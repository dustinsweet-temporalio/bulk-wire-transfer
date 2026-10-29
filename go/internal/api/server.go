// Package api is bulkwire-service's HTTP API (stdlib net/http, Go 1.22+ routing patterns, JSON camelCase).
//
// The service is STATELESS. It holds no batch state in memory or in Redis. Every response is assembled from
// the claim-check store (before a batch is drafted), from Temporal (Query, Describe, Visibility), or by the
// portal from Core directly. Its four jobs:
//  1. Staging (synchronous, no Temporal): parse, validate, write claim-check objects.
//  2. Commands into the workflow: Update-with-Start / Update.
//  3. Reads from the workflow: Query `status`, plus DescribeWorkflowExecution for retry telemetry.
//  4. Lists: Visibility queries on Search Attributes.
//
// The only non-production piece is the DEMO-ONLY draft-defaults hook (defaults.go) used by control-service.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"

	"bulkwire/internal/auth"
	"bulkwire/internal/claimcheck"
	"bulkwire/internal/config"
	"bulkwire/internal/staging"
)

// TemporalClient is the subset of client.Client this service uses (a real client.Client satisfies it; tests
// pass a fake).
type TemporalClient interface {
	NewWithStartWorkflowOperation(options client.StartWorkflowOptions, workflow any, args ...any) client.WithStartWorkflowOperation
	UpdateWithStartWorkflow(ctx context.Context, options client.UpdateWithStartWorkflowOptions) (client.WorkflowUpdateHandle, error)
	UpdateWorkflow(ctx context.Context, options client.UpdateWorkflowOptions) (client.WorkflowUpdateHandle, error)
	QueryWorkflow(ctx context.Context, workflowID, runID, queryType string, args ...any) (converter.EncodedValue, error)
	DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
	ListWorkflow(ctx context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error)
	DescribeTaskQueue(ctx context.Context, taskQueue string, taskQueueType enums.TaskQueueType) (*workflowservice.DescribeTaskQueueResponse, error)
	CheckHealth(ctx context.Context, request *client.CheckHealthRequest) (*client.CheckHealthResponse, error)
}

// Server holds the dependencies of every handler.
type Server struct {
	Log      *slog.Logger
	Temporal TemporalClient
	Store    claimcheck.Store
	Core     staging.AccountChecker
	Tokens   *auth.Tokens
	Cfg      config.Config
	Defaults *DraftDefaults
	Now      func() time.Time
}

// Routes wires every endpoint. The gateway maps /api/bulkwire/* to /api/*; /internal/* is never routed by
// the gateway (service-to-service only).
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Auth and branding
	mux.HandleFunc("GET /api/fi/{fiId}/branding", s.branding)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("GET /api/auth/me", s.user(s.me))

	// Samples
	mux.HandleFunc("GET /api/samples", s.user(s.listSamples))
	mux.HandleFunc("GET /api/samples/{file}", s.user(s.sampleCSV))

	// Staging
	mux.HandleFunc("POST /api/batches", s.user(s.upload))
	mux.HandleFunc("GET /api/batches/{batchId}/staging", s.user(s.stagingView))
	mux.HandleFunc("GET /api/batches/{batchId}/errors.csv", s.user(s.errorsCSV))

	// Commands
	mux.HandleFunc("POST /api/batches/{batchId}/draft", s.user(s.draft))
	mux.HandleFunc("POST /api/batches/{batchId}/cancel", s.user(s.cancel))

	// Reads
	mux.HandleFunc("GET /api/batches/{batchId}", s.user(s.batchStatus))
	mux.HandleFunc("GET /api/batches", s.user(s.listBatches))

	// Bank operations
	mux.HandleFunc("GET /api/ops/attention", s.user(s.opsAttention))
	mux.HandleFunc("POST /api/ops/batches/{batchId}/resolve", s.user(s.resolve))

	// Internal (service-to-service)
	mux.HandleFunc("POST /internal/approvals", s.internal(s.internalApproval))
	mux.HandleFunc("POST /internal/denials", s.internal(s.internalDenial))
	mux.HandleFunc("GET /internal/staged-batches/{fiId}/{batchId}", s.internal(s.internalStagedBatch))
	mux.HandleFunc("GET /internal/draft-defaults", s.internal(s.getDraftDefaults)) // DEMO-ONLY
	mux.HandleFunc("PUT /internal/draft-defaults", s.internal(s.putDraftDefaults)) // DEMO-ONLY

	mux.HandleFunc("GET /healthz", s.healthz)
	return s.logRequests(mux)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	status := map[string]string{"temporal": "ok", "claimCheck": "ok", "backend": s.Store.Name()}
	code := http.StatusOK
	if _, err := s.Temporal.CheckHealth(ctx, &client.CheckHealthRequest{}); err != nil {
		status["temporal"], code = err.Error(), http.StatusServiceUnavailable
	}
	if err := s.Store.Ping(ctx); err != nil {
		status["claimCheck"], code = err.Error(), http.StatusServiceUnavailable
	}
	writeJSON(w, code, status)
}

// ---- helpers ------------------------------------------------------------------------------------------------

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Errors  any    `json:"errors,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Code: code, Message: message})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }

// logRequests writes one line per request with the route PATTERN (never the raw path or query string).
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.Pattern == "GET /healthz" {
			return
		}
		s.Log.Info("request", "route", r.Pattern, "status", rec.status, "ms", time.Since(start).Milliseconds())
	})
}
