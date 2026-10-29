// Reads: live batch status (Query + Describe), lists (Visibility), and bank operations' exception queue.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"

	"bulkwire/internal/auth"
	"bulkwire/internal/claimcheck"
	"bulkwire/internal/staging"
	"bulkwire/internal/temporalx"
	"bulkwire/internal/workflow"
)

// queryTimeout: a Query needs a running worker. Past this, the portal shows the honest degraded banner.
const queryTimeout = 3 * time.Second

// PendingActivity is retry telemetry for one in-flight Activity, from DescribeWorkflowExecution.
type PendingActivity struct {
	ActivityID    string     `json:"activityId"`
	ChunkID       string     `json:"chunkId"`
	Phase         string     `json:"phase"` // draft | authorize | reject | plan | gentxbatch | policy | status-...
	Attempt       int32      `json:"attempt"`
	State         string     `json:"state"` // SCHEDULED | STARTED | ...
	LastFailure   string     `json:"lastFailure,omitempty"`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
	LastStartedAt *time.Time `json:"lastStartedAt,omitempty"`
}

// BatchStatus is GET /api/batches/{batchId}.
type BatchStatus struct {
	Source        string              `json:"source"` // workflow | staging
	Staging       *StagingSummary     `json:"staging"`
	View          *workflow.BatchView `json:"view"`
	Ops           OpsInfo             `json:"ops"`
	Degraded      *Degraded           `json:"degraded,omitempty"`
	WorkflowID    string              `json:"workflowId"`
	TemporalUIURL string              `json:"temporalUiUrl"`
}

type OpsInfo struct {
	PendingActivities []PendingActivity `json:"pendingActivities"`
}

// Degraded is returned when the workflow exists but its Query cannot be answered (no worker polling, or the
// Query failed). The batch is safe: its state lives in Temporal, not in the workers.
type Degraded struct {
	Reason            string            `json:"reason"` // NO_WORKERS | QUERY_FAILED
	Phase             string            `json:"phase"`  // BulkWireStatus Search Attribute, from Describe
	NeedsAttention    bool              `json:"needsAttention"`
	PendingActivities []PendingActivity `json:"pendingActivities"`
	Pollers           int               `json:"pollers"`
}

