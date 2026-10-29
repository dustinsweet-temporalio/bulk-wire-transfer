// Package control is DEMO-ONLY: the presenter's control-service behind the Control Center (:3100).
//
// Everything that exists only to demonstrate the design lives here, isolated from the production-shaped
// services: failure injection (Core faults, latency, holds), chaos through the Docker Engine API (kill, stop,
// pause, restart), load generation (payroll day, retry storm), the per-FI fairness rate limit, encryption key
// rotation and the payload inspector, the Worker Versioning walkthrough, and the one-click reset. Production
// services (bulkwire-service, the workers, core-api) contain only small, labeled DEMO-ONLY hooks.
//
// DEMO-ONLY: there is NO authentication on this API. It is reachable only through the gateway on
// localhost:3100 and holds the Docker socket, so it must never be exposed beyond a presenter's laptop.
package control

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"go.temporal.io/sdk/client"

	"bulkwire/internal/auth"
	"bulkwire/internal/claimcheck"
	"bulkwire/internal/codec"
	"bulkwire/internal/config"
	"bulkwire/internal/coreclient"
)

// Service holds the control-service dependencies.
type Service struct {
	Log      *slog.Logger
	Cfg      config.Config
	Temporal client.Client // encrypting DataConverter
	Raw      client.Client // DEFAULT DataConverter: shows payloads exactly as the Temporal Service stores them
	Codec    *codec.Codec
	Docker   ContainerAPI
	Core     *coreclient.Client
	Store    claimcheck.Store
	Tokens   *auth.Tokens
	HTTP     *http.Client
	// WorkerMetrics maps a worker name to its Prometheus endpoint (WORKER_METRICS_URLS).
	WorkerMetrics map[string]string
}

// Routes wires the API. The gateway maps /api/control/* to /api/*.
func (s *Service) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/containers/{service}/{action}", s.containerAction)
	mux.HandleFunc("POST /api/core/{fiId}/faults", s.coreProxy("/admin/faults"))
	mux.HandleFunc("POST /api/core/{fiId}/latency", s.coreProxy("/admin/latency"))
	mux.HandleFunc("POST /api/core/{fiId}/stats/reset", s.coreProxy("/admin/stats/reset"))
	mux.HandleFunc("POST /api/core/{fiId}/accounts/{acct}/hold", s.coreHold)
	mux.HandleFunc("GET /api/core/stats", s.coreStats)
	mux.HandleFunc("GET /api/batches", s.batches)
	mux.HandleFunc("GET /api/draft-defaults/{fiId}", s.getDraftDefaults)
	mux.HandleFunc("PUT /api/draft-defaults/{fiId}", s.putDraftDefaults)
	mux.HandleFunc("GET /api/fairness", s.getFairness)
	mux.HandleFunc("PUT /api/fairness", s.putFairness)
	mux.HandleFunc("POST /api/load/payroll-day", s.payrollDay)
	mux.HandleFunc("POST /api/load/retry-storm", s.retryStorm)
	mux.HandleFunc("POST /api/claimcheck/expire", s.expireStaged)
	mux.HandleFunc("GET /api/encryption/keys", s.keys)
	mux.HandleFunc("POST /api/encryption/rotate", s.rotateKey)
	mux.HandleFunc("GET /api/encryption/inspect", s.inspect)
	mux.HandleFunc("GET /api/versioning", s.versioning)
	mux.HandleFunc("POST /api/versioning/set-current", s.setCurrent)
	mux.HandleFunc("POST /api/versioning/move-pinned", s.movePinned)
	mux.HandleFunc("GET /api/metrics", s.metrics)
	mux.HandleFunc("POST /api/reset", s.reset)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

func timeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
