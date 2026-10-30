// Command worker hosts the BulkWireBatch workflow and its Activities. One process runs several SDK workers:
//   - one on the shared task queue "bulkwire": the workflow and PlanChunks (claim-check only);
//   - one per Core task queue in config/routing.yaml: only the Core-calling Activities, with that queue's rate
//     limit and per-worker cap, and Core clients for exactly the FIs that queue serves.
//
// Two identical containers (worker-a, worker-b) run build v1 and poll every queue; either can execute any
// batch, so killing one only pauses the chunks it was running until Temporal hands them to the other.
// PATTERN: Worker Versioning (Pinned) — https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning
// Every worker in the process joins deployment "bulkwire" as build WORKER_BUILD_ID, and the workflow is
// registered PINNED: a batch can wait up to its approval window across deployments, and a pinned execution
// always finishes on the build it started on, so workflow code can change without patching. A pinned
// workflow's Activities are dispatched to ITS version on whatever task queue they target, so each version
// needs at least one poller on the shared queue and on every Core queue. Here every process polls all of them.
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
	"bulkwire/internal/routing"
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
	routes, err := routing.Load(cfg.RoutingPath)
	if err == nil {
		err = routes.Validate(cfg.Core.FiIDs())
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
	all := coreclient.New(cfg.Core.Endpoints, cfg.Core.InternalTokens)
	for _, fi := range cfg.Core.FiIDs() {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if err := all.Healthz(hctx, fi); err != nil {
			log.Warn("Core not reachable at startup; its batches will retry until it is", "fi", fi, "err", err)
		}
		cancel()
	}

	opts := worker.Options{
		// WHY 20: protects this worker process, which on the shared queue runs only PlanChunks (a manifest read).
		// Core load is governed per Core queue (below), not here.
		MaxConcurrentActivityExecutionSize: 20,
		// WHY: eager Activity execution hands a newly scheduled Activity straight back to this worker in the
		// Workflow Task response, bypassing the task queue's matching service. The Core queues' rate limits and
		// Fairness are enforced by matching, so every Activity must be dispatched through its task queue.
		DisableEagerActivities: true,
		// WHY 8 s: on SIGTERM the worker stops polling and lets in-flight Core calls finish and report, inside
		// Docker's 10 s stop grace period. The SDK default (0) cancels them at once, so each one would sit until
		// its 60 s StartToClose before being retried elsewhere, which is crash behavior, not a graceful drain.
		// PRODUCTION NOTE: size this to your slowest normal Activity and the platform's termination grace period.
		WorkerStopTimeout: 8 * time.Second,
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

	shared := worker.New(c, cfg.Temporal.TaskQueue, opts)
	shared.RegisterWorkflowWithOptions(workflow.BulkWireBatch, register)
	activities.RegisterShared(shared, &activities.Activities{ClaimCheck: store})
	workers := []worker.Worker{shared}
	log.Info("worker starting", "taskQueue", cfg.Temporal.TaskQueue, "deployment", cfg.Temporal.DeploymentName,
		"versioning", cfg.Temporal.VersioningEnabled, "claimCheck", store.Name())

	// PATTERN: Task Queue per Downstream (internal/routing). One worker per Core queue, in the same deployment
	// version as the shared worker (same opts.DeploymentOptions), registering only the Core Activities.
	for _, q := range routes.Queues() {
		qopts := opts
		qopts.DisableWorkflowWorker = true // this queue carries Activities only
		// WHY: TaskQueueActivitiesPerSecond is enforced by the Temporal Service for the whole queue, across all
		// workers; it caps how fast Core Activities START. `temporal task-queue config set --queue-rps-limit`
		// (or the Control Center) overrides it without a deployment.
		qopts.TaskQueueActivitiesPerSecond = q.ActivitiesPerSecond
		// WHY: caps how many of this queue's Activities THIS process runs at once (protects the worker).
		qopts.MaxConcurrentActivityExecutionSize = q.MaxConcurrentActivities
		// Core clients for exactly the FIs this queue serves: a worker on bulkwire-core-lakeshore holds no
		// credentials for Prairie's Core.
		core := coreclient.New(routing.Subset(cfg.Core.Endpoints, q.FIs), routing.Subset(cfg.Core.InternalTokens, q.FIs))
		w := worker.New(c, q.Name, qopts)
		activities.RegisterCore(w, &activities.Activities{Core: core, ClaimCheck: store, AlertAttemptThreshold: cfg.Core.AlertAttemptThreshold})
		workers = append(workers, w)
		log.Info("core queue worker starting", "taskQueue", q.Name, "fis", q.FIs,
			"activitiesPerSecond", q.ActivitiesPerSecond, "maxConcurrentActivities", q.MaxConcurrentActivities)
	}
	// SIGTERM (docker stop) drains gracefully; SIGKILL (docker kill) is the crash scenario.
	return runAll(workers, worker.InterruptCh())
}

// runAll runs every worker until the interrupt arrives or one of them fails, then stops them all. Each worker
// drains its own in-flight Activities (WorkerStopTimeout) as it stops.
func runAll(workers []worker.Worker, interrupt <-chan any) error {
	stop := make(chan any)
	errs := make(chan error, len(workers))
	for _, w := range workers {
		go func() { errs <- w.Run(stop) }()
	}
	pending := len(workers)
	var first error
	select {
	case <-interrupt:
	case first = <-errs: // a worker failed to start or hit a fatal error: stop the others too
		pending--
	}
	close(stop) // a closed channel reaches every worker's Run
	for ; pending > 0; pending-- {
		if err := <-errs; first == nil {
			first = err
		}
	}
	return first
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
