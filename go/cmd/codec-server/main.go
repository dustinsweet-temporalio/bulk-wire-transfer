// Command codec-server decodes encrypted payloads for the Temporal Web UI and CLI.
//
// PATTERN: Codec Server — https://docs.temporal.io/codec-server
// The Temporal Service stores only ciphertext. The Web UI (started with --ui-codec-endpoint) and the CLI
// (--codec-endpoint) send payloads here, and this process decrypts them with the same keyring the workers
// use (mounted read-only, hot-reloaded on rotation). Without it, history shows `binary/encrypted` payloads.
//
// PRODUCTION NOTE: authenticate callers (validate the UI's access token, restrict by network) and run this
// inside your own trust boundary; the Temporal Service never sees the keys.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"time"

	"go.temporal.io/sdk/converter"

	"bulkwire/internal/codec"
	"bulkwire/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	cdc, err := codec.LoadFile(cfg.KeyringPath)
	if err != nil {
		log.Error("keyring", "err", err)
		os.Exit(1)
	}
	go cdc.Watch(context.Background(), cfg.KeyringPath, 2*time.Second, log)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","activeKeyId":"` + cdc.ActiveKeyID() + `"}`))
	})
	// Serves POST /encode and POST /decode (the remote codec protocol used by the Web UI and CLI).
	mux.Handle("/", converter.NewPayloadCodecHTTPHandler(cdc))

	log.Info("codec server listening", "addr", ":8089", "corsOrigins", cfg.CORSOrigins)
	if err := http.ListenAndServe(":8089", cors(cfg.CORSOrigins, mux)); err != nil {
		log.Error("codec server stopped", "err", err)
		os.Exit(1)
	}
}

// cors lets the browser-based Temporal Web UI (a different origin) call this server, including the preflight.
func cors(origins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); slices.Contains(origins, origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, X-Namespace, Authorization")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
