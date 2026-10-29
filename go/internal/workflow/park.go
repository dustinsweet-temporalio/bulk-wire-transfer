// Parking: what happens when retrying cannot help.
//
// PATTERN: Resumable Activity — https://docs.temporal.io/design-patterns/resumable-activity
// A structural failure (account on hold, batch closed, contract defect, staged data gone) or an exhausted
// retry budget does not fail the workflow and does not go to a dead-letter queue. The affected chunk (or
// batch-level step) PARKS inside the workflow: its state becomes NEEDS_ATTENTION, the NeedsAttention Search
// Attribute flips true so it shows in bank operations' exception queue, and the workflow waits durably for
// an operator's `resolve` Update (RETRY | REJECT_CHUNK | CANCEL_BATCH). Other chunks keep going. When the
// human fixes the cause (e.g., releases the hold) and chooses RETRY, the same chunk runs again.
package workflow

import (
	"errors"
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"bulkwire/internal/model"
)

type parkReason struct {
	Code    string
	Message string
	Source  string // Core | CLAIM_CHECK | RETRY_BUDGET
}

// shouldPark inspects the *temporal.ActivityError returned by Future.Get (via errors.As on its cause
// chain) and reports whether the failure needs a human:
//  1. an exhausted retry budget (PATTERN: Fixed Wall-Time Retries): a ScheduleToClose TimeoutError, or an
//     ActivityError with RetryState TIMEOUT. Checked first, because the cause is the last transient failure,
//     which by itself would not park; or
//  2. a NON-retryable ApplicationError (CoreStructuralError, CoreContractError, ClaimCheckNotFound,
//     StagingInvalid, or any other type an Activity marks non-retryable).
//
// Anything else (e.g., the workflow itself being cancelled) is not parked.
func (s *batchState) shouldPark(err error) (parkReason, bool) {
	// The budget can run out two ways: the ScheduleToClose timer fires (a TimeoutError of that type, whose
	// cause is the last failure), or a retry would start after the deadline, so the service stops retrying
	// and returns the last failure with RetryState TIMEOUT. Both mean "retry budget exhausted".
	var timeoutErr *temporal.TimeoutError
	var actErr *temporal.ActivityError
	switch {
	case errors.As(err, &timeoutErr) && timeoutErr.TimeoutType() == enumspb.TIMEOUT_TYPE_SCHEDULE_TO_CLOSE:
		return s.budgetExhausted(errors.Unwrap(timeoutErr)), true
	case errors.As(err, &actErr) && actErr.RetryState() == enumspb.RETRY_STATE_TIMEOUT:
		return s.budgetExhausted(errors.Unwrap(actErr)), true
	}

	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		return parkReason{}, false
	}
	var d model.CoreErrorDetails
	if appErr.HasDetails() {
		_ = appErr.Details(&d)
	}
	switch appErr.Type() {
	case model.ErrTypeCoreStructural:
		return parkReason{Code: d.Code, Message: d.Message, Source: "Core"}, true
	case model.ErrTypeCoreContract:
		return parkReason{Code: "CORE_CONTRACT", Message: fmt.Sprintf("%s: %s", d.Code, d.Message), Source: "Core"}, true
	case model.ErrTypeClaimCheckNotFound:
		return parkReason{Code: "CLAIM_CHECK_NOT_FOUND", Message: "staged rows are no longer available (expired or never staged); cancel and re-upload", Source: "CLAIM_CHECK"}, true
	case model.ErrTypeStagingInvalid:
		return parkReason{Code: "STAGING_INVALID", Message: appErr.Message(), Source: "CLAIM_CHECK"}, true
	default:
		return parkReason{Code: appErr.Type(), Message: appErr.Message(), Source: "Core"}, true
	}
}

func (s *batchState) budgetExhausted(lastFailure error) parkReason {
	last := "no attempt finished"
	if lastFailure != nil {
		last = lastFailure.Error()
	}
	return parkReason{Code: "RETRY_BUDGET_EXHAUSTED", Source: "RETRY_BUDGET",
		Message: fmt.Sprintf("retry budget of %s exhausted; last error: %s", s.settings.RetryBudget, last)}
}

