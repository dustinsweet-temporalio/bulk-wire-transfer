// Command control-service is the DEMO-ONLY backend of the Control Center (internal/control): chaos via the Docker
// socket, Core fault injection, load generation, the fairness rate limit, key rotation, the versioning walkthrough,
// and reset. It listens on :8095 inside the compose network and has NO authentication (local demo only).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.temporal.io/sdk/converter"

	"bulkwire/internal/auth"
	"bulkwire/internal/claimcheck"
	"bulkwire/internal/codec"
	"bulkwire/internal/config"
	"bulkwire/internal/control"
	"bulkwire/internal/coreclient"
	"bulkwire/internal/temporalx"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("control-service stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err == nil {
		err = errors.Join(cfg.RequireCore(), cfg.RequireAuth())
	}
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cdc, err := codec.LoadFile(cfg.KeyringPath)
	if err != nil {
		return err
	}
	go cdc.Watch(ctx, cfg.KeyringPath, 2*time.Second, log)

	tc, err := temporalx.Dial(ctx, temporalx.DialOptions{DataConverter: codec.NewDataConverter(cdc), Logger: log})
	if err != nil {
		return err
	}
	defer tc.Close()
	// The "raw" client deliberately has NO codec: the encryption inspector uses it to show payloads exactly as the
	// Temporal Service stores them (ciphertext).
	raw, err := temporalx.Dial(ctx, temporalx.DialOptions{DataConverter: converter.GetDefaultDataConverter(), Logger: log})
	if err != nil {
		return err
	}
	defer raw.Close()
	store, err := claimcheck.New(ctx, cfg.ClaimCheck)
	if err != nil {
		return err
	}

	svc := &control.Service{
		Log: log, Cfg: cfg, Temporal: tc, Raw: raw, Codec: cdc,
		Docker:        control.NewDocker("/var/run/docker.sock", os.Getenv("COMPOSE_PROJECT_NAME"), splitList(os.Getenv("CHAOS_ALLOWLIST"))),
		Core:          coreclient.New(cfg.Core.Endpoints, cfg.Core.InternalToken),
		Store:         store,
		Tokens:        auth.NewTokens(cfg.AuthSigningKey),
		HTTP:          &http.Client{Timeout: 60 * time.Second},
		WorkerMetrics: splitPairs(os.Getenv("WORKER_METRICS_URLS")),
	}
	srv := &http.Server{Addr: ":8095", Handler: svc.Routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("control-service listening (DEMO-ONLY, no auth)", "addr", srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func splitPairs(v string) map[string]string {
	out := map[string]string{}
	for _, p := range splitList(v) {
		if k, val, ok := strings.Cut(p, "="); ok {
			out[k] = val
		}
	}
	return out
}
