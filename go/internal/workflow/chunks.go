// Chunks: bounded parallelism with park-and-resolve.
//
// PATTERN: Bounded Parallelism (Semaphore) — https://pkg.go.dev/go.temporal.io/sdk/workflow#NewSemaphore
// PATTERN: Basic Workflow Batch — https://docs.temporal.io/design-patterns/batch-processing-patterns
// PATTERN: Resumable Activity — https://docs.temporal.io/design-patterns/resumable-activity
//
// Every chunk is ONE Activity inside this one workflow (no child workflows). A workflow semaphore keeps at
// most `concurrency` chunk Activities in flight, which is how this batch limits its own load on Core. Across
// batches, the FI's Core task queue protects Core: its rate limit caps how fast Core Activities start, and
// Fairness keyed by batch ID shares that rate among the FI's batches (options.go, internal/routing). A chunk
// that fails structurally parks alone and releases its slot, so the other chunks keep flowing.
package workflow

import (
	"fmt"

	"go.temporal.io/sdk/workflow"

	"bulkwire/internal/activities"
	"bulkwire/internal/model"
)

// Reject code an operator's REJECT_CHUNK writes into Core.
const operatorRejectedCode = "OPERATOR_REJECTED"

func (s *batchState) runChunks(ctx workflow.Context, phase Phase, run func(workflow.Context, *chunk) error) {
	sem := workflow.NewSemaphore(ctx, int64(s.settings.Concurrency))
	wg := workflow.NewWaitGroup(ctx)
	for _, c := range s.chunksFor(phase) { // DETERMINISM: plan order (a slice), never map order
		wg.Add(1)
		// DETERMINISM: workflow.Go, not a Go goroutine. The SDK runs these coroutines one at a time, in a
		// deterministic order, so replay rebuilds exactly the same commands.
		workflow.Go(ctx, func(g workflow.Context) {
			defer wg.Done()
			var operator *ResolveRequest // set after REJECT_CHUNK: the next run carries out the rejection
			for {
				if s.closeRequest != nil {
					s.markCancelled(c) // stop launching new work on cancel; in-flight chunks finish
					return
				}
				if err := sem.Acquire(g, 1); err != nil {
					s.fatal = err
					return
				}
				if s.closeRequest != nil { // the batch closed while this chunk waited for a slot
					sem.Release(1)
					s.markCancelled(c)
					return
				}
				c.State = ChunkRunning
				var err error
				if operator == nil {
					c.ActivityID = activityID(phase, c, false)
					err = run(g, c) // executes ONE Activity with ActivityID set
				} else {
					c.ActivityID = activityID(phase, c, true)
					err = s.rejectChunkByOperator(g, c, phase, operator)
				}
				sem.Release(1) // WHY here, before parking: a parked chunk must not hold a concurrency slot
				s.publishDetails(g)
				if err == nil {
					return // run() already recorded the result into c
				}
				reason, park := s.shouldPark(err)
				if !park {
					s.fatal = err // only cancellation-type errors reach here
					return
				}
				decision, res := s.parkChunk(g, c, phase, reason)
				switch decision {
				case DecisionRetry:
					c.Cycle++
				case DecisionRejectChunk:
					c.Cycle++
					operator = res
				default: // CANCEL_BATCH (an operator's decision, or the batch was cancelled/denied meanwhile)
					s.markCancelled(c)
					return
				}
			}
		})
	}
	wg.Wait(ctx)
}

// chunksFor returns the chunks with work to do in a phase, in plan order. In the authorize phase, chunks
// whose wires were all rejected during drafting have nothing to authorize and become SKIPPED.
func (s *batchState) chunksFor(phase Phase) []*chunk {
	var out []*chunk
	for _, c := range s.chunks {
		if phase == PhaseAuthorizing && c.Drafted == 0 {
			c.State = ChunkSkipped
			continue
		}
		out = append(out, c)
	}
	return out
}

func (s *batchState) markCancelled(c *chunk) {
	switch c.State {
	case ChunkPending, ChunkRunning, ChunkNeedsAttention:
		c.State = ChunkCancelled
	}
}

// activityID is "{draft|authorize|reject}/{chunkId}/r{cycle}". WHY: unique per attempt cycle, and it lets
// bulkwire-service map DescribeWorkflowExecution's pending Activities back to chunks for retry telemetry.
func activityID(phase Phase, c *chunk, operatorReject bool) string {
	kind := "draft"
	if phase == PhaseAuthorizing {
		kind = "authorize"
		if operatorReject {
			kind = "reject"
		}
	}
	return fmt.Sprintf("%s/%s/r%d", kind, c.Ref.ChunkID, c.Cycle)
}

