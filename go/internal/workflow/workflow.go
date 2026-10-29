// Package workflow is BulkWireBatch: ONE workflow per uploaded batch that drafts every valid wire into Core,
// waits durably for Core's approval decision, and authorizes every wire. It performs no I/O: every external
// call is an Activity (internal/activities).
//
// PATTERN: Single Workflow per Business Process — https://docs.temporal.io/child-workflows#when-to-use-child-workflows
// One execution owns the whole lifecycle: draft, durable wait for authorization, authorize. There are no
// child workflows, no Signals, and no Continue-As-New.
//
// History budget (why no Continue-As-New): each Activity adds about 6 events in the worst case, and retries
// are NOT recorded as separate events (only the final attempt's Started/Completed). 5,000 rows at 250 per
// chunk: 20 draft chunks + 20 authorize chunks + ~6 policy checks + ~5 single steps ≈ 51 Activities ≈ 300
// events, plus Updates and one timer. That is far below the 10,240-event warning and the "few thousand
// events" guidance. Reference: https://docs.temporal.io/workflow-execution/limits
package workflow

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/workflow"

	"bulkwire/internal/activities"
	"bulkwire/internal/buildflags"
)

// a is a nil *Activities: the standard Go SDK way to reference Activity methods type-safely from workflow
// code. Only the method names are used; the workflow never calls them directly.
var a *activities.Activities

