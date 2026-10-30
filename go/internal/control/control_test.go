// Tests for control-service (DEMO-ONLY tooling): the container allowlist is enforced before any Docker call,
// reset steps run in order, and retry-storm, history-inspector, and metrics parsing behave as the Control Center expects.

package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"

	"bulkwire/internal/routing"
)

func TestAllowlistIsEnforcedBeforeDocker(t *testing.T) {
	d := NewDocker("/nonexistent.sock", "bulkwire", []string{"worker-a", "core-lakeshore"})
	for _, svc := range []string{"gateway", "control-service", "sql-prairie"} {
		if err := d.Act(context.Background(), svc, "kill"); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s: got %v, want ErrNotAllowed", svc, err)
		}
	}
	if err := d.Act(context.Background(), "worker-a", "rm"); err == nil || errors.Is(err, ErrNotAllowed) {
		t.Errorf("unknown action must be refused: %v", err)
	}
}

func TestContainerActionMapsNotAllowedTo403(t *testing.T) {
	s := &Service{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Docker: NewDocker("/x", "p", []string{"worker-a"})}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/containers/gateway/kill", nil)
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestResetStepOrder(t *testing.T) {
	var names []string
	for _, st := range (&Service{}).resetSteps() {
		names = append(names, st.Name)
	}
	want := []string{
		"unpause paused containers",
		"start stopped essential services",
		"stop worker-v2 and worker-v3",
		"set current version v1",
		"terminate running batches",
		"reset Core data, faults, latency, stats",
		"clear Core queue rate-limit overrides",
		"reset draft defaults",
	}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("reset order:\n%v\nwant\n%v", names, want)
	}
}

func TestRetryStormAggregation(t *testing.T) {
	statuses := []int{202, 202, 409, 202, 503, 0}
	errs := []error{nil, nil, nil, nil, nil, fmt.Errorf("connection reset")}
	res := aggregateStorm("bulkwire/lakeshore/B", "B", statuses, errs)
	if res.Requests != 6 || res.StatusCounts[202] != 3 || res.StatusCounts[409] != 1 || res.StatusCounts[503] != 1 || len(res.Errors) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestInspectorPicksWorkflowInputAndFirstActivityInput(t *testing.T) {
	payload := func(s string) *commonpb.Payloads {
		return &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: []byte(s)}}}
	}
	events := []*historypb.HistoryEvent{
		{Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{Input: payload("wf-input")}}},
		{Attributes: &historypb.HistoryEvent_WorkflowTaskScheduledEventAttributes{WorkflowTaskScheduledEventAttributes: &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskqueuepb.TaskQueue{Name: "bulkwire"}}}},
		{Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{ActivityId: "plan/r0", ActivityType: &commonpb.ActivityType{Name: "PlanChunks"}, Input: payload("first-activity")}}},
		{Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{ActivityId: "gentxbatch/r0", ActivityType: &commonpb.ActivityType{Name: "EnsureGenTxBatch"}, Input: payload("second-activity")}}},
	}
	got := pickPayloads(events)
	if len(got) != 2 || string(got[0].payload.Data) != "wf-input" || string(got[1].payload.Data) != "first-activity" ||
		!strings.Contains(got[1].event, "PlanChunks") {
		t.Fatalf("%+v", got)
	}
}

func TestParseMetrics(t *testing.T) {
	text := `# HELP bulkwire_core_attempts_over_threshold x
bulkwire_core_attempts_over_threshold{activity="InsertDraftChunk",fi="lakeshore"} 7
bulkwire_core_attempts_over_threshold_total{activity="AuthorizeChunk",fi="prairie"} 2
temporal_activity_execution_failed{activity_type="InsertDraftChunk",namespace="bulkwire"} 3
temporal_activity_execution_failed{activity_type="EnsureGenTxBatch",namespace="bulkwire"} 1
`
	alerts, totals := parseMetrics("worker-a", strings.NewReader(text))
	if len(alerts) != 2 || alerts[0].Value != 7 || alerts[0].FiID != "lakeshore" || alerts[1].Activity != "AuthorizeChunk" {
		t.Fatalf("alerts %+v", alerts)
	}
	if totals["temporal_activity_execution_failed"] != 4 {
		t.Fatalf("totals %+v", totals)
	}
}

// fakeTaskQueues keeps one API override per queue, as the Temporal Service does, and reports the effective
// limit: the override when one is set, otherwise the workers' TaskQueueActivitiesPerSecond.
type fakeTaskQueues struct {
	workerRPS map[string]float32
	override  map[string]*taskqueuepb.RateLimitConfig
	updates   []*workflowservice.UpdateTaskQueueConfigRequest
}

