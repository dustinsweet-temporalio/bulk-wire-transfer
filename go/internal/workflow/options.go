// Activity options: one set of timeouts and one retry policy for every Activity in this workflow, and two
// routes. Core-calling Activities go to the batch's Core task queue with a fairness key; the claim-check-only
// Activity (PlanChunks) stays on the workflow's own shared queue. There are deliberately no per-Activity retry
// policy variants.
package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// StartToClose is one attempt's limit.
// WHY 60 s: just above Core's 30 s SQL command timeout, so a slow statement fails inside Core first (a real
// 504 the Activity retries), and a lost or hung attempt (Core frozen, response never sent) is detected
// within a minute instead of blocking the chunk.
const StartToClose = 60 * time.Second

// route says which task queue an Activity runs on.
type route int

const (
	// onSharedQueue: the workflow's own task queue ("bulkwire"). For Activities that never call Core.
	onSharedQueue route = iota
	// onCoreQueue: the batch's Core task queue (BatchInput.CoreTaskQueue), for every Activity that calls Core.
	onCoreQueue
)

// activityOptions:
//
// PATTERN: Retry Policy (no attempt cap) — https://docs.temporal.io/encyclopedia/retry-policies
// PATTERN: Fixed Wall-Time Retries — https://docs.temporal.io/design-patterns/fixed-wall-time-retries
// Transient Core failures retry IN PLACE with exponential backoff (1 s, 2 s, 4 s, ... capped at
// MaximumInterval) for as long as the retry budget allows. There is NO MaximumAttempts: an outage is
// measured in time, not in attempts, so the budget is expressed as wall time (ScheduleToClose). When the
// budget runs out, the Activity fails with a ScheduleToClose timeout and the workflow parks the chunk
// (park.go); nothing ever leaves Temporal.
//
// Non-retryable failures are signalled by the Activity itself (temporal.NewNonRetryableApplicationError) and
// come back to the workflow on the first attempt.
//
// No HeartbeatTimeout: every call is a sub-minute, set-based Core operation (see internal/activities).
func activityOptions(st settings, r route, activityID, summary string) workflow.ActivityOptions {
	o := workflow.ActivityOptions{
		ActivityID:             activityID,
		Summary:                summary, // shown in the Web UI (encrypted payload, masked data only)
		StartToCloseTimeout:    StartToClose,
		ScheduleToCloseTimeout: st.RetryBudget, // total retry budget; exhaustion parks the chunk
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    st.RetryMaxInterval,
			// NO MaximumAttempts: transient Core failures retry until success or budget exhaustion.
		},
	}
	if r == onCoreQueue {
		// PATTERN: Task Queue per Downstream (internal/routing). This FI's Core queue has its own server-side
		// rate limit and its own worker capacity, so a slow Core backs up only this FI's queue.
		// DETERMINISM: both values come from the workflow input, fixed at start.
		o.TaskQueue = st.CoreTaskQueue
		// PATTERN: Fairness — https://docs.temporal.io/develop/task-queue-priority-fairness
		// Set on every Core Activity explicitly (the workflow start sets no Priority, so nothing is inherited).
		// When the queue backs up, the Temporal Service dispatches round-robin across fairness keys: across this
		// FI's batches on a single-FI queue, across FIs on a shared one.
		o.Priority = temporal.Priority{FairnessKey: st.CoreFairnessKey}
	}
	return o
}