// draftChunk writes one chunk to Core. If its group was denied by Core's limit policy, the wires are still
// written, as Rejected (DAILY_LIMIT_EXCEEDED), so Core remains the complete record.
// PATTERN: Business Outcome as Result — https://docs.temporal.io/design-patterns/non-retryable-errors
func (s *batchState) draftChunk(ctx workflow.Context, c *chunk) error {
	var force *activities.ForceReject
	if g := s.group(c.Ref.GroupKey); g != nil && g.Decision == activities.DecisionDeny {
		force = &activities.ForceReject{Code: g.Code, Message: g.Message}
	}
	return s.insertChunk(ctx, c, force)
}

func (s *batchState) insertChunk(ctx workflow.Context, c *chunk, force *activities.ForceReject) error {
	summary := fmt.Sprintf("Draft chunk %s · %d wires · %s", c.Ref.ChunkID, c.Ref.Length, s.groupLabel(c))
	if force != nil {
		summary += " · write as Rejected " + force.Code
	}
	actx := workflow.WithActivityOptions(ctx, activityOptions(s.settings, onCoreQueue, c.ActivityID, summary))
	var res activities.ChunkDraftResult
	err := workflow.ExecuteActivity(actx, a.InsertDraftChunk, activities.DraftChunkInput{
		FiID: s.in.FiID, BatchID: s.in.BatchID, GenTxBatchID: s.genTxBatchID, Chunk: c.Ref, ForceReject: force, Actor: activities.Actor,
	}).Get(actx, &res)
	if err != nil {
		return err
	}
	c.Drafted, c.Rejected, c.Rejections = res.ChunkDrafted, res.ChunkRejected, res.Rejected
	c.State = ChunkDrafted
	if res.ChunkDrafted == 0 {
		c.State = ChunkRejected
	}
	return nil
}

// authorizeChunk flips one chunk's Drafted wires to Authorized in Core.
func (s *batchState) authorizeChunk(ctx workflow.Context, c *chunk) error {
	summary := fmt.Sprintf("Authorize chunk %s · %d wires · %s", c.Ref.ChunkID, c.Drafted, s.authorization.ApprovalRef)
	actx := workflow.WithActivityOptions(ctx, activityOptions(s.settings, onCoreQueue, c.ActivityID, summary))
	var res activities.ChunkAuthorizeResult
	err := workflow.ExecuteActivity(actx, a.AuthorizeChunk, activities.AuthorizeChunkInput{
		FiID: s.in.FiID, GenTxBatchID: s.genTxBatchID, ChunkID: c.Ref.ChunkID, ApprovalRef: s.authorization.ApprovalRef, Actor: activities.Actor,
	}).Get(actx, &res)
	if err != nil {
		return err
	}
	s.applyAuthorizeResult(c, res)
	return nil
}

// rejectChunkByOperator carries out REJECT_CHUNK. While drafting, the wires do not exist in Core yet, so the
// chunk is written with ForceReject; while authorizing, its Drafted wires are flipped to Rejected. Either way
// the call runs through the same retry/park loop as any chunk (runChunks).
func (s *batchState) rejectChunkByOperator(ctx workflow.Context, c *chunk, phase Phase, op *ResolveRequest) error {
	message := fmt.Sprintf("Rejected by bank operations (%s)", op.By)
	if op.Note != "" {
		message += ": " + op.Note
	}
	if phase == PhaseDrafting {
		return s.insertChunk(ctx, c, &activities.ForceReject{Code: operatorRejectedCode, Message: message})
	}
	actx := workflow.WithActivityOptions(ctx, activityOptions(s.settings, onCoreQueue, c.ActivityID,
		fmt.Sprintf("Reject chunk %s · %s · operator decision", c.Ref.ChunkID, s.groupLabel(c))))
	var res activities.ChunkAuthorizeResult
	err := workflow.ExecuteActivity(actx, a.RejectChunk, activities.RejectChunkInput{
		FiID: s.in.FiID, GenTxBatchID: s.genTxBatchID, ChunkID: c.Ref.ChunkID, Code: operatorRejectedCode, Message: message, Actor: activities.Actor,
	}).Get(actx, &res)
	if err != nil {
		return err
	}
	s.applyAuthorizeResult(c, res)
	return nil
}

func (s *batchState) applyAuthorizeResult(c *chunk, res activities.ChunkAuthorizeResult) {
	c.Authorized, c.Rejected = res.ChunkAuthorized, res.ChunkRejected
	c.Drafted = c.Ref.Length - res.ChunkRejected // every wire in the chunk is now either accepted or rejected
	c.State = ChunkAuthorized
	if res.ChunkAuthorized == 0 {
		c.State = ChunkRejected
	}
}

// groupLabel is "••0033 · 2026-11-03" (masked) for summaries.
func (s *batchState) groupLabel(c *chunk) string {
	if g := s.group(c.Ref.GroupKey); g != nil {
		return fmt.Sprintf("%s · %s", model.MaskAccount(g.Info.OriginatingAccount), g.Info.ProcessDate)
	}
	return ""
}
