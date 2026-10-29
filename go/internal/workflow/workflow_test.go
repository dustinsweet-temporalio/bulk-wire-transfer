// Workflow tests W1–W15 in the Temporal test environment: the happy path, outages retried in place,
// park-and-resolve, validators that reject without mutating state, approve-before-draft, expiry, and the semaphore.
// Activities are mocked; the workflow code under test is the production code.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"bulkwire/internal/activities"
	"bulkwire/internal/model"
	"bulkwire/internal/temporalx"
)

// Plan used by most tests: group g00 (Cold Chain ••0033, 501 rows -> 250 + 250 + 1) and group g01 (••0017, 250).
var testPlan = activities.Plan{
	Groups: []model.GroupInfo{
		{GroupKey: "4401220033|2026-11-03", OriginatingAccount: "4401220033", ProcessDate: "2026-11-03", StartOffset: 0, RowCount: 501, TotalAmountCents: 1_000_000},
		{GroupKey: "4401220017|2026-11-03", OriginatingAccount: "4401220017", ProcessDate: "2026-11-03", StartOffset: 501, RowCount: 250, TotalAmountCents: 500_000},
	},
	Chunks: []model.ChunkRef{
		{ChunkID: "g00-c00", GroupKey: "4401220033|2026-11-03", Offset: 0, Length: 250},
		{ChunkID: "g00-c01", GroupKey: "4401220033|2026-11-03", Offset: 250, Length: 250},
		{ChunkID: "g00-c02", GroupKey: "4401220033|2026-11-03", Offset: 500, Length: 1},
		{ChunkID: "g01-c00", GroupKey: "4401220017|2026-11-03", Offset: 501, Length: 250},
	},
}

func testInput() BatchInput {
	return BatchInput{BatchID: "01JTEST", FiID: "lakeshore", CustomerID: "summit-logistics", InitiatedBy: "jordan",
		RowCount: 751, TotalAmountCents: 1_500_000, ChunkSize: 250, Concurrency: 2, ApprovalWindow: 24 * time.Hour,
		RetryBudget: 30 * time.Minute, RetryMaxInterval: 20 * time.Second, FileName: "payroll.csv", GroupCount: 2}
}

var errOnHold = temporal.NewNonRetryableApplicationError("ACCOUNT_ON_HOLD: account ending 0033 is on hold (HTTP 422)",
	model.ErrTypeCoreStructural, nil, model.CoreErrorDetails{Code: "ACCOUNT_ON_HOLD", Message: "account ending 0033 is on hold", HTTPStatus: 422})

var errTransient = temporal.NewApplicationError("CORE_UNAVAILABLE: Core is unavailable (HTTP 503)", model.ErrTypeCoreTransient,
	model.CoreErrorDetails{Code: "CORE_UNAVAILABLE", HTTPStatus: 503})

// fakeCore is a mutex-guarded recorder behind the mocked Activities. Tests change its behavior per chunk.
type fakeCore struct {
	mu           sync.Mutex
	inserts      []activities.DraftChunkInput
	authorizes   []activities.AuthorizeChunkInput
	rejects      []activities.RejectChunkInput
	statuses     []string
	policyCalls  int
	activityIDs  []string
	draftFail    map[string]error // chunkId -> error to return (until cleared)
	transientFor map[string]int   // chunkId -> remaining transient failures
	authFail     map[string]error
	deny         map[string]bool // groupKey -> DENY
	inFlight     int
	maxInFlight  int
	slow         time.Duration
	blocked      map[string]bool // chunkId -> beneficiary-blocked count of 1
}

func newFakeCore() *fakeCore {
	return &fakeCore{draftFail: map[string]error{}, transientFor: map[string]int{}, authFail: map[string]error{}, deny: map[string]bool{}, blocked: map[string]bool{}}
}

type WorkflowSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env        *testsuite.TestWorkflowEnvironment
	core       *fakeCore
	saStatuses []string
	saAttn     []bool
	rejected   map[string]error // update ID -> validator error
}

func TestWorkflowSuite(t *testing.T) { suite.Run(t, new(WorkflowSuite)) }

