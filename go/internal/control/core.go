// DEMO-ONLY: proxies to each FI's Core admin API (faults, latency, holds, stats) and to bulkwire-service's
// draft-defaults hook. Each FI has its own Core, so every call names the FI.
package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

func (s *Service) coreProxy(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = decode(r, &body)
		var out any
		ctx, cancel := timeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := s.Core.Do(ctx, r.PathValue("fiId"), http.MethodPost, path, body, &out); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		s.Log.Info("DEMO-ONLY Core admin", "fi", r.PathValue("fiId"), "path", path)
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Service) coreHold(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OnHold bool   `json:"onHold"`
		Reason string `json:"reason"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var out any
	if err := s.Core.Do(r.Context(), r.PathValue("fiId"), http.MethodPost, "/admin/accounts/"+r.PathValue("acct")+"/hold", body, &out); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// coreStats merges every Core's call statistics for the load charts (polled every second by the Control Center).
func (s *Service) coreStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.collectCore(r, "/admin/stats"))
}

// collectCore calls the same admin GET on every FI's Core in parallel; a down Core reports its error instead.
func (s *Service) collectCore(r *http.Request, path string) map[string]any {
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := map[string]any{}
	for _, fi := range s.Cfg.Core.FiIDs() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := timeout(r.Context(), 3*time.Second)
			defer cancel()
			var v any
			if err := s.Core.Do(ctx, fi, http.MethodGet, path, nil, &v); err != nil {
				v = map[string]string{"error": err.Error()}
			}
			mu.Lock()
			out[fi] = v
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// ---- draft defaults (bulkwire-service /internal/draft-defaults) --------------------------------------------

// bulkwire calls one of bulkwire-service's internal hooks on behalf of one FI, with that FI's token: a PUT may
// change only that FI's defaults.
func (s *Service) bulkwire(r *http.Request, fiID, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, s.Cfg.BulkwireURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", s.Cfg.Core.TokenFor(fiID))
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return &httpError{status: resp.StatusCode, body: string(data)}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string { return http.StatusText(e.status) + ": " + e.body }

func (s *Service) getDraftDefaults(w http.ResponseWriter, r *http.Request) {
	var all map[string]any
	if err := s.bulkwire(r, r.PathValue("fiId"), http.MethodGet, "/internal/draft-defaults", nil, &all); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, all[r.PathValue("fiId")])
}

func (s *Service) putDraftDefaults(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var all map[string]any
	if err := s.bulkwire(r, r.PathValue("fiId"), http.MethodPut, "/internal/draft-defaults", map[string]any{r.PathValue("fiId"): body}, &all); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, all[r.PathValue("fiId")])
}

// envDefaults are the .env draft defaults, used by reset.
func (s *Service) envDefaults(fiID string) map[string]any {
	d := s.Cfg.Defaults
	return map[string]any{"chunkSize": d.ChunkSize, "concurrency": s.Routing.ChunkConcurrency(fiID, d.Concurrency), "approvalWindow": d.ApprovalWindow.String(),
		"retryBudget": d.RetryBudget.String(), "retryMaxInterval": d.RetryMaxInterval.String()}
}