// batchStatus serves the portal's live view.
// This endpoint is polled every second by each viewer. A Query writes nothing to history, so this is cheap.
// At high fan-out (many viewers per batch), add a short server-side cache: that is still not a projection
// store.
func (s *Server) batchStatus(w http.ResponseWriter, r *http.Request, c auth.Claims) {
	batchID := r.PathValue("batchId")
	wfID := temporalx.WorkflowID(c.FiID, batchID)
	out := BatchStatus{WorkflowID: wfID, Ops: OpsInfo{PendingActivities: []PendingActivity{}},
		TemporalUIURL: fmt.Sprintf("%s/namespaces/%s/workflows/%s", s.Cfg.Temporal.UIURL, s.Cfg.Temporal.Namespace, url.PathEscape(wfID))}

	m, err := staging.ReadManifest(r.Context(), s.Store, c.FiID, batchID)
	manifestFound := err == nil
	if manifestFound {
		if !c.IsBankOps() && m.CustomerID != c.CustomerID {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "batch not found")
			return
		}
		sum := s.summaryOf(m)
		out.Staging = &sum
	} else if !errors.Is(err, claimcheck.ErrNotFound) {
		s.Log.Warn("claim check unavailable while reading batch status", "err", err)
	}

	// PATTERN: Query for State — the live view comes ONLY from the workflow.
	qctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	val, qerr := s.Temporal.QueryWorkflow(qctx, wfID, "", workflow.QueryStatus)
	var notFound *serviceerror.NotFound
	switch {
	case qerr == nil:
		var view workflow.BatchView
		if err := val.Get(&view); err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "unreadable workflow view")
			return
		}
		if !c.IsBankOps() && view.CustomerID != c.CustomerID {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "batch not found")
			return
		}
		out.Source, out.View = "workflow", &view
		// PATTERN: Describe for Retry Telemetry — https://docs.temporal.io/encyclopedia/retry-policies
		// Retries are invisible to workflow code by design; DescribeWorkflowExecution exposes each pending
		// Activity's attempt count and last failure, which is what the "Retrying · attempt 4" tiles show.
		if desc, err := s.Temporal.DescribeWorkflowExecution(r.Context(), wfID, ""); err == nil {
			out.Ops.PendingActivities = pendingActivities(desc)
		}
	case errors.As(qerr, &notFound):
		if !manifestFound {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "batch not found")
			return
		}
		out.Source = "staging"
	default:
		// Degraded mode: the workflow may exist but no worker answered the Query.
		desc, err := s.Temporal.DescribeWorkflowExecution(r.Context(), wfID, "")
		if errors.As(err, &notFound) && manifestFound {
			out.Source = "staging"
			break
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "ORCHESTRATION_UNAVAILABLE", "Batch status is temporarily unavailable.")
			return
		}
		sa := searchAttributes(desc.GetWorkflowExecutionInfo().GetSearchAttributes())
		if !c.IsBankOps() && sa.customerID != c.CustomerID {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "batch not found")
			return
		}
		d := &Degraded{Reason: "QUERY_FAILED", Phase: sa.status, NeedsAttention: sa.needsAttention, PendingActivities: pendingActivities(desc)}
		// A live worker answers a Query in milliseconds, so a Query that times out (or that the service rejects
		// with "no poller seen") means no worker is polling. The poller list is informational only: the service
		// keeps showing a stopped worker's pollers for up to a minute after their last poll.
		var deadline *serviceerror.DeadlineExceeded
		if errors.Is(qerr, context.DeadlineExceeded) || errors.As(qerr, &deadline) || strings.Contains(qerr.Error(), "no poller seen") {
			d.Reason = "NO_WORKERS"
		}
		s.Log.Warn("status query failed; serving degraded view", "workflowId", wfID, "reason", d.Reason, "err", qerr)
		if tq, err := s.Temporal.DescribeTaskQueue(r.Context(), s.Cfg.Temporal.TaskQueue, enumspb.TASK_QUEUE_TYPE_WORKFLOW); err == nil {
			d.Pollers = len(tq.GetPollers())
		}
		out.Source, out.Degraded = "workflow", d
	}
	writeJSON(w, http.StatusOK, out)
}

// pendingActivities maps Describe's pending Activities back to chunks via the Activity ID
// "{draft|authorize|reject}/{chunkId}/r{cycle}". Only Activities that are retrying are included.
func pendingActivities(desc *workflowservice.DescribeWorkflowExecutionResponse) []PendingActivity {
	out := []PendingActivity{}
	for _, p := range desc.GetPendingActivities() {
		if p.GetAttempt() <= 1 && p.GetLastFailure() == nil {
			continue
		}
		phase, chunkID := parseActivityID(p.GetActivityId())
		pa := PendingActivity{ActivityID: p.GetActivityId(), ChunkID: chunkID, Phase: phase, Attempt: p.GetAttempt(),
			State: strings.TrimPrefix(enumspb.PendingActivityState_name[int32(p.GetState())], "PENDING_ACTIVITY_STATE_"), LastFailure: p.GetLastFailure().GetMessage()}
		if t := p.GetNextAttemptScheduleTime(); t != nil {
			v := t.AsTime()
			pa.NextAttemptAt = &v
		}
		if t := p.GetLastStartedTime(); t != nil {
			v := t.AsTime()
			pa.LastStartedAt = &v
		}
		out = append(out, pa)
	}
	return out
}

func parseActivityID(id string) (phase, chunkID string) {
	parts := strings.Split(id, "/")
	switch {
	case len(parts) == 3 && (parts[0] == "draft" || parts[0] == "authorize" || parts[0] == "reject"):
		return parts[0], parts[1]
	case len(parts) > 0:
		return parts[0], ""
	}
	return "", ""
}

type saValues struct {
	status, fiID, customerID, initiatedBy, deploymentVersion string
	needsAttention                                           bool
}