func (s *WorkflowSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.core = newFakeCore()
	s.saStatuses, s.saAttn, s.rejected = nil, nil, map[string]error{}
	core, env := s.core, s.env

	env.OnUpsertTypedSearchAttributes(mock.Anything).Run(func(args mock.Arguments) {
		sa := args.Get(0).(temporal.SearchAttributes)
		if v, ok := sa.GetKeyword(temporalx.SABulkWireStatus); ok {
			s.saStatuses = append(s.saStatuses, v)
		}
		if v, ok := sa.GetBool(temporalx.SANeedsAttention); ok {
			s.saAttn = append(s.saAttn, v)
		}
	}).Return(nil)

	env.OnActivity(a.PlanChunks, mock.Anything, mock.Anything).Return(testPlan, nil)
	env.OnActivity(a.EnsureGenTxBatch, mock.Anything, mock.Anything).Return(int64(100000), nil)
	env.OnActivity(a.CheckGroupPolicy, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in activities.PolicyInput) (activities.PolicyDecision, error) {
			core.mu.Lock()
			defer core.mu.Unlock()
			core.policyCalls++
			if core.deny[in.Group.GroupKey] {
				return activities.PolicyDecision{Decision: activities.DecisionDeny, Code: "DAILY_LIMIT_EXCEEDED", Message: "Daily limit exceeded"}, nil
			}
			return activities.PolicyDecision{Decision: activities.DecisionAllow}, nil
		})
	env.OnActivity(a.InsertDraftChunk, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, in activities.DraftChunkInput) (activities.ChunkDraftResult, error) {
			core.mu.Lock()
			core.inFlight++
			core.maxInFlight = max(core.maxInFlight, core.inFlight)
			slow := core.slow
			core.mu.Unlock()
			time.Sleep(slow)
			core.mu.Lock()
			defer core.mu.Unlock()
			core.inFlight--
			core.inserts = append(core.inserts, in)
			id := in.Chunk.ChunkID
			if in.ForceReject == nil {
				if err := core.draftFail[id]; err != nil {
					return activities.ChunkDraftResult{}, err
				}
				if core.transientFor[id] > 0 {
					core.transientFor[id]--
					return activities.ChunkDraftResult{}, errTransient
				}
				if core.blocked[id] {
					return activities.ChunkDraftResult{ChunkDrafted: in.Chunk.Length - 1, ChunkRejected: 1,
						Rejected: []model.Rejection{{RowNumber: 7, ExternalWireID: "01JTEST-00007", Code: "BENEFICIARY_BLOCKED"}}}, nil
				}
				return activities.ChunkDraftResult{ChunkDrafted: in.Chunk.Length, InsertedThisCall: in.Chunk.Length}, nil
			}
			return activities.ChunkDraftResult{ChunkRejected: in.Chunk.Length, InsertedThisCall: in.Chunk.Length}, nil
		})
	env.OnActivity(a.AuthorizeChunk, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in activities.AuthorizeChunkInput) (activities.ChunkAuthorizeResult, error) {
			core.mu.Lock()
			defer core.mu.Unlock()
			core.authorizes = append(core.authorizes, in)
			if err := core.authFail[in.ChunkID]; err != nil {
				return activities.ChunkAuthorizeResult{}, err
			}
			n := lengthOf(in.ChunkID)
			if core.blocked[in.ChunkID] {
				return activities.ChunkAuthorizeResult{ChunkAuthorized: n - 1, ChunkRejected: 1, FlippedThisCall: n - 1}, nil
			}
			return activities.ChunkAuthorizeResult{ChunkAuthorized: n, FlippedThisCall: n}, nil
		})
	env.OnActivity(a.RejectChunk, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in activities.RejectChunkInput) (activities.ChunkAuthorizeResult, error) {
			core.mu.Lock()
			defer core.mu.Unlock()
			core.rejects = append(core.rejects, in)
			n := lengthOf(in.ChunkID)
			return activities.ChunkAuthorizeResult{ChunkRejected: n, FlippedThisCall: n}, nil
		})
	env.OnActivity(a.SetGenTxBatchStatus, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in activities.StatusInput) (activities.GenTxBatchStatus, error) {
			core.mu.Lock()
			defer core.mu.Unlock()
			core.statuses = append(core.statuses, in.Status)
			return activities.GenTxBatchStatus{Status: in.Status}, nil
		})
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		core.mu.Lock()
		defer core.mu.Unlock()
		core.activityIDs = append(core.activityIDs, info.ActivityID)
	})
}

func lengthOf(chunkID string) int {
	for _, c := range testPlan.Chunks {
		if c.ChunkID == chunkID {
			return c.Length
		}
	}
	return 0
}