// BulkWireBatch reads top to bottom as the business process.
func BulkWireBatch(ctx workflow.Context, in BatchInput) (BatchResult, error) {
	s := newBatchState(ctx, in)
	if err := registerHandlers(ctx, s); err != nil { // before the first blocking call (see handlers.go)
		return BatchResult{}, err
	}
	s.publishDetails(ctx)

	// Wait for the first instruction. Update-with-Start normally delivers it in the first Workflow Task
	// (`draft` from the portal, or `authorize` from Core when an approver approved before anyone drafted).
	// WHY a timeout: Update-with-Start is not atomic. If the execution starts but its Update is never
	// accepted (no worker was polling and the caller gave up), nothing else will ever arrive. A batch nobody
	// acts on within the approval window is closed as CANCELLED instead of sitting in DRAFTING forever.
	ok, err := awaitWithTimer(ctx, in.ApprovalWindow, fmt.Sprintf("Waiting for a first instruction (%s)", humanDuration(in.ApprovalWindow)),
		func() bool { return s.draft != nil || s.authorization != nil || s.closeRequest != nil })
	if err != nil {
		return BatchResult{}, err
	}
	if !ok {
		s.addTimeline(ctx, EvNoInstruction, "no draft or approval arrived within "+humanDuration(in.ApprovalWindow))
		return s.finish(ctx, PhaseCancelled)
	}
	if s.closeRequest != nil {
		return s.finish(ctx, s.closeRequest.Phase) // e.g. denied or cancelled before anything ran
	}

	// ---- Draft phase -------------------------------------------------------------------------------------
	s.setPhase(ctx, PhaseDrafting)

	var plan activities.Plan
	if !s.mustStep(ctx, "plan", fmt.Sprintf("Plan chunks · %d wires · chunk size %d", in.RowCount, in.ChunkSize),
		a.PlanChunks, activities.PlanInput{FiID: in.FiID, BatchID: in.BatchID, ChunkSize: in.ChunkSize}, &plan, true) {
		return s.stopped(ctx)
	}
	s.applyPlan(ctx, plan)

	if !s.mustStep(ctx, "gentxbatch", "Create Core batch · "+dollars(in.TotalAmountCents),
		a.EnsureGenTxBatch, activities.EnsureInput{FiID: in.FiID, BatchID: in.BatchID, CustomerID: in.CustomerID,
			InitiatedBy: in.InitiatedBy, RowCount: in.RowCount, AmountCents: in.TotalAmountCents}, &s.genTxBatchID, true) {
		return s.stopped(ctx)
	}
	s.addTimeline(ctx, EvGenTxBatchReady, fmt.Sprintf("Core batch %d", s.genTxBatchID))

	s.checkGroupPolicies(ctx)                     // one CheckGroupPolicy per group, bounded by the same concurrency
	s.runChunks(ctx, PhaseDrafting, s.draftChunk) // N in flight; failed chunks park alone
	if s.fatal != nil {
		return BatchResult{}, s.fatal
	}
	if s.closeRequest != nil {
		return s.finish(ctx, s.closeRequest.Phase) // CANCELLED, or DENIED if Core denied mid-draft
	}
	if s.totals().Drafted == 0 {
		return s.finish(ctx, PhaseRejected) // every wire rejected (policy, screening, or operator)
	}
	if !s.setCoreStatus(ctx, activities.GenTxDrafted, "", true) {
		return s.stopped(ctx)
	}
	s.authorizeBy = workflow.Now(ctx).Add(in.ApprovalWindow)
	s.setPhase(ctx, PhaseDrafted)

	// ---- Wait for Core's decision --------------------------------------------------------------------------
	// PATTERN: Approval — https://docs.temporal.io/design-patterns/approval
	// PATTERN: Durable Timer — https://docs.temporal.io/design-patterns/approval
	// Waiting hours or days costs nothing: no worker holds this batch in memory while it waits, and the
	// timer survives restarts of workers and of the Temporal Service.
	if s.authorization == nil && s.closeRequest == nil {
		s.addTimeline(ctx, EvAwaitingAuthorization, "until "+s.authorizeBy.UTC().Format(time.RFC3339))
		// DETERMINISM: a durable timer (with a summary for the Web UI) and workflow.Now, never the system clock.
		ok, err := awaitWithTimer(ctx, in.ApprovalWindow, fmt.Sprintf("Approval window (%s)", humanDuration(in.ApprovalWindow)),
			func() bool { return s.authorization != nil || s.closeRequest != nil })
		if err != nil {
			return BatchResult{}, err
		}
		if !ok {
			return s.finish(ctx, PhaseExpired)
		}
	}
	if s.closeRequest != nil {
		return s.finish(ctx, s.closeRequest.Phase) // DENIED or CANCELLED
	}

	// ---- Authorize phase ---------------------------------------------------------------------------------
	if !s.setCoreStatus(ctx, activities.GenTxAuthorizing, "approval "+s.authorization.ApprovalRef, false) {
		return s.stopped(ctx)
	}

	// DEMO-ONLY: compile-time constant, identical on every replay of this binary, so it is deterministic.
	// Only the worker-v2 image is built with it; it simulates shipping a workflow bug. A panic fails the
	// Workflow TASK (not the workflow): Temporal retries the task, the batch waits here with all progress
	// intact (the approval is already recorded and Core is Authorizing), and the stack trace is visible in the
	// Web UI until a fixed build takes over. WHY here and not before the Core status step: an Update is durable
	// only once a Workflow Task accepts it, so a panic in the task that receives `authorize` would reject the
	// approval itself; one Activity later, the approval is safely in history.
	if buildflags.DemoBug == "panic-before-authorize" {
		panic("demo bug: authorize step crashed (worker-v2)")
	}

	s.setPhase(ctx, PhaseAuthorizing)
	s.runChunks(ctx, PhaseAuthorizing, s.authorizeChunk) // only chunks with Drafted > 0
	if s.fatal != nil {
		return BatchResult{}, s.fatal
	}
	if !s.setCoreStatus(ctx, activities.GenTxAuthorized, "approval "+s.authorization.ApprovalRef, false) {
		return s.stopped(ctx)
	}
	s.setPhase(ctx, PhaseAuthorized)

	// Let any in-flight Update handler finish before completing.
	if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
		return BatchResult{}, err
	}
	return s.result(), nil
}

// finish closes the batch with a business outcome (EXPIRED, DENIED, CANCELLED, REJECTED). If Core has a batch
// record, Core is told first (via mustStep, so a Core outage is retried and Core always ends consistent). Closed
// outcomes are SUCCESSFUL workflow completions: they are business results, not workflow failures.
func (s *batchState) finish(ctx workflow.Context, p Phase) (BatchResult, error) {
	if s.genTxBatchID != 0 {
		reason := strings.ToLower(string(p))
		if s.closeRequest != nil && s.closeRequest.By != "" {
			reason = strings.TrimSpace(fmt.Sprintf("%s by %s %s", reason, s.closeRequest.By, s.closeRequest.Reason))
		}
		if !s.setCoreStatus(ctx, coreStatusFor[p], reason, false) && s.fatal != nil {
			return BatchResult{}, s.fatal
		}
	}
	for _, c := range s.chunks {
		if c.State == ChunkPending {
			c.State = ChunkCancelled
		}
	}
	s.setPhase(ctx, p)
	if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
		return BatchResult{}, err
	}
	return s.result(), nil
}

