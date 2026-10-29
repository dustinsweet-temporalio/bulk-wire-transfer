// Tests for bulkwire-service: the Update-with-Start options (USE_EXISTING + REJECT_DUPLICATE, Update ID = key),
// error mapping, tenant and customer scoping, degraded mode, and Describe telemetry, against a fake Temporal client.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/protobuf/types/known/timestamppb"

	"bulkwire/internal/auth"
	"bulkwire/internal/claimcheck"
	"bulkwire/internal/config"
	"bulkwire/internal/samples"
	"bulkwire/internal/staging"
	"bulkwire/internal/workflow"
)

// fakeTemporal implements TemporalClient. Each test sets the outcome it needs.
type fakeTemporal struct {
	updateErr  error // returned by UpdateWithStartWorkflow / UpdateWorkflow
	getErr     error // returned by the update handle's Get (e.g. a validator rejection)
	queryErr   error
	view       workflow.BatchView
	describe   *workflowservice.DescribeWorkflowExecutionResponse
	pollers    int
	lastStart  client.StartWorkflowOptions
	lastUpdate client.UpdateWorkflowOptions
	starts     int
	listQuery  string
}

func (f *fakeTemporal) NewWithStartWorkflowOperation(o client.StartWorkflowOptions, _ any, _ ...any) client.WithStartWorkflowOperation {
	f.lastStart = o
	return nil
}
func (f *fakeTemporal) UpdateWithStartWorkflow(_ context.Context, o client.UpdateWithStartWorkflowOptions) (client.WorkflowUpdateHandle, error) {
	f.starts++
	f.lastUpdate = o.UpdateOptions
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return fakeHandle{f}, nil
}
func (f *fakeTemporal) UpdateWorkflow(_ context.Context, o client.UpdateWorkflowOptions) (client.WorkflowUpdateHandle, error) {
	f.lastUpdate = o
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return fakeHandle{f}, nil
}
func (f *fakeTemporal) QueryWorkflow(context.Context, string, string, string, ...any) (converter.EncodedValue, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	p, _ := converter.GetDefaultDataConverter().ToPayloads(f.view)
	return encoded{p}, nil
}
func (f *fakeTemporal) DescribeWorkflowExecution(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	if f.describe == nil {
		return nil, serviceerror.NewNotFound("not found")
	}
	return f.describe, nil
}
func (f *fakeTemporal) ListWorkflow(_ context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	f.listQuery = req.GetQuery()
	return &workflowservice.ListWorkflowExecutionsResponse{}, nil
}
func (f *fakeTemporal) DescribeTaskQueue(context.Context, string, enumspb.TaskQueueType) (*workflowservice.DescribeTaskQueueResponse, error) {
	resp := &workflowservice.DescribeTaskQueueResponse{}
	for i := 0; i < f.pollers; i++ {
		resp.Pollers = append(resp.Pollers, nil)
	}
	return resp, nil
}
func (f *fakeTemporal) CheckHealth(context.Context, *client.CheckHealthRequest) (*client.CheckHealthResponse, error) {
	return &client.CheckHealthResponse{}, nil
}

type fakeHandle struct{ f *fakeTemporal }

func (h fakeHandle) WorkflowID() string { return "wf" }
func (h fakeHandle) RunID() string      { return "run" }
func (h fakeHandle) UpdateID() string   { return "u" }
func (h fakeHandle) Get(_ context.Context, v any) error {
	if h.f.getErr != nil {
		return h.f.getErr
	}
	data, _ := json.Marshal(h.f.view)
	return json.Unmarshal(data, v)
}

type encoded struct{ p *commonpb.Payloads }

func (e encoded) HasValue() bool { return true }
func (e encoded) Get(v any) error {
	return converter.GetDefaultDataConverter().FromPayloads(e.p, v)
}

func searchAttrs(fields map[string]*commonpb.Payload) *commonpb.SearchAttributes {
	return &commonpb.SearchAttributes{IndexedFields: fields}
}

type activeCore struct{}

func (activeCore) ValidateAccounts(_ context.Context, _, _ string, accts []string) (map[string]staging.AccountStatus, error) {
	out := map[string]staging.AccountStatus{}
	for _, a := range accts {
		if a != samples.UnknownAccount {
			out[a] = staging.AccountStatus{Exists: true, OwnedByCustomer: true, Status: "Active"}
		}
	}
	return out, nil
}