// update sends an Update at workflow time `at`; validator rejections are collected by update ID.
func (s *WorkflowSuite) update(at time.Duration, name, id string, arg any) {
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(name, id, &testsuite.TestUpdateCallback{
			OnAccept:   func() {},
			OnReject:   func(err error) { s.rejected[id] = err },
			OnComplete: func(any, error) {},
		}, arg)
	}, at)
}

func (s *WorkflowSuite) draftAt(at time.Duration) {
	s.update(at, UpdateDraft, "draft-1", DraftRequest{RequestedBy: "jordan"})
}

func (s *WorkflowSuite) authorizeAt(at time.Duration) {
	s.update(at, UpdateAuthorize, "APR-01JTEST", AuthorizeRequest{ApprovalRef: "APR-01JTEST",
		Approvers: []Approver{{UserID: "alex", DisplayName: "Alex Rivera"}, {UserID: "sam", DisplayName: "Sam Patel"}}})
}

func (s *WorkflowSuite) resolveAt(at time.Duration, chunkID, decision string) {
	s.update(at, UpdateResolve, "resolve-"+chunkID+"-"+decision, ResolveRequest{ChunkID: chunkID, Decision: decision, By: "morgan", Note: "test"})
}

// queryAt runs fn with the status view at workflow time `at`.
func (s *WorkflowSuite) queryAt(at time.Duration, fn func(BatchView)) {
	s.env.RegisterDelayedCallback(func() {
		v, err := s.env.QueryWorkflow(QueryStatus)
		s.Require().NoError(err)
		var view BatchView
		s.Require().NoError(v.Get(&view))
		fn(view)
	}, at)
}

func (s *WorkflowSuite) run(in BatchInput) BatchResult {
	s.env.ExecuteWorkflow(BulkWireBatch, in)
	s.Require().True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())
	var res BatchResult
	s.Require().NoError(s.env.GetWorkflowResult(&res))
	return res
}

func (s *WorkflowSuite) finalView() BatchView {
	v, err := s.env.QueryWorkflow(QueryStatus)
	s.Require().NoError(err)
	var view BatchView
	s.Require().NoError(v.Get(&view))
	return view
}

func (s *WorkflowSuite) insertedChunks() []string {
	var ids []string
	for _, in := range s.core.inserts {
		ids = append(ids, in.Chunk.ChunkID)
	}
	return ids
}

// W1: happy path draft -> authorize.
func (s *WorkflowSuite) TestW1HappyPath() {
	s.core.blocked["g01-c00"] = true
	s.draftAt(0)
	s.authorizeAt(2 * time.Hour)
	res := s.run(testInput())

	s.Equal(PhaseAuthorized, res.Phase)
	s.Equal(Totals{Rows: 751, Drafted: 750, Authorized: 750, Rejected: 1, Pending: 0, AmountCents: 1_500_000}, res.Totals)
	s.ElementsMatch([]string{"g00-c00", "g00-c01", "g00-c02", "g01-c00"}, s.insertedChunks(), "InsertDraftChunk once per chunk")
	s.Len(s.core.authorizes, 4, "AuthorizeChunk once per chunk with Drafted > 0")
	s.Equal([]string{"Drafted", "Authorizing", "Authorized"}, s.core.statuses, "SetGenTxBatchStatus in order")
	s.Equal([]string{"DRAFTING", "DRAFTED", "AUTHORIZING", "AUTHORIZED"}, s.saStatuses, "BulkWireStatus upserted on each phase")
	s.Empty(s.saAttn, "NeedsAttention never flips")

	view := s.finalView()
	s.Equal(PhaseAuthorized, view.Phase)
	s.Equal(int64(100000), view.GenTxBatchID)
	s.Equal(1, view.RejectionCount)
	s.Equal("BENEFICIARY_BLOCKED", view.Rejections[0].Code)
	s.Equal("••0033", view.Groups[0].OriginatingAccountMasked)
	s.Equal("APR-01JTEST", view.Authorization.ApprovalRef)
	for _, c := range view.Chunks {
		s.Equal(ChunkAuthorized, c.State)
		s.True(strings.HasPrefix(c.ActivityID, "authorize/"+c.ChunkID+"/r0"))
	}
	s.Contains(s.core.activityIDs, "draft/g00-c00/r0")
	s.Contains(s.core.activityIDs, "policy/g01/r0")
	types := timelineTypes(view)
	for _, ev := range []string{EvStarted, EvDraftRequested, EvPlanReady, EvGenTxBatchReady, EvDrafted, EvAwaitingAuthorization, EvAuthorizationReceived, EvAuthorizing, EvAuthorized} {
		s.Contains(types, ev)
	}
}

