// DEMO-ONLY: per-FI draft defaults, held in memory and initialized from the environment.
//
// WHY this hook exists: the presenter changes these from the Control Center (e.g., a 60 s retry budget to
// demonstrate budget exhaustion, or a 2-minute approval window to demonstrate expiry). The values are COPIED
// into BatchInput when a workflow starts, so a running batch never changes behavior mid-flight. Only
// control-service calls GET/PUT /internal/draft-defaults.
package api

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"bulkwire/internal/config"
	"bulkwire/internal/routing"
)

// FIDefaults are one FI's settings for the next batch that starts.
type FIDefaults struct {
	ChunkSize        int    `json:"chunkSize"`
	Concurrency      int    `json:"concurrency"`
	ApprovalWindow   string `json:"approvalWindow"`   // Go duration, e.g. "24h", "2m"
	RetryBudget      string `json:"retryBudget"`      // e.g. "30m", "60s"
	RetryMaxInterval string `json:"retryMaxInterval"` // e.g. "20s"
}

type parsedDefaults struct {
	chunkSize, concurrency                        int
	approvalWindow, retryBudget, retryMaxInterval time.Duration
}

// parse validates one FI's defaults. Chunk size has no artificial floor: any size from 1 up to the largest
// batch (maxRows) is allowed. WHY: the right size depends on the downstream call (one stored-procedure call per
// chunk here); what actually bounds a batch is its chunk COUNT, which MAX_CHUNKS_PER_BATCH guards (commands.go).
func (d FIDefaults) parse(maxRows int) (parsedDefaults, error) {
	var p parsedDefaults
	var err error
	if d.ChunkSize < 1 || d.ChunkSize > maxRows {
		return p, fmt.Errorf("chunkSize must be 1..%d", maxRows)
	}
	if d.Concurrency < 1 || d.Concurrency > 8 {
		return p, fmt.Errorf("concurrency must be 1..8")
	}
	p.chunkSize, p.concurrency = d.ChunkSize, d.Concurrency
	for _, f := range []struct {
		name string
		in   string
		out  *time.Duration
	}{{"approvalWindow", d.ApprovalWindow, &p.approvalWindow}, {"retryBudget", d.RetryBudget, &p.retryBudget},
		{"retryMaxInterval", d.RetryMaxInterval, &p.retryMaxInterval}} {
		if *f.out, err = time.ParseDuration(f.in); err != nil || *f.out <= 0 {
			return p, fmt.Errorf("%s must be a positive duration such as 30s, 2m, 24h", f.name)
		}
	}
	return p, nil
}

// DraftDefaults is the in-memory per-FI store.
type DraftDefaults struct {
	mu      sync.RWMutex
	byFI    map[string]FIDefaults
	maxRows int
}

// NewDraftDefaults initializes every configured FI from the environment defaults, with each FI's chunk
// concurrency from config/routing.yaml (fiDefaults) when it has one.
func NewDraftDefaults(cfg config.Config, routes routing.Config) *DraftDefaults {
	d := &DraftDefaults{byFI: map[string]FIDefaults{}, maxRows: cfg.Defaults.MaxRows}
	for _, fi := range cfg.Core.FiIDs() {
		d.byFI[fi] = FIDefaults{
			ChunkSize: cfg.Defaults.ChunkSize, Concurrency: routes.ChunkConcurrency(fi, cfg.Defaults.Concurrency),
			ApprovalWindow: cfg.Defaults.ApprovalWindow.String(), RetryBudget: cfg.Defaults.RetryBudget.String(),
			RetryMaxInterval: cfg.Defaults.RetryMaxInterval.String(),
		}
	}
	return d
}

func (d *DraftDefaults) get(fiID string) (parsedDefaults, error) {
	d.mu.RLock()
	v, ok := d.byFI[fiID]
	d.mu.RUnlock()
	if !ok {
		return parsedDefaults{}, fmt.Errorf("no draft defaults for FI %q", fiID)
	}
	return v.parse(d.maxRows)
}

func (s *Server) getDraftDefaults(w http.ResponseWriter, _ *http.Request) {
	s.Defaults.mu.RLock()
	defer s.Defaults.mu.RUnlock()
	writeJSON(w, http.StatusOK, s.Defaults.byFI)
}

// putDraftDefaults replaces the defaults of the caller's FI ({ "lakeshore": {...} }); the internal token decides
// which FI that is.
func (s *Server) putDraftDefaults(w http.ResponseWriter, r *http.Request) {
	var body map[string]FIDefaults
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	for fi := range body { // a token may change only its own FI's defaults
		if !sameFI(w, r, fi) {
			return
		}
	}
	s.Defaults.mu.Lock()
	defer s.Defaults.mu.Unlock()
	for fi, v := range body {
		if _, ok := s.Defaults.byFI[fi]; !ok {
			writeError(w, http.StatusBadRequest, "UNKNOWN_FI", "unknown FI "+fi)
			return
		}
		if _, err := v.parse(s.Defaults.maxRows); err != nil {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", fi+": "+err.Error())
			return
		}
	}
	for fi, v := range body {
		s.Defaults.byFI[fi] = v
	}
	writeJSON(w, http.StatusOK, s.Defaults.byFI)
}