const testKey = "test-signing-key-test-signing-key-0123456789"

func newTestServer(t *testing.T, f *fakeTemporal) (*Server, http.Handler) {
	t.Helper()
	cfg := config.Config{
		Temporal:       config.Temporal{Namespace: "bulkwire", TaskQueue: "bulkwire", UIURL: "http://localhost:8233"},
		ClaimCheck:     config.ClaimCheck{PageSize: 500},
		Defaults:       config.Defaults{ChunkSize: 250, Concurrency: 2, MaxRows: 10000, ApprovalWindow: 24 * time.Hour, RetryBudget: 30 * time.Minute, RetryMaxInterval: 20 * time.Second},
		Core:           config.Core{Endpoints: map[string]string{"lakeshore": "x", "prairie": "y"}, InternalToken: "itok"},
		AuthSigningKey: testKey, DemoPassword: "demo",
	}
	s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Temporal: f, Store: claimcheck.NewMemory(), Core: activeCore{},
		Tokens: auth.NewTokens(testKey), Cfg: cfg, Defaults: NewDraftDefaults(cfg), Now: time.Now}
	return s, s.Routes()
}

func token(t *testing.T, fi, company, user string) string {
	id, ok := auth.Lookup(fi, company, user)
	if !ok {
		t.Fatalf("no user %s", user)
	}
	tok, _ := auth.NewTokens(testKey).Mint(id)
	return tok
}

// stage uploads a generated sample as `user` and returns the batch ID.
func stage(t *testing.T, h http.Handler, tok, sample string) string {
	t.Helper()
	data, _ := samples.Generate(sample, time.Now())
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", sample+".csv")
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/batches", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var sum StagingSummary
	_ = json.Unmarshal(rec.Body.Bytes(), &sum)
	return sum.BatchID
}

func do(h http.Handler, method, path, tok string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(nil))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	var e apiError
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return fmt.Sprintf("%d %s", rec.Code, e.Code)
}