// W2: authorize arrives first (approve-before-draft): one execution drafts, then authorizes; no timer wait.
func (s *WorkflowSuite) TestW2AuthorizeBeforeDraft() {
	s.authorizeAt(0)
	res := s.run(testInput())
	s.Equal(PhaseAuthorized, res.Phase)
	s.Len(s.core.inserts, 4)
	s.Len(s.core.authorizes, 4)
	s.NotContains(timelineTypes(s.finalView()), EvAwaitingAuthorization, "no wait on the approval timer")
}

// W3: transient Core errors are retried in place by the RetryPolicy; nothing parks.
func (s *WorkflowSuite) TestW3TransientErrorsRetry() {
	s.core.transientFor["g00-c01"] = 2
	s.draftAt(0)
	s.authorizeAt(2 * time.Hour)
	res := s.run(testInput())
	s.Equal(PhaseAuthorized, res.Phase)
	s.Equal(3, count(s.insertedChunks(), "g00-c01"), "two failed attempts + one success")
	s.Empty(s.saAttn, "NeedsAttention never true")
	for _, id := range s.core.activityIDs {
		s.NotContains(id, "/r1", "retries are attempts of the same Activity, not new Activities")
	}
}

// W4: structural error parks the chunk; others complete; RETRY runs it again with Activity ID /r1.
func (s *WorkflowSuite) TestW4StructuralErrorThenRetry() {
	s.core.draftFail["g00-c01"] = errOnHold
	s.draftAt(0)
	s.queryAt(time.Hour, func(v BatchView) {
		c := chunkView(v, "g00-c01")
		s.Equal(ChunkNeedsAttention, c.State)
		s.Equal("ACCOUNT_ON_HOLD", c.Attention.Code)
		s.True(v.NeedsAttention)
		for _, other := range []string{"g00-c00", "g00-c02", "g01-c00"} {
			s.Equal(ChunkDrafted, chunkView(v, other).State, other)
		}
		s.core.mu.Lock()
		delete(s.core.draftFail, "g00-c01") // bank operations released the hold
		s.core.mu.Unlock()
	})
	s.resolveAt(time.Hour+time.Minute, "g00-c01", DecisionRetry)
	s.authorizeAt(3 * time.Hour)
	res := s.run(testInput())

	s.Equal(PhaseAuthorized, res.Phase)
	s.Equal(751, res.Totals.Authorized)
	s.Contains(s.core.activityIDs, "draft/g00-c01/r1")
	s.Equal([]bool{true, false}, s.saAttn, "NeedsAttention flips true while parked, then false")
	s.Contains(timelineTypes(s.finalView()), EvChunkParked)
	s.Contains(timelineTypes(s.finalView()), EvChunkResolved)
}

// W5: REJECT_CHUNK while drafting writes the chunk with ForceReject OPERATOR_REJECTED.
func (s *WorkflowSuite) TestW5RejectChunkWhileDrafting() {
	s.core.draftFail["g00-c01"] = errOnHold
	s.draftAt(0)
	s.resolveAt(time.Hour, "g00-c01", DecisionRejectChunk)
	s.authorizeAt(3 * time.Hour)
	res := s.run(testInput())

	s.Equal(PhaseAuthorized, res.Phase)
	last := s.core.inserts[len(s.core.inserts)-1]
	for _, in := range s.core.inserts {
		if in.Chunk.ChunkID == "g00-c01" && in.ForceReject != nil {
			last = in
		}
	}
	s.Require().NotNil(last.ForceReject)
	s.Equal(operatorRejectedCode, last.ForceReject.Code)
	s.Equal(ChunkSkipped, chunkView(s.finalView(), "g00-c01").State, "nothing to authorize")
	s.Equal(250, res.Totals.Rejected)
	s.Len(s.core.authorizes, 3, "rejected chunk is not authorized")
}