// stopped is reached when a batch-level step returned without completing: either the batch was cancelled
// while that step was parked, or the workflow itself was cancelled.
func (s *batchState) stopped(ctx workflow.Context) (BatchResult, error) {
	if s.fatal != nil {
		return BatchResult{}, s.fatal
	}
	return s.finish(ctx, s.closeRequest.Phase)
}

// setCoreStatus keeps Core's batch record in step with the workflow's phase.
func (s *batchState) setCoreStatus(ctx workflow.Context, status, reason string, allowCancel bool) bool {
	var out activities.GenTxBatchStatus
	return s.mustStep(ctx, "status-"+strings.ToLower(status), "Set Core batch status → "+status,
		a.SetGenTxBatchStatus, activities.StatusInput{FiID: s.in.FiID, GenTxBatchID: s.genTxBatchID, Status: status,
			Reason: reason, Actor: activities.Actor}, &out, allowCancel)
}

// applyPlan turns the plan's descriptors into state. Planning needed only the manifest, never rows.
func (s *batchState) applyPlan(ctx workflow.Context, plan activities.Plan) {
	for _, g := range plan.Groups {
		s.groups = append(s.groups, &groupDecision{Info: g, Decision: "PENDING"})
	}
	for _, ref := range plan.Chunks {
		s.chunks = append(s.chunks, &chunk{Ref: ref, State: ChunkPending})
	}
	s.addTimeline(ctx, EvPlanReady, fmt.Sprintf("%d groups · %d chunks of up to %d wires", len(s.groups), len(s.chunks), s.settings.ChunkSize))
	s.publishDetails(ctx)
}

// checkGroupPolicies asks Core for each group's daily-limit decision, at most `concurrency` at a time. A DENY is
// recorded (and later written to Core as rejected wires); it is not an error.
func (s *batchState) checkGroupPolicies(ctx workflow.Context) {
	sem := workflow.NewSemaphore(ctx, int64(s.settings.Concurrency))
	wg := workflow.NewWaitGroup(ctx)
	for i, g := range s.groups {
		wg.Add(1)
		workflow.Go(ctx, func(gctx workflow.Context) {
			defer wg.Done()
			var d activities.PolicyDecision
			summary := fmt.Sprintf("Policy check %s · %s · %s", maskedGroup(g), g.Info.ProcessDate, dollars(g.Info.TotalAmountCents))
			// The slot is held only while the check runs; a parked check releases it (mustStepInSlot).
			if !s.mustStepInSlot(gctx, sem, fmt.Sprintf("policy/g%02d", i), summary, a.CheckGroupPolicy,
				activities.PolicyInput{FiID: s.in.FiID, GenTxBatchID: s.genTxBatchID, Group: g.Info}, &d, true) {
				return
			}
			g.Decision, g.Code, g.Message = d.Decision, d.Code, d.Message
			if d.Decision == activities.DecisionDeny {
				s.addTimeline(gctx, EvGroupDenied, fmt.Sprintf("%s · %s: %s", maskedGroup(g), g.Info.ProcessDate, d.Code))
			}
		})
	}
	wg.Wait(ctx)
	s.publishDetails(ctx)
}

// awaitWithTimer waits until condition() is true (returns true) or the durable timer fires (returns false).
// WHY not workflow.AwaitWithOptions: in this SDK version it leaves its timer running after the condition is met,
// so the history and the Web UI show a timer that no longer means anything, and it fires later for nothing. Here
// the timer is cancelled the moment the condition holds. If that happens in the same Workflow Task that created
// it (an Update delivered with Update-with-Start), the timer never reaches the history at all.
func awaitWithTimer(ctx workflow.Context, d time.Duration, summary string, condition func() bool) (bool, error) {
	if condition() {
		return true, nil
	}
	tctx, cancelTimer := workflow.WithCancel(ctx)
	defer cancelTimer()
	timer := workflow.NewTimerWithOptions(tctx, d, workflow.TimerOptions{Summary: summary})
	if err := workflow.Await(ctx, func() bool { return condition() || timer.IsReady() }); err != nil {
		return false, err
	}
	return condition(), nil
}

func humanDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return d.String()
}
