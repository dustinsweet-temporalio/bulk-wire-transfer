// Types of the BulkWireBatch workflow: its input, the Update requests it accepts, and the BatchView its
// `status` Query returns. bulkwire-service imports these to start the workflow and to serve the portal.
package workflow

import (
	"time"

	"bulkwire/internal/model"
)

// Names of the workflow type, its Query, and its Updates. These strings appear in history and in callers.
const (
	WorkflowName = "BulkWireBatch"

	QueryStatus = "status"

	UpdateDraft     = "draft"
	UpdateAuthorize = "authorize"
	UpdateDeny      = "deny"
	UpdateCancel    = "cancel"
	UpdateResolve   = "resolve"
)

// BatchInput is everything the workflow needs, fixed at start.
type BatchInput struct {
	BatchID          string // ULID from staging
	FiID             string // PATTERN: Tenant Context in Input — Activities resolve Core by FiID from worker config
	CustomerID       string
	InitiatedBy      string // uploader userId
	RowCount         int    // valid rows
	TotalAmountCents int64
	ChunkSize        int           // 50..1000, default 250
	Concurrency      int           // 1..8, default 2
	ApprovalWindow   time.Duration // default 24h; the deadline starts when the batch reaches DRAFTED
	RetryBudget      time.Duration // ScheduleToClose for each Core Activity; default 30m
	RetryMaxInterval time.Duration // default 20s in the demo .env, 1–2 min in production guidance

	// Staging facts, so the Event History starts with the complete story of the file.
	FileName   string
	FileSHA256 string
	UploadedAt time.Time
	GroupCount int
}

// WHY the Core URL is not in the input: endpoints are deployment configuration; putting them in history would
// pin running workflows to stale URLs. The input carries WHICH tenant; the worker config says WHERE it is.

// Phase is the batch's lifecycle phase (also the BulkWireStatus Search Attribute).
type Phase string

const (
	PhaseDrafting    Phase = "DRAFTING"
	PhaseDrafted     Phase = "DRAFTED"
	PhaseAuthorizing Phase = "AUTHORIZING"
	PhaseAuthorized  Phase = "AUTHORIZED"
	PhaseExpired     Phase = "EXPIRED"
	PhaseDenied      Phase = "DENIED"
	PhaseCancelled   Phase = "CANCELLED"
	PhaseRejected    Phase = "REJECTED"
)

// Terminal reports whether the batch is closed.
func (p Phase) Terminal() bool {
	switch p {
	case PhaseAuthorized, PhaseExpired, PhaseDenied, PhaseCancelled, PhaseRejected:
		return true
	}
	return false
}

// ChunkState is one chunk's state in the current phase.
type ChunkState string

const (
	ChunkPending        ChunkState = "PENDING"
	ChunkRunning        ChunkState = "RUNNING"
	ChunkDrafted        ChunkState = "DRAFTED"
	ChunkRejected       ChunkState = "REJECTED"
	ChunkNeedsAttention ChunkState = "NEEDS_ATTENTION"
	ChunkAuthorized     ChunkState = "AUTHORIZED"
	ChunkCancelled      ChunkState = "CANCELLED"
	ChunkSkipped        ChunkState = "SKIPPED" // nothing to do in the authorize phase: every wire was rejected
)

// ---- Update requests ----------------------------------------------------------------------------------------

// DraftRequest: the initiator (or an approver) asked to draft the batch into Core.
type DraftRequest struct {
	RequestedBy string `json:"requestedBy"`
}

// Approver is one signature Core collected.
type Approver struct {
	UserID      string    `json:"userId"`
	DisplayName string    `json:"displayName"`
	SignedAt    time.Time `json:"signedAt"`
}

// AuthorizeRequest: Core fully approved the batch (determination, MFA/TAC, dual control all happened in Core).
type AuthorizeRequest struct {
	ApprovalRef string     `json:"approvalRef"`
	Approvers   []Approver `json:"approvers"`
	ApprovedAt  time.Time  `json:"approvedAt"`
}

// DenyRequest: Core recorded a denial.
type DenyRequest struct {
	DeniedBy string `json:"deniedBy"`
	Reason   string `json:"reason"`
}

// CancelRequest: the initiator cancelled the batch.
type CancelRequest struct {
	By     string `json:"by"`
	Reason string `json:"reason"`
}

// Operator decisions for a parked item.
const (
	DecisionRetry       = "RETRY"
	DecisionRejectChunk = "REJECT_CHUNK"
	DecisionCancelBatch = "CANCEL_BATCH"
)

// BatchTarget is the resolve target for a parked batch-level step (plan, Core batch, policy, status).
const BatchTarget = "_batch"

// ResolveRequest: bank operations' decision for a parked chunk (or for "_batch").
type ResolveRequest struct {
	ChunkID  string `json:"chunkId"`
	Decision string `json:"decision"`
	By       string `json:"by"`
	Note     string `json:"note"`
}

// ---- Result --------------------------------------------------------------------------------------------------

// Totals are always recomputed from chunk fields (no separate counters that could drift).
type Totals struct {
	Rows        int   `json:"rows"`
	Drafted     int   `json:"drafted"`
	Authorized  int   `json:"authorized"`
	Rejected    int   `json:"rejected"`
	Pending     int   `json:"pending"`
	AmountCents int64 `json:"amountCents"`
}