// W6: CANCEL_BATCH stops new chunks and closes Core's batch as Cancelled.
func (s *WorkflowSuite) TestW6CancelBatch() {
	in := testInput()
	in.Concurrency = 1
	s.core.draftFail["g00-c00"] = errOnHold
	s.core.slow = 0
	s.draftAt(0)
	s.resolveAt(time.Hour, "g00-c00", DecisionCancelBatch)
	res := s.run(in)

	s.Equal(PhaseCancelled, res.Phase)
	s.Equal("Cancelled", s.core.statuses[len(s.core.statuses)-1])
	s.NotContains(s.core.statuses, "Drafted")
	view := s.finalView()
	s.Equal(ChunkCancelled, chunkView(view, "g00-c00").State)
	s.Contains(timelineTypes(view), EvCancelled)
	s.Empty(s.core.authorizes)
}

// W7: an exhausted retry budget (ScheduleToClose) parks the chunk with RETRY_BUDGET_EXHAUSTED.
func (s *WorkflowSuite) TestW7RetryBudgetExhausted() {
	in := testInput()
	// What the service returns when the budget runs out: a ScheduleToClose timeout whose cause is the last failure.
	s.core.draftFail["g01-c00"] = temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_SCHEDULE_TO_CLOSE, errTransient)
	s.draftAt(0)
	s.queryAt(time.Hour, func(v BatchView) {
		c := chunkView(v, "g01-c00")
		s.Equal(ChunkNeedsAttention, c.State)
		s.Equal("RETRY_BUDGET_EXHAUSTED", c.Attention.Code)
		s.Equal("RETRY_BUDGET", c.Attention.Source)
		s.Contains(c.Attention.Message, "CORE_UNAVAILABLE")
		s.core.mu.Lock()
		delete(s.core.draftFail, "g01-c00") // the outage is over
		s.core.mu.Unlock()
	})
	s.resolveAt(time.Hour+time.Minute, "g01-c00", DecisionRetry)
	s.authorizeAt(3 * time.Hour)
	res := s.run(in)
	s.Equal(PhaseAuthorized, res.Phase)
	s.Contains(s.core.activityIDs, "draft/g01-c00/r1")
}

// W8: approval never arrives -> durable timer -> EXPIRED, and Core is told.
func (s *WorkflowSuite) TestW8ApprovalTimeout() {
	s.draftAt(0)
	res := s.run(testInput())
	s.Equal(PhaseExpired, res.Phase)
	s.Equal([]string{"Drafted", "Expired"}, s.core.statuses)
	s.Empty(s.core.authorizes)
}

// W9: deny while DRAFTED.
func (s *WorkflowSuite) TestW9DenyWhileDrafted() {
	s.draftAt(0)
	s.update(2*time.Hour, UpdateDeny, "deny-1", DenyRequest{DeniedBy: "alex", Reason: "duplicate file"})
	res := s.run(testInput())
	s.Equal(PhaseDenied, res.Phase)
	s.Equal([]string{"Drafted", "Denied"}, s.core.statuses)
	s.Equal("alex", s.finalView().Closure.By)
}

// W10: validators reject invalid requests without touching state; idempotent repeats are accepted.
func (s *WorkflowSuite) TestW10Validators() {
	s.core.authFail["g00-c00"] = errOnHold // keeps the batch in AUTHORIZING with a parked chunk
	s.draftAt(0)
	s.update(time.Minute, UpdateDraft, "draft-2", DraftRequest{RequestedBy: "jordan"}) // idempotent: accepted
	s.update(2*time.Minute, UpdateResolve, "resolve-not-parked", ResolveRequest{ChunkID: "g00-c00", Decision: DecisionRetry})
	s.authorizeAt(time.Hour)
	s.update(time.Hour+time.Minute, UpdateAuthorize, "APR-01JTEST", AuthorizeRequest{ApprovalRef: "APR-01JTEST"}) // same ref: accepted
	s.update(2*time.Hour, UpdateAuthorize, "APR-OTHER", AuthorizeRequest{ApprovalRef: "APR-OTHER"})
	s.update(2*time.Hour, UpdateCancel, "cancel-authorizing", CancelRequest{By: "jordan"})
	s.update(2*time.Hour, UpdateResolve, "cancel-batch-authorizing", ResolveRequest{ChunkID: "g00-c00", Decision: DecisionCancelBatch})
	s.update(2*time.Hour, UpdateResolve, "reject-batch-target", ResolveRequest{ChunkID: BatchTarget, Decision: DecisionRejectChunk})
	s.env.RegisterDelayedCallback(func() {
		s.core.mu.Lock()
		delete(s.core.authFail, "g00-c00")
		s.core.mu.Unlock()
	}, 3*time.Hour)
	s.resolveAt(3*time.Hour, "g00-c00", DecisionRetry)
	res := s.run(testInput())

	s.Equal(PhaseAuthorized, res.Phase)
	for _, id := range []string{"resolve-not-parked", "APR-OTHER", "cancel-authorizing", "cancel-batch-authorizing", "reject-batch-target"} {
		s.Error(s.rejected[id], id)
	}
	for _, id := range []string{"draft-2", "APR-01JTEST"} {
		s.NoError(s.rejected[id], id)
	}
	s.Equal("APR-01JTEST", s.finalView().Authorization.ApprovalRef)
}

