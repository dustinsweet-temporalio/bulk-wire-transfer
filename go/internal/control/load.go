// DEMO-ONLY load generation, acting as demo users through bulkwire-service's PUBLIC API (the same calls a
// portal makes): payroll day (several large batches at once) and the retry storm (many Draft requests for one
// batch, each with a different Idempotency-Key, to show that the Business Workflow ID still yields ONE execution).
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"sync"
	"time"

	"go.temporal.io/api/workflowservice/v1"

	"bulkwire/internal/auth"
	"bulkwire/internal/temporalx"
)

// portal calls bulkwire-service's public API as one demo user.
type portal struct {
	s     *Service
	token string
	fiID  string
}

func (s *Service) portalAs(fiID, userID string) (*portal, error) {
	id, ok := auth.FindUser(fiID, userID)
	if !ok {
		return nil, fmt.Errorf("no demo user %s at %s", userID, fiID)
	}
	tok, err := s.Tokens.Mint(id)
	if err != nil {
		return nil, err
	}
	return &portal{s: s, token: tok, fiID: fiID}, nil
}

func (p *portal) do(ctx context.Context, method, path string, body io.Reader, contentType string, headers map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.s.Cfg.BulkwireURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := p.s.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, nil
}

// stage downloads a sample (fresh dates) and uploads it, returning the batch ID.
func (p *portal) stage(ctx context.Context, sample string) (string, error) {
	status, csv, err := p.do(ctx, http.MethodGet, "/api/samples/"+sample+".csv", nil, "", nil)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("download %s: %d %v", sample, status, err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", sample+".csv")
	_, _ = fw.Write(csv)
	_ = mw.Close()
	status, data, err := p.do(ctx, http.MethodPost, "/api/batches", &buf, mw.FormDataContentType(), nil)
	if err != nil || status != http.StatusCreated {
		return "", fmt.Errorf("upload %s: %d %s %v", sample, status, data, err)
	}
	var sum struct {
		BatchID string `json:"batchId"`
		Status  string `json:"status"`
	}
	_ = json.Unmarshal(data, &sum)
	if sum.Status != "VALID" {
		return sum.BatchID, fmt.Errorf("%s staged as %s", sample, sum.Status)
	}
	return sum.BatchID, nil
}

func (p *portal) draft(ctx context.Context, batchID, key string) (int, error) {
	status, _, err := p.do(ctx, http.MethodPost, "/api/batches/"+batchID+"/draft", nil, "", map[string]string{"Idempotency-Key": key})
	return status, err
}

// payrollDay: for each entry concurrently, sign in as that user, stage the sample, and draft it.
func (s *Service) payrollDay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Batches []struct {
			FiID        string `json:"fiId"`
			UserID      string `json:"userId"`
			Sample      string `json:"sample"`
			Concurrency int    `json:"concurrency"`
		} `json:"batches"`
	}
	if err := decode(r, &body); err != nil || len(body.Batches) == 0 {
		writeError(w, http.StatusBadRequest, "send {batches:[{fiId,userId,sample,concurrency}]}")
		return
	}
	// Concurrency is a per-FI draft default (copied into each workflow's input at start), so it is applied per FI
	// before any batch starts.
	perFI := map[string]int{}
	for _, b := range body.Batches {
		if b.Concurrency > 0 {
			perFI[b.FiID] = max(perFI[b.FiID], b.Concurrency)
		}
	}
	for fi, c := range perFI {
		var cur map[string]any
		if err := s.bulkwire(r, fi, http.MethodGet, "/internal/draft-defaults", nil, &cur); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		d, _ := cur[fi].(map[string]any)
		d["concurrency"] = c
		if err := s.bulkwire(r, fi, http.MethodPut, "/internal/draft-defaults", map[string]any{fi: d}, nil); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	type result struct {
		FiID       string `json:"fiId"`
		UserID     string `json:"userId"`
		Sample     string `json:"sample"`
		BatchID    string `json:"batchId,omitempty"`
		WorkflowID string `json:"workflowId,omitempty"`
		Status     int    `json:"draftStatus,omitempty"`
		Error      string `json:"error,omitempty"`
	}
	results := make([]result, len(body.Batches))
	var wg sync.WaitGroup
	for i, b := range body.Batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := result{FiID: b.FiID, UserID: b.UserID, Sample: b.Sample}
			defer func() { results[i] = res }()
			ctx, cancel := timeout(r.Context(), 90*time.Second)
			defer cancel()
			p, err := s.portalAs(b.FiID, b.UserID)
			if err == nil {
				res.BatchID, err = p.stage(ctx, b.Sample)
			}
			if err == nil {
				res.Status, err = p.draft(ctx, res.BatchID, fmt.Sprintf("payroll-day-%s", res.BatchID))
				res.WorkflowID = temporalx.WorkflowID(b.FiID, res.BatchID)
			}
			if err != nil {
				res.Error = err.Error()
			}
		}()
	}
	wg.Wait()
	s.Log.Info("DEMO-ONLY payroll day started", "batches", len(results))
	writeJSON(w, http.StatusOK, results)
}

