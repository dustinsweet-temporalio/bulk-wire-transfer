// Command worker hosts the BulkWireBatch workflow and its Activities on task queue "bulkwire".
//
// Two identical containers (worker-a, worker-b) run build v1; either can execute any batch, so killing one
// only pauses the chunks it was running until Temporal hands them to the other.
// PATTERN: Worker Versioning (Pinned) — https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning
// The worker joins deployment "bulkwire" as build WORKER_BUILD_ID, and the workflow is registered PINNED:
// a batch can wait up to its approval window across deployments, and a pinned execution always finishes on
// the build it started on, so workflow code can change without patching.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	tallyprom "github.com/uber-go/tally/v4/prometheus"

	"github.com/uber-go/tally/v4"
	sdktally "go.temporal.io/sdk/contrib/tally"
	"go.temporal.io/sdk/worker"
	sdkworkflow "go.temporal.io/sdk/workflow"

	"bulkwire/internal/activities"
	"bulkwire/internal/claimcheck"
	"bulkwire/internal/codec"
	"bulkwire/internal/config"
	"bulkwire/internal/coreclient"
	"bulkwire/internal/temporalx"
	"bulkwire/internal/workflow"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("worker stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err == nil {
		err = cfg.RequireCore()
	}
	if err != nil {
		return err
	}
	ctx := context.Background()
	log = log.With("buildId", cfg.Temporal.BuildID)

	// PATTERN: Encrypting Data Converter. The keyring hot-reloads, so a key rotation needs no restart.
	cdc, err := codec.LoadFile(cfg.KeyringPath)
	if err != nil {
		return err
	}
	go cdc.Watch(ctx, cfg.KeyringPath, 2*time.Second, log)

	// Prometheus metrics: SDK metrics plus bulkwire_core_attempts_over_threshold (PATTERN: Retry Alerting via Metrics).
	reporter := tallyprom.NewReporter(tallyprom.Options{})
	scope, closer := tally.NewRootScope(tally.ScopeOptions{
		CachedReporter:  reporter,
		Separator:       tallyprom.DefaultSeparator,
		SanitizeOptions: &sdktally.PrometheusSanitizeOptions,
	}, time.Second)
	defer closer.Close()
	go serveMetrics(log, reporter.HTTPHandler())

	c, err := temporalx.Dial(ctx, temporalx.DialOptions{
		DataConverter:  codec.NewDataConverter(cdc),
		Logger:         log,
		MetricsHandler: sdktally.NewMetricsHandler(sdktally.NewPrometheusNamingScope(scope)),
	})
	if err != nil {
		return err
	}
	defer c.Close()
	if err := temporalx.VerifySearchAttributes(ctx, c, cfg.Temporal.Namespace, log); err != nil {
		return err
	}

	// Fail fast if the claim-check store is unreachable; only warn if a Core is down, because the OTHER FI's
	// batches must keep working (one Core per FI).
	store, err := claimcheck.New(ctx, cfg.ClaimCheck)
	if err != nil {
		return err
	}
	core := coreclient.New(cfg.Core.Endpoints, cfg.Core.InternalToken)
	for _, fi := range cfg.Core.FiIDs() {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if err := core.Healthz(hctx, fi); err != nil {
			log.Warn("Core not reachable at startup; its batches will retry until it is", "fi", fi, "err", err)
		}
		cancel()
	}

	opts := worker.Options{
		// WHY 20: protects this worker process. Core load is governed by each batch's semaphore and by the
		// per-FI Fairness rate limit on the task queue, not by this number.
		MaxConcurrentActivityExecutionSize: 20,
		// WHY: eager Activity execution hands a newly scheduled Activity straight back to this worker in the
		// Workflow Task response, bypassing the task queue's matching service. Fairness and the per-FI rate
		// limit are enforced by matching, so every Core Activity must be dispatched through the task queue.
		DisableEagerActivities: true,
	}
	register := sdkworkflow.RegisterOptions{Name: workflow.WorkflowName}
	if cfg.Temporal.VersioningEnabled {
		opts.DeploymentOptions = worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.WorkerDeploymentVersion{DeploymentName: cfg.Temporal.DeploymentName, BuildID: cfg.Temporal.BuildID},
		}
		register.VersioningBehavior = sdkworkflow.VersioningBehaviorPinned
	}
	// WORKER_VERSIONING_ENABLED=false runs an unversioned worker (documented alternative; versioning is the
	// recommended default).

	w := worker.New(c, cfg.Temporal.TaskQueue, opts)
	w.RegisterWorkflowWithOptions(workflow.BulkWireBatch, register)
	w.RegisterActivity(&activities.Activities{Core: core, ClaimCheck: store, AlertAttemptThreshold: cfg.Core.AlertAttemptThreshold})

	log.Info("worker starting", "taskQueue", cfg.Temporal.TaskQueue, "deployment", cfg.Temporal.DeploymentName,
		"versioning", cfg.Temporal.VersioningEnabled, "claimCheck", store.Name())
	// SIGTERM (docker stop) drains gracefully; SIGKILL (docker kill) is the crash scenario.
	return w.Run(worker.InterruptCh())
}

func serveMetrics(log *slog.Logger, h http.Handler) {
	port := os.Getenv("METRICS_PORT")
	if port == "" {
		port = "9464"
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", h)
	if err := http.ListenAndServe(":"+port, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("metrics server stopped", "err", err)
	}
}