// W10 (cont.): a draft Update after the batch is terminal is rejected.
func (s *WorkflowSuite) TestW10DraftAfterTerminalIsRejected() {
	s.update(0, UpdateCancel, "cancel-1", CancelRequest{By: "jordan"})
	s.env.RegisterDelayedCallback(func() {}, time.Minute)
	res := s.run(testInput())
	s.Equal(PhaseCancelled, res.Phase)
	s.Empty(s.core.statuses, "no Core batch existed, so Core is not called")
	// Once terminal, the workflow completes in the same task, so no Update can reach it through the test
	// environment; the validator itself is checked directly.
	s.Error((&batchState{phase: PhaseCancelled}).validateDraft(nil, DraftRequest{}))
}

// W11: group denial is a business outcome: the group's chunks are written as Rejected; nothing parks.
func (s *WorkflowSuite) TestW11GroupDenial() {
	s.core.deny["4401220033|2026-11-03"] = true
	s.draftAt(0)
	s.authorizeAt(2 * time.Hour)
	res := s.run(testInput())

	s.Equal(PhaseAuthorized, res.Phase)
	for _, in := range s.core.inserts {
		if in.Chunk.GroupKey == "4401220033|2026-11-03" {
			s.Require().NotNil(in.ForceReject, in.Chunk.ChunkID)
			s.Equal("DAILY_LIMIT_EXCEEDED", in.ForceReject.Code)
		} else {
			s.Nil(in.ForceReject)
		}
	}
	s.Equal(501, res.Totals.Rejected)
	s.Equal(250, res.Totals.Authorized)
	s.Empty(s.saAttn)
	view := s.finalView()
	s.Equal("DENY", view.Groups[0].Decision)
	s.Contains(timelineTypes(view), EvGroupDenied)
}

// W12: the semaphore bounds parallelism: never more than Concurrency chunk Activities at once.
func (s *WorkflowSuite) TestW12BoundedParallelism() {
	s.core.slow = 30 * time.Millisecond
	s.draftAt(0)
	s.authorizeAt(2 * time.Hour)
	s.run(testInput())
	s.Equal(2, s.core.maxInFlight)
}

// W13: with Concurrency=1, a parked chunk releases its slot, so the remaining chunks still run.
func (s *WorkflowSuite) TestW13ParkReleasesSlot() {
	in := testInput()
	in.Concurrency = 1
	s.core.draftFail["g00-c00"] = errOnHold
	s.draftAt(0)
	s.queryAt(time.Hour, func(v BatchView) {
		s.Equal(ChunkNeedsAttention, chunkView(v, "g00-c00").State)
		for _, id := range []string{"g00-c01", "g00-c02", "g01-c00"} {
			s.Equal(ChunkDrafted, chunkView(v, id).State, id)
		}
	})
	s.resolveAt(2*time.Hour, "g00-c00", DecisionRejectChunk)
	s.authorizeAt(3 * time.Hour)
	s.Equal(PhaseAuthorized, s.run(in).Phase)
}

// W14: every chunk rejected -> REJECTED, and Core's batch is closed as Rejected.
func (s *WorkflowSuite) TestW14AllRejected() {
	s.core.deny["4401220033|2026-11-03"] = true
	s.core.deny["4401220017|2026-11-03"] = true
	s.draftAt(0)
	res := s.run(testInput())
	s.Equal(PhaseRejected, res.Phase)
	s.Equal([]string{"Rejected"}, s.core.statuses)
	s.Equal(751, res.Totals.Rejected)
}