// searchAttributes decodes Search Attribute values with the DEFAULT converter: they are not payloads of the
// workflow, so they are never encrypted.
func searchAttributes(sa *commonpb.SearchAttributes) saValues {
	dc := converter.GetDefaultDataConverter()
	var v saValues
	get := func(name string, out any) {
		if p, ok := sa.GetIndexedFields()[name]; ok {
			_ = dc.FromPayload(p, out)
		}
	}
	get(temporalx.SABulkWireStatus.GetName(), &v.status)
	get(temporalx.SAFiID.GetName(), &v.fiID)
	get(temporalx.SACustomerID.GetName(), &v.customerID)
	get(temporalx.SAInitiatedBy.GetName(), &v.initiatedBy)
	get(temporalx.SANeedsAttention.GetName(), &v.needsAttention)
	get(temporalx.SAWorkerDeploymentVersion, &v.deploymentVersion)
	return v
}

// queryView queries a workflow's status with the short Query timeout.
func (s *Server) queryView(ctx context.Context, wfID string) (workflow.BatchView, bool) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	val, err := s.Temporal.QueryWorkflow(ctx, wfID, "", workflow.QueryStatus)
	if err != nil {
		return workflow.BatchView{}, false
	}
	var v workflow.BatchView
	return v, val.Get(&v) == nil
}

// BatchListItem is one row of a batch list.
type BatchListItem struct {
	BatchID         string           `json:"batchId"`
	WorkflowID      string           `json:"workflowId"`
	Status          string           `json:"status"` // BulkWireStatus
	NeedsAttention  bool             `json:"needsAttention"`
	CustomerID      string           `json:"customerId"`
	InitiatedBy     string           `json:"initiatedBy"`
	ExecutionStatus string           `json:"executionStatus"`
	StartTime       *time.Time       `json:"startTime"`
	CloseTime       *time.Time       `json:"closeTime"`
	FileName        string           `json:"fileName,omitempty"`
	Totals          *workflow.Totals `json:"totals,omitempty"`
}

const maxEnriched = 25

// listBatches lists the caller's batches from Visibility (PATTERN: Search Attributes for Operations).
// Visibility is eventually consistent: lists may lag a second or two behind the live Query.
func (s *Server) listBatches(w http.ResponseWriter, r *http.Request, c auth.Claims) {
	q := fmt.Sprintf("WorkflowType = '%s' AND FiId = '%s'", workflow.WorkflowName, quote(c.FiID))
	switch scope := r.URL.Query().Get("scope"); scope {
	case "mine":
		q += fmt.Sprintf(" AND InitiatedBy = '%s'", quote(c.UserID()))
	case "", "company":
		if c.CustomerID != "" {
			q += fmt.Sprintf(" AND CustomerId = '%s'", quote(c.CustomerID))
		}
	case "approvals":
		// WHY ExecutionStatus: a terminated execution (an operator tool, e.g. the demo reset) keeps its last
		// BulkWireStatus, so without it a terminated batch would still be waiting for approval.
		q += fmt.Sprintf(" AND CustomerId = '%s' AND BulkWireStatus = '%s' AND ExecutionStatus = 'Running'", quote(c.CustomerID), workflow.PhaseDrafted)
	default:
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "scope must be mine, company, or approvals")
		return
	}
	items, err := s.list(r.Context(), q, 100)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ORCHESTRATION_UNAVAILABLE", "Batch lists are temporarily unavailable.")
		return
	}
	s.enrich(r.Context(), items)
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) list(ctx context.Context, query string, pageSize int32) ([]*BatchListItem, error) {
	resp, err := s.Temporal.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		Namespace: s.Cfg.Temporal.Namespace, PageSize: pageSize, Query: query,
	})
	if err != nil {
		return nil, err
	}
	items := []*BatchListItem{}
	for _, e := range resp.GetExecutions() {
		sa := searchAttributes(e.GetSearchAttributes())
		wfID := e.GetExecution().GetWorkflowId()
		item := &BatchListItem{BatchID: wfID[strings.LastIndex(wfID, "/")+1:], WorkflowID: wfID, Status: sa.status,
			NeedsAttention: sa.needsAttention, CustomerID: sa.customerID, InitiatedBy: sa.initiatedBy,
			ExecutionStatus: strings.TrimPrefix(enumspb.WorkflowExecutionStatus_name[int32(e.GetStatus())], "WORKFLOW_EXECUTION_STATUS_")}
		if t := e.GetStartTime(); t != nil {
			v := t.AsTime()
			item.StartTime = &v
		}
		if t := e.GetCloseTime(); t != nil {
			v := t.AsTime()
			item.CloseTime = &v
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].StartTime != nil && items[j].StartTime != nil && items[i].StartTime.After(*items[j].StartTime)
	})
	return items, nil
}

