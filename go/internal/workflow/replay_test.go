// Replay tests: re-run the CURRENT workflow code against real histories captured from the running system
// (go/testdata/histories, exported with scripts/capture-history.sh). If a code change would make the workflow
// issue different commands than it did when the history was recorded, replay fails with a non-determinism
// error. Run this in CI before every worker deployment (`make replay`). Histories are stored encrypted, exactly
// as the Temporal Service holds them, so the replayer uses the same encrypting DataConverter as the workers.
package workflow

import (
	"path/filepath"
	"testing"

	"go.temporal.io/sdk/worker"
	sdkworkflow "go.temporal.io/sdk/workflow"

	"bulkwire/internal/codec"
)

const historiesDir = "../../testdata/histories"

func newReplayer(t *testing.T, wf any) worker.WorkflowReplayer {
	t.Helper()
	cdc, err := codec.LoadFile("../../../config/keyring.json")
	if err != nil {
		t.Fatal(err)
	}
	dc := codec.NewDataConverter(cdc)
	r, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{DataConverter: dc, FailureConverter: codec.NewFailureConverter(dc)})
	if err != nil {
		t.Fatal(err)
	}
	r.RegisterWorkflowWithOptions(wf, sdkworkflow.RegisterOptions{Name: WorkflowName})
	return r
}

func capturedHistories(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(historiesDir, "*.json"))
	if len(files) == 0 {
		t.Skip("no captured histories in go/testdata/histories (scripts/capture-history.sh)")
	}
	return files
}

func TestReplay(t *testing.T) {
	r := newReplayer(t, BulkWireBatch)
	for _, f := range capturedHistories(t) {
		t.Run(filepath.Base(f), func(t *testing.T) {
			if err := r.ReplayWorkflowHistoryFromJSONFile(nil, f); err != nil {
				t.Fatalf("replay failed (non-determinism?): %v", err)
			}
		})
	}
}
