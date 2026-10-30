// batchState: the workflow's in-memory state. It is rebuilt identically on every replay (it is only ever
// changed by workflow code reacting to history), and it is the single source of truth for the portal's live
// view through the `status` Query. Nothing here is stored anywhere else: no Redis, no projection table.
package workflow

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/workflow"

	"bulkwire/internal/activities"
	"bulkwire/internal/model"
	"bulkwire/internal/temporalx"
)

const (
	maxTimeline   = 100 // timeline entries kept (oldest dropped first)
	maxRejections = 200 // rejection rows shown in the view; rejectionCount stays exact
)

type chunk struct {
	Ref        model.ChunkRef
	State      ChunkState
	Cycle      int    // increments on each operator RETRY or REJECT_CHUNK; part of the Activity ID
	ActivityID string // current/last Activity ID (lets the API map Describe -> chunk)
	Drafted    int    // wires accepted for payment (Drafted, later Authorized)
	Rejected   int
	Authorized int
	Rejections []model.Rejection // this chunk's rejections from its latest result (capped by the Activity)
	Attention  *Attention        // non-nil while parked
	Resolution *ResolveRequest   // set by the resolve Update; consumed by the park loop
}

type groupDecision struct {
	Info     model.GroupInfo
	Decision string // ALLOW | DENY | PENDING
	Code     string
	Message  string
}

type closeRequest struct {
	Phase  Phase // EXPIRED | DENIED | CANCELLED
	By     string
	Reason string
}

type settings struct {
	ChunkSize        int
	Concurrency      int
	RetryBudget      time.Duration
	RetryMaxInterval time.Duration
	ApprovalWindow   time.Duration
	CoreTaskQueue    string
	CoreFairnessKey  string
}

type batchState struct {
	in       BatchInput
	settings settings
	phase    Phase

	genTxBatchID int64
	groups       []*groupDecision // plan order
	chunks       []*chunk         // plan order. DETERMINISM: slices, never map iteration, decide command order

	draft         *DraftView
	authorization *AuthorizeRequest
	closeRequest  *closeRequest
	authorizeBy   time.Time
	timeline      []TimelineEvent

	// A parked batch-level step (plan, Core batch, policy check, status). One at a time: batchParkSlot
	// serializes concurrent policy-check failures so `_batch` always names exactly one item.
	batchAttention  *Attention
	batchResolution *ResolveRequest
	batchParkSlot   workflow.Semaphore

	parked int   // items currently parked; drives the NeedsAttention Search Attribute
	fatal  error // only cancellation-type errors (see runChunks)
}

func newBatchState(ctx workflow.Context, in BatchInput) *batchState {
	s := &batchState{
		in:    in,
		phase: PhaseDrafting,
		settings: settings{
			ChunkSize: in.ChunkSize, Concurrency: in.Concurrency, RetryBudget: in.RetryBudget,
			RetryMaxInterval: in.RetryMaxInterval, ApprovalWindow: in.ApprovalWindow,
			CoreTaskQueue: in.CoreTaskQueue, CoreFairnessKey: in.CoreFairnessKey,
		},
		batchParkSlot: workflow.NewSemaphore(ctx, 1),
	}
	s.addTimeline(ctx, EvStarted, fmt.Sprintf("%s · %d wires · %s", in.FileName, in.RowCount, dollars(in.TotalAmountCents)))
	return s
}

// setPhase records a phase change everywhere it is visible: state, the BulkWireStatus Search Attribute
// (lists, approvals queue), the timeline, and the Web UI's current details.
// Search Attribute updates are eventually consistent (seconds); they power lists, not the live progress
// view, which comes from the Query.
func (s *batchState) setPhase(ctx workflow.Context, p Phase) {
	s.phase = p
	if err := workflow.UpsertTypedSearchAttributes(ctx, temporalx.SABulkWireStatus.ValueSet(string(p))); err != nil {
		workflow.GetLogger(ctx).Error("upsert BulkWireStatus failed", "error", err)
	}
	if ev := phaseEvent(p); ev != "" { // DRAFTING is the initial phase; its story is DRAFT_REQUESTED/PLAN_READY
		s.addTimeline(ctx, ev, phaseDetail(s, p))
	}
	s.publishDetails(ctx)
}