func (f *fakeTaskQueues) DescribeTaskQueue(_ context.Context, in *workflowservice.DescribeTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.DescribeTaskQueueResponse, error) {
	q := in.GetTaskQueue().GetName()
	eff := &workflowservice.DescribeTaskQueueResponse_EffectiveRateLimit{RequestsPerSecond: f.workerRPS[q], RateLimitSource: enumspb.RATE_LIMIT_SOURCE_WORKER}
	cfg := &taskqueuepb.TaskQueueConfig{}
	if o := f.override[q]; o != nil {
		cfg.QueueRateLimit = o
		eff = &workflowservice.DescribeTaskQueueResponse_EffectiveRateLimit{RequestsPerSecond: o.GetRateLimit().GetRequestsPerSecond(), RateLimitSource: enumspb.RATE_LIMIT_SOURCE_API}
	}
	return &workflowservice.DescribeTaskQueueResponse{Config: cfg, EffectiveRateLimit: eff}, nil
}

func (f *fakeTaskQueues) UpdateTaskQueueConfig(_ context.Context, in *workflowservice.UpdateTaskQueueConfigRequest, _ ...grpc.CallOption) (*workflowservice.UpdateTaskQueueConfigResponse, error) {
	f.updates = append(f.updates, in)
	if in.GetTaskQueueType() != enumspb.TASK_QUEUE_TYPE_ACTIVITY || in.GetUpdateFairnessKeyRateLimitDefault() != nil {
		return nil, fmt.Errorf("unexpected update %v", in)
	}
	if u := in.GetUpdateQueueRateLimit(); u.GetRateLimit() == nil {
		delete(f.override, in.GetTaskQueue())
	} else {
		f.override[in.GetTaskQueue()] = &taskqueuepb.RateLimitConfig{RateLimit: u.GetRateLimit(), Metadata: &taskqueuepb.ConfigMetadata{Reason: u.GetReason()}}
	}
	return &workflowservice.UpdateTaskQueueConfigResponse{}, nil
}

// Setting one FI's Core queue rate limit overrides only that queue; Control Center reset clears every override, so
// each queue returns to its workers' TaskQueueActivitiesPerSecond from routing.yaml. Status shows both values.
func TestCoreQueueRateLimitSetAndReset(t *testing.T) {
	routes, err := routing.Load("../../../config/routing.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tq := &fakeTaskQueues{workerRPS: map[string]float32{"bulkwire-core-lakeshore": 10, "bulkwire-core-prairie": 10}, override: map[string]*taskqueuepb.RateLimitConfig{}}
	s := &Service{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Routing: routes, TaskQueues: tq}
	s.Cfg.Temporal.Namespace = "bulkwire"
	h := s.Routes()
	call := func(method, path, body string) []coreQueue {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
		}
		var out []coreQueue
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	byQueue := func(qs []coreQueue) map[string]coreQueue {
		m := map[string]coreQueue{}
		for _, q := range qs {
			m[q.TaskQueue] = q
		}
		return m
	}

	got := byQueue(call(http.MethodPut, "/api/ratelimits/lakeshore", `{"rps":2}`))
	l, p := got["bulkwire-core-lakeshore"], got["bulkwire-core-prairie"]
	if l.ConfiguredRPS != 10 || l.OverrideRPS == nil || *l.OverrideRPS != 2 || l.EffectiveRPS != 2 || l.EffectiveSource != "API" {
		t.Fatalf("lakeshore after set: %+v", l)
	}
	if p.OverrideRPS != nil || p.EffectiveRPS != 10 || p.EffectiveSource != "WORKER" {
		t.Fatalf("prairie must be untouched: %+v", p)
	}
	if u := tq.updates[0]; u.GetTaskQueue() != "bulkwire-core-lakeshore" || u.GetNamespace() != "bulkwire" || u.GetUpdateQueueRateLimit().GetRateLimit().GetRequestsPerSecond() != 2 {
		t.Fatalf("update request: %v", u)
	}

	if _, err := s.clearRateLimits(context.Background(), nil); err != nil { // the Control Center reset step
		t.Fatal(err)
	}
	got = byQueue(call(http.MethodGet, "/api/ratelimits", ""))
	if l := got["bulkwire-core-lakeshore"]; l.OverrideRPS != nil || l.EffectiveRPS != 10 || l.EffectiveSource != "WORKER" {
		t.Fatalf("reset must return lakeshore to the workers' rate: %+v", l)
	}

	call(http.MethodPut, "/api/ratelimits/prairie", `{"rps":3}`)
	if l := byQueue(call(http.MethodPut, "/api/ratelimits/prairie", `{"rps":null}`))["bulkwire-core-prairie"]; l.OverrideRPS != nil {
		t.Fatalf("{rps:null} must clear the override: %+v", l)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/ratelimits/lakeshore", strings.NewReader(`{"rps":0}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rps 0 must be refused (clear with null): %d", rec.Code)
	}
}