// BatchResult is the workflow's result. Every closed outcome is a successful completion: EXPIRED, DENIED,
// CANCELLED, and REJECTED are business outcomes, not workflow failures.
type BatchResult struct {
	Phase        Phase  `json:"phase"`
	GenTxBatchID int64  `json:"genTxBatchId"`
	Totals       Totals `json:"totals"`
}

// ---- Query view (the JSON the portal renders) -------------------------------------------------------------

// Attention describes a parked item.
type Attention struct {
	Code             string    `json:"code"`    // ACCOUNT_ON_HOLD, RETRY_BUDGET_EXHAUSTED, CLAIM_CHECK_NOT_FOUND, CORE_CONTRACT, ...
	Message          string    `json:"message"` // masked, human-readable
	Phase            Phase     `json:"phase"`   // DRAFTING or AUTHORIZING
	Since            time.Time `json:"since"`   // workflow.Now
	Source           string    `json:"source"`  // Core | CLAIM_CHECK | RETRY_BUDGET
	Step             string    `json:"step,omitempty"`
	AllowedDecisions []string  `json:"allowedDecisions"`
}

// BatchView is the `status` Query result: the single source of truth for the portal's live view.
type BatchView struct {
	BatchID        string            `json:"batchId"`
	FiID           string            `json:"fiId"`
	CustomerID     string            `json:"customerId"`
	InitiatedBy    string            `json:"initiatedBy"`
	FileName       string            `json:"fileName"`
	GenTxBatchID   int64             `json:"genTxBatchId"`
	Phase          Phase             `json:"phase"`
	NeedsAttention bool              `json:"needsAttention"`
	Settings       SettingsView      `json:"settings"`
	Totals         Totals            `json:"totals"`
	Groups         []GroupView       `json:"groups"`
	Chunks         []ChunkView       `json:"chunks"`
	BatchAttention *Attention        `json:"batchAttention"`
	Rejections     []model.Rejection `json:"rejections"`
	RejectionCount int               `json:"rejectionCount"`
	Draft          *DraftView        `json:"draft"`
	Authorization  *AuthorizeRequest `json:"authorization"`
	Closure        *ClosureView      `json:"closure"`
	AuthorizeBy    *time.Time        `json:"authorizeBy"`
	Timeline       []TimelineEvent   `json:"timeline"`
}

type SettingsView struct {
	ChunkSize          int   `json:"chunkSize"`
	Concurrency        int   `json:"concurrency"`
	RetryBudgetSeconds int64 `json:"retryBudgetSeconds"`
	ApprovalWindowSecs int64 `json:"approvalWindowSeconds"`
}

type GroupView struct {
	GroupKey                 string  `json:"groupKey"`
	OriginatingAccountMasked string  `json:"originatingAccountMasked"`
	ProcessDate              string  `json:"processDate"`
	RowCount                 int     `json:"rowCount"`
	TotalAmountCents         int64   `json:"totalAmountCents"`
	Decision                 string  `json:"decision"` // ALLOW | DENY | PENDING
	Code                     *string `json:"code"`
	Message                  *string `json:"message"`
}

type ChunkView struct {
	ChunkID                  string     `json:"chunkId"`
	GroupKey                 string     `json:"groupKey"`
	OriginatingAccountMasked string     `json:"originatingAccountMasked"`
	Length                   int        `json:"length"`
	State                    ChunkState `json:"state"`
	Cycle                    int        `json:"cycle"`
	ActivityID               string     `json:"activityId"`
	Drafted                  int        `json:"drafted"`
	Rejected                 int        `json:"rejected"`
	Authorized               int        `json:"authorized"`
	Attention                *Attention `json:"attention"`
}

type DraftView struct {
	RequestedBy string    `json:"requestedBy"`
	RequestedAt time.Time `json:"requestedAt"`
}

type ClosureView struct {
	Phase  Phase  `json:"phase"`
	By     string `json:"by"`
	Reason string `json:"reason"`
}

// TimelineEvent is one entry in the batch's human-readable timeline (masked data only).
type TimelineEvent struct {
	At     time.Time `json:"at"`
	Type   string    `json:"type"`
	Detail string    `json:"detail"`
}

// Timeline event types.
const (
	EvStarted               = "STARTED"
	EvDraftRequested        = "DRAFT_REQUESTED"
	EvAuthorizationReceived = "AUTHORIZATION_RECEIVED"
	EvPlanReady             = "PLAN_READY"
	EvGenTxBatchReady       = "GENTX_BATCH_READY"
	EvGroupDenied           = "GROUP_DENIED"
	EvChunkParked           = "CHUNK_PARKED"
	EvChunkResolved         = "CHUNK_RESOLVED"
	EvDrafted               = "DRAFTED"
	EvAwaitingAuthorization = "AWAITING_AUTHORIZATION"
	EvNoInstruction         = "NO_INSTRUCTION"
	EvAuthorizing           = "AUTHORIZING"
	EvAuthorized            = "AUTHORIZED"
	EvExpired               = "EXPIRED"
	EvDenied                = "DENIED"
	EvCancelRequested       = "CANCEL_REQUESTED"
	EvCancelled             = "CANCELLED"
	EvRejected              = "REJECTED"
)