// W15: a hold placed after drafting parks the chunk in AUTHORIZING; only RETRY/REJECT_CHUNK are accepted.
func (s *WorkflowSuite) TestW15AuthorizePhaseHold() {
	s.core.authFail["g01-c00"] = errOnHold
	s.draftAt(0)
	s.authorizeAt(time.Hour)
	s.queryAt(2*time.Hour, func(v BatchView) {
		c := chunkView(v, "g01-c00")
		s.Equal(ChunkNeedsAttention, c.State)
		s.Equal(PhaseAuthorizing, c.Attention.Phase)
		s.Equal([]string{DecisionRetry, DecisionRejectChunk}, c.Attention.AllowedDecisions)
	})
	s.update(2*time.Hour+time.Minute, UpdateResolve, "cancel-in-authorize", ResolveRequest{ChunkID: "g01-c00", Decision: DecisionCancelBatch, By: "morgan"})
	s.resolveAt(2*time.Hour+2*time.Minute, "g01-c00", DecisionRejectChunk)
	res := s.run(testInput())

	s.Error(s.rejected["cancel-in-authorize"])
	s.Equal(PhaseAuthorized, res.Phase)
	s.Require().Len(s.core.rejects, 1)
	s.Equal(operatorRejectedCode, s.core.rejects[0].Code)
	s.Contains(s.core.activityIDs, "reject/g01-c00/r1")
	s.Equal(500+1, res.Totals.Authorized)
	s.Equal(250, res.Totals.Rejected)
}

// Parked batch-level steps (e.g. EnsureGenTxBatch hitting a closed batch) accept RETRY and, while drafting,
// CANCEL_BATCH; never REJECT_CHUNK.
func (s *WorkflowSuite) TestBatchStepParksAndResumes() {
	env := s.NewTestWorkflowEnvironment()
	s.env = env
	env.OnUpsertTypedSearchAttributes(mock.Anything).Return(nil)
	fail := true
	env.OnActivity(a.PlanChunks, mock.Anything, mock.Anything).Return(
		func(context.Context, activities.PlanInput) (activities.Plan, error) {
			if fail {
				return activities.Plan{}, temporal.NewNonRetryableApplicationError("staged data not found", model.ErrTypeClaimCheckNotFound, errors.New("gone"))
			}
			return testPlan, nil
		})
	env.OnActivity(a.EnsureGenTxBatch, mock.Anything, mock.Anything).Return(int64(1), nil)
	env.OnActivity(a.CheckGroupPolicy, mock.Anything, mock.Anything).Return(activities.PolicyDecision{Decision: activities.DecisionAllow}, nil)
	env.OnActivity(a.InsertDraftChunk, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in activities.DraftChunkInput) (activities.ChunkDraftResult, error) {
			return activities.ChunkDraftResult{ChunkDrafted: in.Chunk.Length}, nil
		})
	env.OnActivity(a.SetGenTxBatchStatus, mock.Anything, mock.Anything).Return(activities.GenTxBatchStatus{}, nil)
	s.draftAt(0)
	s.queryAt(time.Hour, func(v BatchView) {
		s.Require().NotNil(v.BatchAttention)
		s.Equal("CLAIM_CHECK_NOT_FOUND", v.BatchAttention.Code)
		s.Equal([]string{DecisionRetry, DecisionCancelBatch}, v.BatchAttention.AllowedDecisions)
		fail = false
	})
	s.update(time.Hour+time.Minute, UpdateResolve, "reject-batch", ResolveRequest{ChunkID: BatchTarget, Decision: DecisionRejectChunk})
	s.resolveAt(time.Hour+2*time.Minute, BatchTarget, DecisionRetry)
	s.update(2*time.Hour, UpdateCancel, "cancel", CancelRequest{By: "jordan", Reason: "wrong file"})
	res := s.run(testInput())
	s.Error(s.rejected["reject-batch"])
	s.Equal(PhaseCancelled, res.Phase)
}

func chunkView(v BatchView, id string) ChunkView {
	for _, c := range v.Chunks {
		if c.ChunkID == id {
			return c
		}
	}
	panic(fmt.Sprintf("chunk %s not in view", id))
}

func timelineTypes(v BatchView) []string {
	var t []string
	for _, e := range v.Timeline {
		t = append(t, e.Type)
	}
	return t
}

func count(xs []string, x string) int {
	n := 0
	for _, v := range xs {
		if v == x {
			n++
		}
	}
	return n
}