func phaseEvent(p Phase) string {
	switch p {
	case PhaseDrafted:
		return EvDrafted
	case PhaseAuthorizing:
		return EvAuthorizing
	case PhaseAuthorized:
		return EvAuthorized
	case PhaseExpired:
		return EvExpired
	case PhaseDenied:
		return EvDenied
	case PhaseCancelled:
		return EvCancelled
	case PhaseRejected:
		return EvRejected
	}
	return ""
}

func phaseDetail(s *batchState, p Phase) string {
	t := s.totals()
	switch p {
	case PhaseDrafted:
		return fmt.Sprintf("%d drafted, %d rejected", t.Drafted, t.Rejected)
	case PhaseAuthorized:
		return fmt.Sprintf("%d authorized, %d rejected", t.Authorized, t.Rejected)
	case PhaseDenied, PhaseCancelled, PhaseExpired:
		if s.closeRequest != nil && s.closeRequest.By != "" {
			return strings.TrimSpace(fmt.Sprintf("by %s %s", s.closeRequest.By, s.closeRequest.Reason))
		}
	case PhaseRejected:
		return fmt.Sprintf("all %d wires rejected", t.Rejected)
	}
	return ""
}

func (s *batchState) addTimeline(ctx workflow.Context, typ, detail string) {
	s.timeline = append(s.timeline, TimelineEvent{At: workflow.Now(ctx), Type: typ, Detail: detail})
	if len(s.timeline) > maxTimeline {
		s.timeline = s.timeline[len(s.timeline)-maxTimeline:]
	}
}

// setParked moves the parked-item count and flips the NeedsAttention Search Attribute only on the 0 -> 1
// and 1 -> 0 transitions (the bank operations exception queue lists batches by it).
func (s *batchState) setParked(ctx workflow.Context, delta int) {
	before := s.parked
	s.parked += delta
	if (before == 0) != (s.parked == 0) {
		if err := workflow.UpsertTypedSearchAttributes(ctx, temporalx.SANeedsAttention.ValueSet(s.parked > 0)); err != nil {
			workflow.GetLogger(ctx).Error("upsert NeedsAttention failed", "error", err)
		}
	}
}

// totals are recomputed from chunk fields every time (no separate counters to drift).
func (s *batchState) totals() Totals {
	t := Totals{Rows: s.in.RowCount, AmountCents: s.in.TotalAmountCents}
	for _, c := range s.chunks {
		t.Drafted += c.Drafted
		t.Rejected += c.Rejected
		t.Authorized += c.Authorized
	}
	t.Pending = max(t.Rows-t.Drafted-t.Rejected, 0)
	return t
}

func (s *batchState) group(key string) *groupDecision {
	for _, g := range s.groups {
		if g.Info.GroupKey == key {
			return g
		}
	}
	return nil
}

func (s *batchState) chunkByID(id string) *chunk {
	for _, c := range s.chunks {
		if c.Ref.ChunkID == id {
			return c
		}
	}
	return nil
}

func (s *batchState) result() BatchResult {
	return BatchResult{Phase: s.phase, GenTxBatchID: s.genTxBatchID, Totals: s.totals()}
}

// publishDetails sets the Web UI's "current details" for this execution (Workflow User Metadata), so the
// presenter can read progress in the Temporal UI itself. Masked data only.
// PATTERN: Workflow User Metadata — https://docs.temporal.io/workflow-execution
func (s *batchState) publishDetails(ctx workflow.Context) {
	t := s.totals()
	var b strings.Builder
	fmt.Fprintf(&b, "**Phase:** %s\n\n", s.phase)
	fmt.Fprintf(&b, "| Wires | Drafted | Authorized | Rejected | Pending |\n|---|---|---|---|---|\n| %d | %d | %d | %d | %d |\n",
		t.Rows, t.Drafted, t.Authorized, t.Rejected, t.Pending)
	if s.genTxBatchID != 0 {
		fmt.Fprintf(&b, "\nCore batch **%d**", s.genTxBatchID)
	}
	if s.phase == PhaseDrafted && !s.authorizeBy.IsZero() {
		fmt.Fprintf(&b, " · awaiting Core approval until %s", s.authorizeBy.UTC().Format(time.RFC3339))
	}
	if s.batchAttention != nil {
		fmt.Fprintf(&b, "\n\n**Needs attention:** batch step %s: %s", s.batchAttention.Step, s.batchAttention.Code)
	}
	for _, c := range s.chunks {
		if c.Attention != nil {
			fmt.Fprintf(&b, "\n\n**Needs attention:** chunk %s: %s (%s)", c.Ref.ChunkID, c.Attention.Code, s.maskedAccountOf(c))
		}
	}
	workflow.SetCurrentDetails(ctx, b.String())
}

