// DEMO-ONLY: set or clear the rate limit of one FI's Core Activity task queue without a deployment.
//
// PATTERN: Task Queue per Downstream (internal/routing), PATTERN: Task Queue Rate Limit —
// https://docs.temporal.io/develop/task-queue-priority-fairness#set-rate-limits-at-the-task-queue-level
// Each Core queue's workers set TaskQueueActivitiesPerSecond from config/routing.yaml. An operator can override it
// server-side, with the same UpdateTaskQueueConfig RPC that `temporal task-queue config set --queue-rps-limit` (and
// `make ratelimit`) calls. The override wins over the workers' value until it is cleared; clearing it returns the
// queue to the workers' value. A rate limit caps how fast Activities START, not how many run at once: with
// per-call latency L, steady-state in-flight Core calls are about rate x L, and the number in flight is bounded by
// each batch's semaphore and each worker's MaxConcurrentActivityExecutionSize.
package control

import (
	"context"
	"fmt"
	"net/http"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
)

// TaskQueueConfigAPI is the part of the WorkflowService this file uses (client.WorkflowService() satisfies it;
// tests pass a fake).
type TaskQueueConfigAPI interface {
	DescribeTaskQueue(ctx context.Context, in *workflowservice.DescribeTaskQueueRequest, opts ...grpc.CallOption) (*workflowservice.DescribeTaskQueueResponse, error)
	UpdateTaskQueueConfig(ctx context.Context, in *workflowservice.UpdateTaskQueueConfigRequest, opts ...grpc.CallOption) (*workflowservice.UpdateTaskQueueConfigResponse, error)
}

// coreQueue is one Core queue as the Control Center shows it.
type coreQueue struct {
	TaskQueue               string   `json:"taskQueue"`
	FIs                     []string `json:"fis"`
	ConfiguredRPS           float64  `json:"configuredRps"`            // routing.yaml: the workers' TaskQueueActivitiesPerSecond
	MaxConcurrentActivities int      `json:"maxConcurrentActivities"`  // routing.yaml: per worker
	OverrideRPS             *float32 `json:"overrideRps,omitempty"`    // set through UpdateTaskQueueConfig; nil = none
	OverrideReason          string   `json:"overrideReason,omitempty"` //
	EffectiveRPS            float32  `json:"effectiveRps"`             // what the Temporal Service enforces now
	EffectiveSource         string   `json:"effectiveSource"`          // API (override) | WORKER | SYSTEM
	Pollers                 []string `json:"pollers"`                  // worker identities polling this queue
	Error                   string   `json:"error,omitempty"`
}

func (s *Service) coreQueues(ctx context.Context) []coreQueue {
	var out []coreQueue
	for _, q := range s.Routing.Queues() {
		cq := coreQueue{TaskQueue: q.Name, FIs: q.FIs, ConfiguredRPS: q.ActivitiesPerSecond, MaxConcurrentActivities: q.MaxConcurrentActivities, Pollers: []string{}}
		resp, err := s.TaskQueues.DescribeTaskQueue(ctx, &workflowservice.DescribeTaskQueueRequest{
			Namespace: s.Cfg.Temporal.Namespace, TaskQueue: &taskqueuepb.TaskQueue{Name: q.Name},
			TaskQueueType: enumspb.TASK_QUEUE_TYPE_ACTIVITY, ReportConfig: true,
		})
		if err != nil {
			cq.Error = err.Error()
			out = append(out, cq)
			continue
		}
		if l := resp.GetConfig().GetQueueRateLimit(); l.GetRateLimit() != nil {
			rps := l.GetRateLimit().GetRequestsPerSecond()
			cq.OverrideRPS, cq.OverrideReason = &rps, l.GetMetadata().GetReason()
		}
		if e := resp.GetEffectiveRateLimit(); e != nil {
			cq.EffectiveRPS = e.GetRequestsPerSecond()
			cq.EffectiveSource = map[enumspb.RateLimitSource]string{enumspb.RATE_LIMIT_SOURCE_API: "API",
				enumspb.RATE_LIMIT_SOURCE_WORKER: "WORKER", enumspb.RATE_LIMIT_SOURCE_SYSTEM: "SYSTEM"}[e.GetRateLimitSource()]
		}
		for _, p := range resp.GetPollers() {
			cq.Pollers = append(cq.Pollers, p.GetIdentity())
		}
		out = append(out, cq)
	}
	return out
}

func (s *Service) getRateLimits(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := timeout(r.Context(), 5*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.coreQueues(ctx))
}

// putRateLimit sets ({rps: 2}) or clears ({rps: null}) the override on the FI's Core queue.
func (s *Service) putRateLimit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RPS *float32 `json:"rps"`
	}
	if err := decode(r, &body); err != nil || (body.RPS != nil && *body.RPS <= 0) {
		writeError(w, http.StatusBadRequest, "send {rps: number > 0} to set, or {rps: null} to clear")
		return
	}
	q, ok := s.Routing.QueueFor(r.PathValue("fiId"))
	if !ok {
		writeError(w, http.StatusNotFound, "no Core queue for FI "+r.PathValue("fiId"))
		return
	}
	ctx, cancel := timeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.setQueueRateLimit(ctx, q.Name, body.RPS); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.coreQueues(ctx))
}

// setQueueRateLimit overrides a queue's rate limit (rps != nil) or clears the override (rps == nil), which returns
// the queue to the rate its workers set from routing.yaml.
func (s *Service) setQueueRateLimit(ctx context.Context, queue string, rps *float32) error {
	update := &workflowservice.UpdateTaskQueueConfigRequest_RateLimitUpdate{} // nil RateLimit = unset the override
	if rps != nil {
		update = &workflowservice.UpdateTaskQueueConfigRequest_RateLimitUpdate{
			RateLimit: &taskqueuepb.RateLimit{RequestsPerSecond: *rps},
			Reason:    "Core protection (Control Center)",
		}
	}
	_, err := s.TaskQueues.UpdateTaskQueueConfig(ctx, &workflowservice.UpdateTaskQueueConfigRequest{
		Namespace: s.Cfg.Temporal.Namespace, Identity: "control-service", TaskQueue: queue,
		TaskQueueType: enumspb.TASK_QUEUE_TYPE_ACTIVITY, UpdateQueueRateLimit: update,
	})
	if err == nil {
		s.Log.Info("DEMO-ONLY Core queue rate limit", "taskQueue", queue, "override", rps)
	}
	return err
}

// clearRateLimits removes every Core queue's override (Control Center reset).
func (s *Service) clearRateLimits(ctx context.Context, _ *http.Request) (string, error) {
	for _, q := range s.Routing.Queues() {
		if err := s.setQueueRateLimit(ctx, q.Name, nil); err != nil {
			return "", fmt.Errorf("%s: %w", q.Name, err)
		}
	}
	return "overrides cleared; each queue uses its workers' rate from routing.yaml", nil
}
