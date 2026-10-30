//go:build integration

// Integration test against a real Temporal dev server (make test-integration): the Core task queue per FI isolates
// FIs. With NO worker polling Lakeshore's Core queue (its Core-queue workers stopped), a Lakeshore batch waits with
// its Core Activity scheduled on bulkwire-core-lakeshore, while a Prairie batch runs to AUTHORIZED. When a Lakeshore
// Core-queue worker starts, the waiting batch continues from where it stopped.
//
// The Core Activities are fakes registered under the production names; the workflow is the production code.
// This is the evidence for the Narrated failure-catalog entry "Core-queue workers for one FI stopped": in the demo
// topology every worker process polls every queue, so one FI's Core workers cannot be stopped from the Control Center.

package workflow

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	sdkworkflow "go.temporal.io/sdk/workflow"

	"bulkwire/internal/activities"
	"bulkwire/internal/temporalx"
)

func TestCoreQueueWorkersForOneFIStopped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		ExistingPath:   os.Getenv("TEMPORAL_CLI_PATH"),
		CachedDownload: testsuite.CachedDownload{Version: "v1.9.1"}, // the CLI version of the compose image
		ClientOptions:  &client.Options{Namespace: "bulkwire"},
		SearchAttributes: temporal.NewSearchAttributes(
			temporalx.SABulkWireStatus.ValueSet(""), temporalx.SAFiID.ValueSet(""), temporalx.SACustomerID.ValueSet(""),
			temporalx.SAInitiatedBy.ValueSet(""), temporalx.SANeedsAttention.ValueSet(false)),
	})
	require.NoError(t, err)
	defer func() { _ = srv.Stop() }()
	c := srv.Client()

	shared := worker.New(c, testTaskQueue, worker.Options{})
	shared.RegisterWorkflowWithOptions(BulkWireBatch, sdkworkflow.RegisterOptions{Name: WorkflowName})
	shared.RegisterActivityWithOptions(func(context.Context, activities.PlanInput) (activities.Plan, error) { return testPlan, nil },
		activity.RegisterOptions{Name: "PlanChunks"})
	require.NoError(t, shared.Start())
	defer shared.Stop()
	prairie := coreQueueWorker(c, "bulkwire-core-prairie")
	require.NoError(t, prairie.Start())
	defer prairie.Stop()
	// No worker on bulkwire-core-lakeshore: Lakeshore's Core-queue workers are stopped.

	start := func(fi, batch string) client.WorkflowRun {
		in := testInput()
		in.FiID, in.BatchID, in.CoreTaskQueue, in.CoreFairnessKey = fi, batch, "bulkwire-core-"+fi, batch
		op := c.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
			ID: temporalx.WorkflowID(fi, batch), TaskQueue: testTaskQueue,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		}, BulkWireBatch, in)
		_, err := c.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{StartWorkflowOperation: op,
			UpdateOptions: client.UpdateWorkflowOptions{UpdateName: UpdateAuthorize, UpdateID: "APR-" + batch,
				Args: []any{AuthorizeRequest{ApprovalRef: "APR-" + batch}}, WaitForStage: client.WorkflowUpdateStageCompleted}})
		require.NoError(t, err)
		run, err := op.Get(ctx)
		require.NoError(t, err)
		return run
	}
	lakeshoreRun := start("lakeshore", "01JLAKE")
	prairieRun := start("prairie", "01JPRAI")

	var prairieResult BatchResult
	require.NoError(t, prairieRun.Get(ctx, &prairieResult), "Prairie is unaffected")
	require.Equal(t, PhaseAuthorized, prairieResult.Phase)

	// Lakeshore waits: its first Core Activity is scheduled on its own queue and has not started. Nothing failed
	// or parked. (If the workers stayed away longer than the retry budget, ScheduleToClose would park the step.)
	desc, err := c.DescribeWorkflowExecution(ctx, lakeshoreRun.GetID(), "")
	require.NoError(t, err)
	require.Equal(t, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, desc.GetWorkflowExecutionInfo().GetStatus())
	require.Len(t, desc.GetPendingActivities(), 1)
	pending := desc.GetPendingActivities()[0]
	require.Equal(t, "EnsureGenTxBatch", pending.GetActivityType().GetName())
	require.Equal(t, enumspb.PENDING_ACTIVITY_STATE_SCHEDULED, pending.GetState())

	lakeshore := coreQueueWorker(c, "bulkwire-core-lakeshore") // Lakeshore's Core-queue workers come back
	require.NoError(t, lakeshore.Start())
	defer lakeshore.Stop()
	var lakeshoreResult BatchResult
	require.NoError(t, lakeshoreRun.Get(ctx, &lakeshoreResult))
	require.Equal(t, PhaseAuthorized, lakeshoreResult.Phase)
	require.Equal(t, 751, lakeshoreResult.Totals.Authorized)
}

// coreQueueWorker polls one Core queue with fake Core Activities registered under the production names.
func coreQueueWorker(c client.Client, queue string) worker.Worker {
	w := worker.New(c, queue, worker.Options{DisableWorkflowWorker: true})
	for name, fn := range map[string]any{
		"EnsureGenTxBatch": func(context.Context, activities.EnsureInput) (int64, error) { return 100000, nil },
		"CheckGroupPolicy": func(context.Context, activities.PolicyInput) (activities.PolicyDecision, error) {
			return activities.PolicyDecision{Decision: activities.DecisionAllow}, nil
		},
		"InsertDraftChunk": func(_ context.Context, in activities.DraftChunkInput) (activities.ChunkDraftResult, error) {
			return activities.ChunkDraftResult{ChunkDrafted: in.Chunk.Length, InsertedThisCall: in.Chunk.Length}, nil
		},
		"AuthorizeChunk": func(_ context.Context, in activities.AuthorizeChunkInput) (activities.ChunkAuthorizeResult, error) {
			n := lengthOf(in.ChunkID)
			return activities.ChunkAuthorizeResult{ChunkAuthorized: n, FlippedThisCall: n}, nil
		},
		"SetGenTxBatchStatus": func(_ context.Context, in activities.StatusInput) (activities.GenTxBatchStatus, error) {
			return activities.GenTxBatchStatus{Status: in.Status}, nil
		},
	} {
		w.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
	return w
}
