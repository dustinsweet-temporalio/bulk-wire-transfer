// Activity options: ONE builder used by every Activity in this workflow (claim-check reads included). There
// are deliberately no per-Activity retry policy variants.
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

// coreActivityOptions:
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
func coreActivityOptions(st settings, activityID, summary string) workflow.ActivityOptions {
	return workflow.ActivityOptions{
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
}