func maskedGroup(g *groupDecision) string { return model.MaskAccount(g.Info.OriginatingAccount) }

func (s *batchState) maskedAccountOf(c *chunk) string {
	if g := s.group(c.Ref.GroupKey); g != nil {
		return model.MaskAccount(g.Info.OriginatingAccount)
	}
	return ""
}

// view builds the Query response. It must not mutate state: Queries run between workflow tasks and their
// results are never recorded in history.
func (s *batchState) view() BatchView {
	v := BatchView{
		BatchID: s.in.BatchID, FiID: s.in.FiID, CustomerID: s.in.CustomerID, InitiatedBy: s.in.InitiatedBy,
		FileName: s.in.FileName, GenTxBatchID: s.genTxBatchID, Phase: s.phase, NeedsAttention: s.parked > 0,
		Settings: SettingsView{
			ChunkSize: s.settings.ChunkSize, Concurrency: s.settings.Concurrency,
			RetryBudgetSeconds: int64(s.settings.RetryBudget / time.Second),
			ApprovalWindowSecs: int64(s.settings.ApprovalWindow / time.Second),
		},
		Totals:         s.totals(),
		Groups:         []GroupView{},
		Chunks:         []ChunkView{},
		BatchAttention: s.batchAttention,
		Rejections:     []model.Rejection{},
		Draft:          s.draft,
		Authorization:  s.authorization,
		Timeline:       append([]TimelineEvent(nil), s.timeline...),
	}
	v.RejectionCount = v.Totals.Rejected
	for _, g := range s.groups {
		gv := GroupView{GroupKey: g.Info.GroupKey, OriginatingAccountMasked: model.MaskAccount(g.Info.OriginatingAccount),
			ProcessDate: g.Info.ProcessDate, RowCount: g.Info.RowCount, TotalAmountCents: g.Info.TotalAmountCents, Decision: g.Decision}
		if g.Code != "" {
			code, msg := g.Code, g.Message
			gv.Code, gv.Message = &code, &msg
		}
		v.Groups = append(v.Groups, gv)
	}
	for _, c := range s.chunks {
		v.Chunks = append(v.Chunks, ChunkView{ChunkID: c.Ref.ChunkID, GroupKey: c.Ref.GroupKey,
			OriginatingAccountMasked: s.maskedAccountOf(c), Length: c.Ref.Length, State: c.State, Cycle: c.Cycle,
			ActivityID: c.ActivityID, Drafted: c.Drafted, Rejected: c.Rejected, Authorized: c.Authorized, Attention: c.Attention})
		for _, r := range c.Rejections {
			if len(v.Rejections) < maxRejections {
				v.Rejections = append(v.Rejections, r)
			}
		}
	}
	if s.closeRequest != nil {
		v.Closure = &ClosureView{Phase: s.closeRequest.Phase, By: s.closeRequest.By, Reason: s.closeRequest.Reason}
	}
	if !s.authorizeBy.IsZero() {
		by := s.authorizeBy
		v.AuthorizeBy = &by
	}
	return v
}

// dollars renders cents as "$13,245,118.42" (integer arithmetic).
func dollars(cents int64) string { return model.FormatDollars(cents) }

// Status names in Core for each closing phase.
var coreStatusFor = map[Phase]string{
	PhaseExpired:   activities.GenTxExpired,
	PhaseDenied:    activities.GenTxDenied,
	PhaseCancelled: activities.GenTxCancelled,
	PhaseRejected:  activities.GenTxRejected,
}
