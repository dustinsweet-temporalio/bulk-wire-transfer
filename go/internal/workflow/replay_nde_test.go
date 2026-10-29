//go:build ndedemo

// Non-determinism demo (catalog F39, `make replay-nde`). It registers a deliberately modified copy of the
// workflow under the SAME type name, with one change: Core's batch status is set to Drafted BEFORE the chunk loop
// instead of after it. Replaying the captured Scenario A history then fails, because the new code issues a
// SetGenTxBatchStatus command where the history recorded InsertDraftChunk commands.
//
// This is exactly what Worker Versioning (Pinned) avoids in production (a running batch never meets code it did
// not start with) and what replay tests catch in CI (before the change is deployed).
package workflow

import (
	"path/filepath"
	"strings"
	"testing"

	"go.temporal.io/sdk/workflow"

	"bulkwire/internal/activities"
)

// reorderedBulkWireBatch is BulkWireBatch's draft phase with the Core status step moved before the chunks.
func reorderedBulkWireBatch(ctx workflow.Context, in BatchInput) (BatchResult, error) {
	s := newBatchState(ctx, in)
	if err := registerHandlers(ctx, s); err != nil {
		return BatchResult{}, err
	}
	s.publishDetails(ctx)
	if _, err := awaitWithTimer(ctx, in.ApprovalWindow, "Waiting for a first instruction",
		func() bool { return s.draft != nil || s.authorization != nil }); err != nil {
		return BatchResult{}, err
	}
	s.setPhase(ctx, PhaseDrafting)
	var plan activities.Plan
	s.mustStep(ctx, "plan", "Plan chunks", a.PlanChunks, activities.PlanInput{FiID: in.FiID, BatchID: in.BatchID, ChunkSize: in.ChunkSize}, &plan, true)
	s.applyPlan(ctx, plan)
	s.mustStep(ctx, "gentxbatch", "Create Core batch", a.EnsureGenTxBatch, activities.EnsureInput{FiID: in.FiID, BatchID: in.BatchID,
		CustomerID: in.CustomerID, InitiatedBy: in.InitiatedBy, RowCount: in.RowCount, AmountCents: in.TotalAmountCents}, &s.genTxBatchID, true)
	s.checkGroupPolicies(ctx)
	s.setCoreStatus(ctx, activities.GenTxDrafted, "", true) // MOVED: was after runChunks
	s.runChunks(ctx, PhaseDrafting, s.draftChunk)
	return s.result(), nil
}

func TestReplayNonDeterminism(t *testing.T) {
	f := filepath.Join(historiesDir, "scenario-a-outage.json")
	err := newReplayer(t, reorderedBulkWireBatch).ReplayWorkflowHistoryFromJSONFile(nil, f)
	if err == nil {
		t.Fatal("expected a non-determinism error, replay succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "nondetermin") {
		t.Fatalf("expected a non-determinism error, got: %v", err)
	}
	t.Logf("replay of %s with the reordered workflow failed as intended:\n%v", filepath.Base(f), err)
	// `make replay-nde` exists to SHOW this failure: report it as a failed test so the presenter sees it fail.
	t.Errorf("NON-DETERMINISM DETECTED (intended demo failure): %v", err)
}
