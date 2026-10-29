// Tests for control-service (DEMO-ONLY tooling): the container allowlist is enforced before any Docker call,
// reset steps run in order, and retry-storm, history-inspector, and metrics parsing behave as the Control Center expects.

package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
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
		"remove fairness rate limit",
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
