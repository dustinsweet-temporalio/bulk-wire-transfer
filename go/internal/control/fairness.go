// DEMO-ONLY: the per-FI dispatch rate limit on the `bulkwire` ACTIVITY task queue.
//
// PATTERN: Fairness by Tenant — https://docs.temporal.io/develop/task-queue-priority-fairness
// PATTERN: Per-Tenant Rate Limit — https://docs.temporal.io/develop/task-queue-priority-fairness#set-rate-limits-at-the-task-queue-level
// Every batch's Activities carry fairness key = its FI. A fairness-key rate limit caps how many Activity tasks per
// second the Temporal Service DISPATCHES for each FI, across all of that FI's batches and all workers. It caps
// RATE, not concurrency: with per-call latency L, steady-state in-flight Core calls ≈ rate × L. Hard isolation would
// be a per-FI Activity task queue with dedicated worker capacity (see docs/ARCHITECTURE.md, "Tenancy").
// Set here with the WorkflowService UpdateTaskQueueConfig RPC from the Go client.
// On Temporal Cloud, Fairness is a paid namespace feature and single-partition queues need a support request.
package control

import (
	"net/http"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
)

type fairness struct {
	Enabled   bool    `json:"enabled"`
	RPSPerFI  float32 `json:"rpsPerFi"`
	Reason    string  `json:"reason,omitempty"`
	QueueRPS  float32 `json:"queueRps,omitempty"`
	TaskQueue string  `json:"taskQueue"`
}

func (s *Service) getFairness(w http.ResponseWriter, r *http.Request) {
	f, err := s.readFairness(r)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Service) readFairness(r *http.Request) (fairness, error) {
	ctx, cancel := timeout(r.Context(), 5*time.Second)
	defer cancel()
	resp, err := s.Temporal.WorkflowService().DescribeTaskQueue(ctx, &workflowservice.DescribeTaskQueueRequest{
		Namespace: s.Cfg.Temporal.Namespace, TaskQueue: &taskqueuepb.TaskQueue{Name: s.Cfg.Temporal.TaskQueue},
		TaskQueueType: enumspb.TASK_QUEUE_TYPE_ACTIVITY, ReportConfig: true,
	})
	if err != nil {
		return fairness{}, err
	}
	f := fairness{TaskQueue: s.Cfg.Temporal.TaskQueue}
	if c := resp.GetConfig(); c != nil {
		if d := c.GetFairnessKeysRateLimitDefault(); d.GetRateLimit() != nil {
			f.Enabled, f.RPSPerFI, f.Reason = true, d.GetRateLimit().GetRequestsPerSecond(), d.GetMetadata().GetReason()
		}
		f.QueueRPS = c.GetQueueRateLimit().GetRateLimit().GetRequestsPerSecond()
	}
	return f, nil
}

// putFairness enables ({enabled:true, rpsPerFi:3}) or removes ({enabled:false}) the per-FI limit.
func (s *Service) putFairness(w http.ResponseWriter, r *http.Request) {
	var body fairness
	if err := decode(r, &body); err != nil || (body.Enabled && body.RPSPerFI <= 0) {
		writeError(w, http.StatusBadRequest, "send {enabled, rpsPerFi > 0}")
		return
	}
	if err := s.setFairness(r, body.Enabled, body.RPSPerFI); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.getFairness(w, r)
}

func (s *Service) setFairness(r *http.Request, enabled bool, rps float32) error {
	update := &workflowservice.UpdateTaskQueueConfigRequest_RateLimitUpdate{} // nil RateLimit = unset
	if enabled {
		update = &workflowservice.UpdateTaskQueueConfigRequest_RateLimitUpdate{
			RateLimit: &taskqueuepb.RateLimit{RequestsPerSecond: rps},
			Reason:    "Core protection (Control Center)",
		}
	}
	ctx, cancel := timeout(r.Context(), 5*time.Second)
	defer cancel()
	_, err := s.Temporal.WorkflowService().UpdateTaskQueueConfig(ctx, &workflowservice.UpdateTaskQueueConfigRequest{
		Namespace: s.Cfg.Temporal.Namespace, Identity: "control-service", TaskQueue: s.Cfg.Temporal.TaskQueue,
		TaskQueueType: enumspb.TASK_QUEUE_TYPE_ACTIVITY, UpdateFairnessKeyRateLimitDefault: update,
	})
	if err == nil {
		s.Log.Info("DEMO-ONLY fairness rate limit", "enabled", enabled, "rpsPerFi", rps)
	}
	return err
}