// enrich adds file name and live totals to the newest list items with a Query each (bounded), so lists can
// show wires, amounts, and mini progress without any projection store.
func (s *Server) enrich(ctx context.Context, items []*BatchListItem) {
	var wg sync.WaitGroup
	for _, it := range items[:min(len(items), maxEnriched)] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, ok := s.queryView(ctx, it.WorkflowID); ok {
				t := v.Totals
				it.FileName, it.Totals, it.Status, it.NeedsAttention = v.FileName, &t, string(v.Phase), v.NeedsAttention
			}
		}()
	}
	wg.Wait()
}

// ParkedItem is one item waiting for bank operations.
type ParkedItem struct {
	ChunkID                  string    `json:"chunkId"`
	Code                     string    `json:"code"`
	Message                  string    `json:"message"`
	Phase                    string    `json:"phase"`
	Source                   string    `json:"source"`
	Since                    time.Time `json:"since"`
	OriginatingAccountMasked string    `json:"originatingAccountMasked"`
	AllowedDecisions         []string  `json:"allowedDecisions"`
	Step                     string    `json:"step,omitempty"`
}

type AttentionBatch struct {
	BatchID      string       `json:"batchId"`
	WorkflowID   string       `json:"workflowId"`
	CustomerID   string       `json:"customerId"`
	CustomerName string       `json:"customerName"`
	FileName     string       `json:"fileName"`
	Phase        string       `json:"phase"`
	Parked       []ParkedItem `json:"parked"`
}

// opsAttention is bank operations' exception queue: running batches with NeedsAttention=true (Visibility),
// then a Query each (bounded to 50) for the parked items themselves.
func (s *Server) opsAttention(w http.ResponseWriter, r *http.Request, c auth.Claims) {
	if !c.IsBankOps() {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "bank operations only")
		return
	}
	q := fmt.Sprintf("WorkflowType = '%s' AND FiId = '%s' AND NeedsAttention = true AND ExecutionStatus = 'Running'",
		workflow.WorkflowName, quote(c.FiID))
	items, err := s.list(r.Context(), q, 50)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ORCHESTRATION_UNAVAILABLE", "The exception queue is temporarily unavailable.")
		return
	}
	out := make([]AttentionBatch, len(items))
	var wg sync.WaitGroup
	for i, it := range items {
		out[i] = AttentionBatch{BatchID: it.BatchID, WorkflowID: it.WorkflowID, CustomerID: it.CustomerID,
			CustomerName: auth.CustomerName(c.FiID, it.CustomerID), Phase: it.Status, Parked: []ParkedItem{}}
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, ok := s.queryView(r.Context(), it.WorkflowID)
			if !ok {
				return
			}
			out[i].FileName, out[i].Phase = v.FileName, string(v.Phase)
			if a := v.BatchAttention; a != nil {
				out[i].Parked = append(out[i].Parked, ParkedItem{ChunkID: workflow.BatchTarget, Code: a.Code, Message: a.Message,
					Phase: string(a.Phase), Source: a.Source, Since: a.Since, AllowedDecisions: a.AllowedDecisions, Step: a.Step})
			}
			for _, ch := range v.Chunks {
				if a := ch.Attention; a != nil {
					out[i].Parked = append(out[i].Parked, ParkedItem{ChunkID: ch.ChunkID, Code: a.Code, Message: a.Message,
						Phase: string(a.Phase), Source: a.Source, Since: a.Since, OriginatingAccountMasked: ch.OriginatingAccountMasked,
						AllowedDecisions: a.AllowedDecisions})
				}
			}
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}

// quote escapes a value for a Visibility query string literal. Values come from signed tokens, never from
// request bodies, but escaping keeps the query well-formed regardless.
func quote(v string) string { return strings.ReplaceAll(v, "'", "\\'") }
