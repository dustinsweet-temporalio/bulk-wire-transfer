// DEMO-ONLY: every BulkWireBatch across FIs (Visibility), the fastest path from the Control Center to a batch's
// full story in the Temporal Web UI. Running batches are enriched with Describe: history length and size (catalog
// F43), and pending Activities with their attempt counts.
package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"

	"bulkwire/internal/temporalx"
)

type batchRow struct {
	WorkflowID        string     `json:"workflowId"`
	BatchID           string     `json:"batchId"`
	FiID              string     `json:"fiId"`
	CustomerID        string     `json:"customerId"`
	InitiatedBy       string     `json:"initiatedBy"`
	Status            string     `json:"status"`
	NeedsAttention    bool       `json:"needsAttention"`
	ExecutionStatus   string     `json:"executionStatus"`
	DeploymentVersion string     `json:"deploymentVersion"`
	StartTime         *time.Time `json:"startTime"`
	CloseTime         *time.Time `json:"closeTime"`
	HistoryLength     int64      `json:"historyLength"`
	HistorySizeBytes  int64      `json:"historySizeBytes"`
	PendingRetries    []string   `json:"pendingRetries"` // "draft/g02-c01/r0 · attempt 4"
	TemporalUIURL     string     `json:"temporalUiUrl"`
	PortalURL         string     `json:"portalUrl"`
}

var portalPorts = map[string]string{"lakeshore": "3001", "prairie": "3002"}

func (s *Service) batches(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	base := fmt.Sprintf("WorkflowType = '%s'", temporalx.WorkflowType)
	if fi := q.Get("fi"); fi != "" {
		base += fmt.Sprintf(" AND FiId = '%s'", strings.ReplaceAll(fi, "'", ""))
	}
	if st := q.Get("status"); st != "" {
		base += fmt.Sprintf(" AND BulkWireStatus = '%s'", strings.ReplaceAll(st, "'", ""))
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	running, err := s.list(r.Context(), base+" AND ExecutionStatus = 'Running'", 200)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	closed, err := s.list(r.Context(), base+" AND ExecutionStatus != 'Running'", int32(limit))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	var wg sync.WaitGroup
	for _, row := range running {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.describeInto(r.Context(), row)
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, append(running, closed...))
}

func (s *Service) list(ctx context.Context, query string, size int32) ([]*batchRow, error) {
	resp, err := s.Temporal.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		Namespace: s.Cfg.Temporal.Namespace, PageSize: size, Query: query,
	})
	if err != nil {
		return nil, err
	}
	rows := []*batchRow{}
	for _, e := range resp.GetExecutions() {
		sa := e.GetSearchAttributes()
		id := e.GetExecution().GetWorkflowId()
		row := &batchRow{WorkflowID: id, BatchID: id[strings.LastIndex(id, "/")+1:],
			FiID: saString(sa, "FiId"), CustomerID: saString(sa, "CustomerId"), InitiatedBy: saString(sa, "InitiatedBy"),
			Status: saString(sa, "BulkWireStatus"), DeploymentVersion: saString(sa, temporalx.SAWorkerDeploymentVersion),
			ExecutionStatus: strings.TrimPrefix(enumspb.WorkflowExecutionStatus_name[int32(e.GetStatus())], "WORKFLOW_EXECUTION_STATUS_"),
			HistoryLength:   e.GetHistoryLength(), HistorySizeBytes: e.GetHistorySizeBytes(),
			TemporalUIURL:  fmt.Sprintf("%s/namespaces/%s/workflows/%s", s.Cfg.Temporal.UIURL, s.Cfg.Temporal.Namespace, url.PathEscape(id)),
			PendingRetries: []string{}}
		_ = converter.GetDefaultDataConverter().FromPayload(sa.GetIndexedFields()["NeedsAttention"], &row.NeedsAttention)
		if port := portalPorts[row.FiID]; port != "" {
			row.PortalURL = fmt.Sprintf("http://localhost:%s/payments/bulk-wire/%s", port, row.BatchID)
		}
		if t := e.GetStartTime(); t != nil {
			v := t.AsTime()
			row.StartTime = &v
		}
		if t := e.GetCloseTime(); t != nil {
			v := t.AsTime()
			row.CloseTime = &v
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].StartTime != nil && rows[j].StartTime != nil && rows[i].StartTime.After(*rows[j].StartTime)
	})
	return rows, nil
}

func (s *Service) describeInto(ctx context.Context, row *batchRow) {
	ctx, cancel := timeout(ctx, 3*time.Second)
	defer cancel()
	desc, err := s.Temporal.DescribeWorkflowExecution(ctx, row.WorkflowID, "")
	if err != nil {
		return
	}
	info := desc.GetWorkflowExecutionInfo()
	row.HistoryLength, row.HistorySizeBytes = info.GetHistoryLength(), info.GetHistorySizeBytes()
	for _, p := range desc.GetPendingActivities() {
		if p.GetAttempt() > 1 {
			row.PendingRetries = append(row.PendingRetries, fmt.Sprintf("%s · attempt %d", p.GetActivityId(), p.GetAttempt()))
		}
	}
}

// saString decodes a Keyword Search Attribute (never encrypted) with the default converter.
func saString(sa *commonpb.SearchAttributes, name string) string {
	var v string
	if p, ok := sa.GetIndexedFields()[name]; ok {
		_ = converter.GetDefaultDataConverter().FromPayload(p, &v)
	}
	return v
}
