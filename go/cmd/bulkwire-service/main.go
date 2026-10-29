// Command bulkwire-service is the stateless Go HTTP API behind the FI portals: sign-in, staging,
// Update-with-Start commands, Query/Describe reads, and Visibility lists (internal/api). It listens on :8090.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bulkwire/internal/api"
	"bulkwire/internal/auth"
	"bulkwire/internal/claimcheck"
	"bulkwire/internal/codec"
	"bulkwire/internal/config"
	"bulkwire/internal/coreclient"
	"bulkwire/internal/temporalx"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("bulkwire-service stopped", "err", err)
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

	// Every client encrypts: Update arguments and Query results are payloads too.
	c, err := temporalx.Dial(ctx, temporalx.DialOptions{DataConverter: codec.NewDataConverter(cdc), Logger: log})
	if err != nil {
		return err
	}
	defer c.Close()
	if err := temporalx.VerifySearchAttributes(ctx, c, cfg.Temporal.Namespace, log); err != nil {
		return err
	}
	store, err := claimcheck.New(ctx, cfg.ClaimCheck)
	if err != nil {
		return err
	}

	srv := &api.Server{
		Log: log, Temporal: c, Store: store, Core: coreclient.New(cfg.Core.Endpoints, cfg.Core.InternalTokens),
		Tokens: auth.NewTokens(cfg.AuthSigningKey), Cfg: cfg, Defaults: api.NewDraftDefaults(cfg), Now: time.Now,
	}
	httpSrv := &http.Server{Addr: ":8090", Handler: srv.Routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	log.Info("bulkwire-service listening", "addr", httpSrv.Addr, "claimCheck", store.Name())
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