// StormResult summarizes a retry storm.
type StormResult struct {
	WorkflowID   string      `json:"workflowId"`
	BatchID      string      `json:"batchId"`
	Requests     int         `json:"requests"`
	StatusCounts map[int]int `json:"statusCounts"`
	Errors       []string    `json:"errors,omitempty"`
	Executions   int64       `json:"executions"` // Visibility count for the Workflow ID: expected 1
}

func aggregateStorm(wfID, batchID string, statuses []int, errs []error) StormResult {
	res := StormResult{WorkflowID: wfID, BatchID: batchID, Requests: len(statuses), StatusCounts: map[int]int{}}
	for i, st := range statuses {
		if errs[i] != nil {
			res.Errors = append(res.Errors, errs[i].Error())
			continue
		}
		res.StatusCounts[st]++
	}
	sort.Strings(res.Errors)
	return res
}

// retryStorm fires N concurrent Draft requests for one batch, each with a DIFFERENT Idempotency-Key (so Update
// IDs do not dedup them), then counts executions for the Workflow ID. USE_EXISTING + the business Workflow ID
// mean every request lands on the same single execution.
func (s *Service) retryStorm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FiID     string `json:"fiId"`
		UserID   string `json:"userId"`
		BatchID  string `json:"batchId"`
		Sample   string `json:"sample"`
		Requests int    `json:"requests"`
	}
	if err := decode(r, &body); err != nil || body.FiID == "" || body.UserID == "" {
		writeError(w, http.StatusBadRequest, "send {fiId, userId, batchId?, sample?, requests}")
		return
	}
	if body.Requests <= 0 || body.Requests > 100 {
		body.Requests = 20
	}
	ctx, cancel := timeout(r.Context(), 120*time.Second)
	defer cancel()
	p, err := s.portalAs(body.FiID, body.UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.BatchID == "" { // no staged batch given: stage a fresh one first
		if body.Sample == "" {
			body.Sample = map[string]string{"lakeshore": "summit-payroll-5000", "prairie": "harvest-2000"}[body.FiID]
		}
		if body.BatchID, err = p.stage(ctx, body.Sample); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	statuses := make([]int, body.Requests)
	errs := make([]error, body.Requests)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range body.Requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			statuses[i], errs[i] = p.draft(ctx, body.BatchID, fmt.Sprintf("storm-%s-%02d", body.BatchID, i))
		}()
	}
	close(start)
	wg.Wait()

	wfID := temporalx.WorkflowID(body.FiID, body.BatchID)
	res := aggregateStorm(wfID, body.BatchID, statuses, errs)
	time.Sleep(2 * time.Second) // Visibility is eventually consistent
	count, err := s.Temporal.CountWorkflow(ctx, &workflowservice.CountWorkflowExecutionsRequest{
		Namespace: s.Cfg.Temporal.Namespace, Query: fmt.Sprintf("WorkflowId = '%s'", wfID)})
	if err == nil {
		res.Executions = count.GetCount()
	}
	s.Log.Info("DEMO-ONLY retry storm", "workflowId", wfID, "requests", body.Requests, "executions", res.Executions)
	writeJSON(w, http.StatusOK, res)
}