func TestDraftUsesUpdateWithStartWithTheSpecifiedOptions(t *testing.T) {
	f := &fakeTemporal{view: workflow.BatchView{Phase: workflow.PhaseDrafting}}
	_, h := newTestServer(t, f)
	jordan := token(t, "lakeshore", "SUMMIT", "jordan")
	batch := stage(t, h, jordan, "summit-payroll-5000")

	rec := do(h, http.MethodPost, "/api/batches/"+batch+"/draft", jordan, map[string]string{"Idempotency-Key": "K1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("draft: %d %s", rec.Code, rec.Body)
	}
	o := f.lastStart
	if o.ID != "bulkwire/lakeshore/"+batch || o.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING ||
		o.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE || o.Priority.FairnessKey != "lakeshore" {
		t.Fatalf("start options: %+v", o)
	}
	if f.lastUpdate.UpdateID != "K1" || f.lastUpdate.UpdateName != workflow.UpdateDraft || f.lastUpdate.WaitForStage != client.WorkflowUpdateStageCompleted {
		t.Fatalf("update options: %+v", f.lastUpdate)
	}
	if o.StaticSummary == "" || bytes.Contains([]byte(o.StaticSummary+o.StaticDetails), []byte("440122")) {
		t.Fatalf("summary must exist and contain no account numbers: %q / %q", o.StaticSummary, o.StaticDetails)
	}
	if rec := do(h, http.MethodPost, "/api/batches/"+batch+"/draft", jordan, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key: %d", rec.Code)
	}
}

func TestCommandErrorMapping(t *testing.T) {
	jordanTok := func(t *testing.T, h http.Handler) (string, string) {
		tok := token(t, "lakeshore", "SUMMIT", "jordan")
		return tok, stage(t, h, tok, "summit-payroll-5000")
	}
	cases := []struct {
		name string
		f    *fakeTemporal
		want string
	}{
		{"validator rejection", &fakeTemporal{getErr: temporal.NewApplicationError("batch is CANCELLED and can no longer be drafted", "")}, "409 REJECTED_BY_WORKFLOW"},
		{"closed batch (REJECT_DUPLICATE)", &fakeTemporal{updateErr: fmt.Errorf("failed workflow start: %w", serviceerror.NewWorkflowExecutionAlreadyStarted("closed", "", ""))}, "409 BATCH_CLOSED"},
		{"update timeout (no worker)", &fakeTemporal{updateErr: client.NewWorkflowUpdateServiceTimeoutOrCanceledError(context.DeadlineExceeded)}, "503 ORCHESTRATION_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, h := newTestServer(t, tc.f)
			tok, batch := jordanTok(t, h)
			if got := code(t, do(h, http.MethodPost, "/api/batches/"+batch+"/draft", tok, map[string]string{"Idempotency-Key": "K"})); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestInvalidBatchCannotBeDrafted(t *testing.T) {
	f := &fakeTemporal{}
	_, h := newTestServer(t, f)
	tok := token(t, "lakeshore", "SUMMIT", "jordan")
	batch := stage(t, h, tok, "summit-vendors-with-errors")
	if got := code(t, do(h, http.MethodPost, "/api/batches/"+batch+"/draft", tok, map[string]string{"Idempotency-Key": "K"})); got != "409 BATCH_INVALID" {
		t.Fatalf("got %s", got)
	}
	if f.starts != 0 {
		t.Fatal("no workflow may be started for an invalid batch")
	}
}

func TestTenantAndCustomerScope(t *testing.T) {
	_, h := newTestServer(t, &fakeTemporal{queryErr: serviceerror.NewNotFound("no workflow")})
	batch := stage(t, h, token(t, "lakeshore", "SUMMIT", "jordan"), "summit-payroll-5000")
	taylor := token(t, "lakeshore", "NORTHLINE", "taylor") // same FI, different customer
	riley := token(t, "prairie", "HARVEST", "riley")       // different FI
	for name, tok := range map[string]string{"other customer": taylor, "other FI": riley} {
		if rec := do(h, http.MethodGet, "/api/batches/"+batch, tok, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	rec := do(h, http.MethodGet, "/api/batches/"+batch, token(t, "lakeshore", "SUMMIT", "alex"), nil)
	var st BatchStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if rec.Code != 200 || st.Source != "staging" || st.Staging.ValidRowCount != 5000 {
		t.Fatalf("approver at same customer: %d %+v", rec.Code, st)
	}
}

func TestDegradedModeWhenNoWorkersAnswer(t *testing.T) {
	sa, _ := converter.GetDefaultDataConverter().ToPayload("DRAFTING")
	f := &fakeTemporal{queryErr: context.DeadlineExceeded, describe: &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING},
	}}
	cust, _ := converter.GetDefaultDataConverter().ToPayload("summit-logistics")
	f.describe.WorkflowExecutionInfo.SearchAttributes = searchAttrs(map[string]*commonpb.Payload{"BulkWireStatus": sa, "CustomerId": cust})
	_, h := newTestServer(t, f)
	tok := token(t, "lakeshore", "SUMMIT", "jordan")
	batch := stage(t, h, tok, "summit-payroll-5000")
	rec := do(h, http.MethodGet, "/api/batches/"+batch, tok, nil)
	var st BatchStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if rec.Code != 200 || st.View != nil || st.Degraded == nil || st.Degraded.Reason != "NO_WORKERS" || st.Degraded.Phase != "DRAFTING" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestDescribeMapsPendingActivitiesToChunks(t *testing.T) {
	next := time.Date(2026, 11, 3, 10, 0, 8, 0, time.UTC)
	desc := &workflowservice.DescribeWorkflowExecutionResponse{PendingActivities: []*workflowpb.PendingActivityInfo{
		{ActivityId: "draft/g02-c01/r0", Attempt: 4, State: enumspb.PENDING_ACTIVITY_STATE_SCHEDULED,
			LastFailure: &failurepb.Failure{Message: "CORE_UNAVAILABLE: Core is unavailable (HTTP 503)"}, NextAttemptScheduleTime: timestamppb.New(next)},
		{ActivityId: "authorize/g00-c00/r1", Attempt: 1, State: enumspb.PENDING_ACTIVITY_STATE_STARTED}, // healthy: omitted
		{ActivityId: "policy/g01/r0", Attempt: 2, State: enumspb.PENDING_ACTIVITY_STATE_STARTED},
		{ActivityId: "reject/g03-c00/r2", Attempt: 3, State: enumspb.PENDING_ACTIVITY_STATE_SCHEDULED},
	}}
	got := pendingActivities(desc)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if g := got[0]; g.ChunkID != "g02-c01" || g.Phase != "draft" || g.Attempt != 4 || g.State != "SCHEDULED" ||
		g.LastFailure == "" || !g.NextAttemptAt.Equal(next) {
		t.Fatalf("draft activity: %+v", g)
	}
	if g := got[1]; g.ChunkID != "" || g.Phase != "policy" {
		t.Fatalf("batch step: %+v", g)
	}
	if g := got[2]; g.ChunkID != "g03-c00" || g.Phase != "reject" {
		t.Fatalf("reject: %+v", g)
	}
}

func TestDuplicateApprovalCallbackAfterCompletionIsSuccess(t *testing.T) {
	f := &fakeTemporal{updateErr: fmt.Errorf("failed workflow start: %w", serviceerror.NewWorkflowExecutionAlreadyStarted("closed", "", "")),
		view: workflow.BatchView{Phase: workflow.PhaseAuthorized, Authorization: &workflow.AuthorizeRequest{ApprovalRef: "APR-X"}}}
	s, h := newTestServer(t, f)
	batch := stage(t, h, token(t, "lakeshore", "SUMMIT", "jordan"), "summit-payroll-5000")
	body := fmt.Sprintf(`{"fiId":"lakeshore","batchId":%q,"approvalRef":"APR-X"}`, batch)
	req := httptest.NewRequest(http.MethodPost, "/internal/approvals", bytes.NewBufferString(body))
	req.Header.Set("X-Internal-Token", s.Cfg.Core.InternalToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte("ALREADY_APPLIED")) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	req = httptest.NewRequest(http.MethodPost, "/internal/approvals", bytes.NewBufferString(body))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("internal route without token: %d", rec.Code)
	}
}

func TestDraftDefaultsValidation(t *testing.T) {
	s, h := newTestServer(t, &fakeTemporal{})
	put := func(body string) int {
		req := httptest.NewRequest(http.MethodPut, "/internal/draft-defaults", bytes.NewBufferString(body))
		req.Header.Set("X-Internal-Token", s.Cfg.Core.InternalToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := put(`{"lakeshore":{"chunkSize":250,"concurrency":4,"approvalWindow":"2m","retryBudget":"60s","retryMaxInterval":"5s"}}`); c != 200 {
		t.Fatalf("valid put: %d", c)
	}
	if d, _ := s.Defaults.get("lakeshore"); d.concurrency != 4 || d.approvalWindow != 2*time.Minute {
		t.Fatalf("not applied: %+v", d)
	}
	if c := put(`{"lakeshore":{"chunkSize":10,"concurrency":4,"approvalWindow":"2m","retryBudget":"60s","retryMaxInterval":"5s"}}`); c != 400 {
		t.Fatalf("invalid chunk size accepted: %d", c)
	}
}

func TestLoginRejectsOtherFIsUsers(t *testing.T) {
	_, h := newTestServer(t, &fakeTemporal{})
	login := func(body string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body)))
		return rec.Code
	}
	if c := login(`{"fiId":"lakeshore","companyId":"summit","userId":"jordan","password":"demo"}`); c != 200 {
		t.Fatalf("jordan: %d", c)
	}
	if c := login(`{"fiId":"prairie","companyId":"SUMMIT","userId":"jordan","password":"demo"}`); c != 401 {
		t.Fatalf("jordan at prairie: %d", c)
	}
	if c := login(`{"fiId":"lakeshore","companyId":"SUMMIT","userId":"jordan","password":"nope"}`); c != 401 {
		t.Fatalf("wrong password: %d", c)
	}
}

func TestApprovalsQueueListsOnlyRunningBatches(t *testing.T) {
	f := &fakeTemporal{}
	_, h := newTestServer(t, f)
	if rec := do(h, http.MethodGet, "/api/batches?scope=approvals", token(t, "lakeshore", "SUMMIT", "alex"), nil); rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	want := "WorkflowType = 'BulkWireBatch' AND FiId = 'lakeshore' AND CustomerId = 'summit-logistics' AND BulkWireStatus = 'DRAFTED' AND ExecutionStatus = 'Running'"
	if f.listQuery != want {
		t.Fatalf("query:\n got %s\nwant %s", f.listQuery, want)
	}
}