// parkChunk parks one chunk and waits for an operator decision. It returns the decision and the resolve
// request (nil when the batch was cancelled instead).
func (s *batchState) parkChunk(ctx workflow.Context, c *chunk, phase Phase, r parkReason) (string, *ResolveRequest) {
	allowed := []string{DecisionRetry, DecisionRejectChunk}
	if s.cancelAllowed(phase) {
		allowed = append(allowed, DecisionCancelBatch) // once approved, the wires cannot be cancelled
	}
	c.State = ChunkNeedsAttention
	c.Attention = &Attention{Code: r.Code, Message: r.Message, Phase: phase, Since: workflow.Now(ctx), Source: r.Source, AllowedDecisions: allowed}
	s.setParked(ctx, +1)
	s.addTimeline(ctx, EvChunkParked, fmt.Sprintf("%s: %s (%s)", c.Ref.ChunkID, r.Code, s.maskedAccountOf(c)))
	s.publishDetails(ctx)
	// PRODUCTION NOTE: page or post to the operations channel here (or alert on a metric). The Activity-side
	// retry metric already covers the transient case; this is the "a human must act" case.
	workflow.GetLogger(ctx).Warn("chunk parked", "batchId", s.in.BatchID, "chunkId", c.Ref.ChunkID, "code", r.Code, "phase", phase)

	err := workflow.Await(ctx, func() bool { return c.Resolution != nil || s.closeRequest != nil })

	decision, res := DecisionCancelBatch, c.Resolution
	if res != nil {
		decision = res.Decision
	}
	if err != nil {
		s.fatal = err
	}
	c.Resolution, c.Attention = nil, nil
	s.setParked(ctx, -1)
	by := "batch cancellation"
	if res != nil {
		by = res.By
	}
	s.addTimeline(ctx, EvChunkResolved, fmt.Sprintf("%s: %s by %s", c.Ref.ChunkID, decision, by))
	s.publishDetails(ctx)
	return decision, res
}

// parkBatch parks a batch-level step (target "_batch"). Only RETRY is accepted, plus CANCEL_BATCH while the
// batch is still drafting. Returns the decision.
func (s *batchState) parkBatch(ctx workflow.Context, step string, r parkReason, allowCancel bool) string {
	// Concurrent policy checks can fail together; "_batch" names exactly one parked item at a time.
	if err := s.batchParkSlot.Acquire(ctx, 1); err != nil {
		s.fatal = err
		return DecisionCancelBatch
	}
	defer s.batchParkSlot.Release(1)

	allowCancel = allowCancel && s.cancelAllowed(PhaseDrafting)
	allowed := []string{DecisionRetry}
	if allowCancel {
		allowed = append(allowed, DecisionCancelBatch)
	}
	s.batchAttention = &Attention{Code: r.Code, Message: r.Message, Phase: s.phase, Since: workflow.Now(ctx), Source: r.Source, Step: step, AllowedDecisions: allowed}
	s.setParked(ctx, +1)
	s.addTimeline(ctx, EvChunkParked, fmt.Sprintf("%s (%s): %s", BatchTarget, step, r.Code))
	s.publishDetails(ctx)
	workflow.GetLogger(ctx).Warn("batch step parked", "batchId", s.in.BatchID, "step", step, "code", r.Code)

	err := workflow.Await(ctx, func() bool { return s.batchResolution != nil || (allowCancel && s.closeRequest != nil) })

	decision, by := DecisionCancelBatch, "batch cancellation"
	if s.batchResolution != nil {
		decision, by = s.batchResolution.Decision, s.batchResolution.By
	}
	if err != nil {
		s.fatal = err
	}
	s.batchResolution, s.batchAttention = nil, nil
	s.setParked(ctx, -1)
	s.addTimeline(ctx, EvChunkResolved, fmt.Sprintf("%s (%s): %s by %s", BatchTarget, step, decision, by))
	s.publishDetails(ctx)
	return decision
}

// mustStep runs a batch-level Activity (PlanChunks, EnsureGenTxBatch, CheckGroupPolicy, SetGenTxBatchStatus) with
// the same park-and-resolve semantics as chunks. It returns true when the step completed, false when the batch
// was cancelled while the step was parked (or the workflow itself was cancelled: s.fatal).
func (s *batchState) mustStep(ctx workflow.Context, step, summary string, activity any, input any, result any, allowCancel bool) bool {
	return s.mustStepInSlot(ctx, nil, step, summary, activity, input, result, allowCancel)
}

// mustStepInSlot is mustStep for a step that runs under a concurrency slot (the policy checks). The slot is
// held only while the Activity runs: it is released before parking and re-acquired for the retry.
// WHY: same rule as chunks (chunks.go). A parked step must not hold a slot, or a few parked policy checks
// would stall the others. On return the slot is NOT held, whatever the outcome.
func (s *batchState) mustStepInSlot(ctx workflow.Context, slot workflow.Semaphore, step, summary string, activity any, input any, result any, allowCancel bool) bool {
	for cycle := 0; ; cycle++ {
		if slot != nil {
			if err := slot.Acquire(ctx, 1); err != nil {
				s.fatal = err
				return false
			}
		}
		actx := workflow.WithActivityOptions(ctx, coreActivityOptions(s.settings, fmt.Sprintf("%s/r%d", step, cycle), summary))
		err := workflow.ExecuteActivity(actx, activity, input).Get(actx, result)
		if slot != nil {
			slot.Release(1)
		}
		if err == nil {
			return true
		}
		reason, park := s.shouldPark(err)
		if !park {
			s.fatal = err
			return false
		}
		if s.parkBatch(ctx, step, reason, allowCancel) != DecisionRetry || s.fatal != nil {
			return false
		}
	}
}
