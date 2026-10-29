// Activity inputs and results. Inputs are REFERENCES (batch ID, chunk descriptors, Core batch id), never wire
// rows; results are counts plus short rejection lists. Both are recorded in workflow history (encrypted by the
// codec), so keeping them small is what keeps a 10,000-wire batch's history in the tens of KB.
package activities

import "bulkwire/internal/model"

// Actor is how the workflow identifies itself in Core's audit rows. Humans (approvers, operators) appear in
// approval references and reason/detail fields.
const Actor = "bulkwire-workflow"

// MaxRejectionsPerResult caps the rejection list in one chunk result. The exact count is always returned
// (ChunkRejected); Core remains the complete record ("View all in bank records").
const MaxRejectionsPerResult = 50

// PlanInput -> Plan.
type PlanInput struct {
	FiID      string
	BatchID   string
	ChunkSize int
}

// Plan is the chunk plan: a few dozen small descriptors, safe to keep in history.
type Plan struct {
	Groups []model.GroupInfo
	Chunks []model.ChunkRef
}

// EnsureInput -> GenTxBatchID (int64).
type EnsureInput struct {
	FiID        string
	BatchID     string
	CustomerID  string
	InitiatedBy string
	RowCount    int
	AmountCents int64
}

// PolicyInput -> PolicyDecision.
type PolicyInput struct {
	FiID         string
	GenTxBatchID int64
	Group        model.GroupInfo
}

// Policy decisions.
const (
	DecisionAllow = "ALLOW"
	DecisionDeny  = "DENY"
)

// PolicyDecision is Core's limit decision for one group. A DENY is a result, not an error.
type PolicyDecision struct {
	Decision       string // ALLOW | DENY
	Code           string // "" | DAILY_LIMIT_EXCEEDED
	Message        string // human-readable, e.g. "Daily limit exceeded: $16.5M requested, $15.0M limit"
	LimitCents     int64
	UsedCents      int64
	RequestedCents int64
}

// ForceReject writes a chunk's wires as Rejected with this code (group denial or operator decision).
type ForceReject struct {
	Code    string
	Message string
}

// DraftChunkInput references one chunk; the Activity reads its rows from the claim check.
type DraftChunkInput struct {
	FiID         string
	BatchID      string
	GenTxBatchID int64
	Chunk        model.ChunkRef
	ForceReject  *ForceReject
	Actor        string
}

// ChunkDraftResult carries CHUNK-level totals from Core (correct even when a previous attempt committed and
// its response was lost).
type ChunkDraftResult struct {
	ChunkDrafted     int
	ChunkRejected    int
	InsertedThisCall int
	AlreadyPresent   int
	Rejected         []model.Rejection // at most MaxRejectionsPerResult
}

// AuthorizeChunkInput -> ChunkAuthorizeResult.
type AuthorizeChunkInput struct {
	FiID         string
	GenTxBatchID int64
	ChunkID      string
	ApprovalRef  string
	Actor        string
}

// RejectChunkInput -> ChunkAuthorizeResult (operator REJECT_CHUNK in the authorize phase).
type RejectChunkInput struct {
	FiID         string
	GenTxBatchID int64
	ChunkID      string
	Code         string
	Message      string
	Actor        string
}

// ChunkAuthorizeResult carries chunk-level totals.
type ChunkAuthorizeResult struct {
	ChunkAuthorized int
	ChunkRejected   int
	FlippedThisCall int
}

// Core batch statuses (dbo.GenTxBatches.Status).
const (
	GenTxDrafted     = "Drafted"
	GenTxAuthorizing = "Authorizing"
	GenTxAuthorized  = "Authorized"
	GenTxExpired     = "Expired"
	GenTxDenied      = "Denied"
	GenTxCancelled   = "Cancelled"
	GenTxRejected    = "Rejected"
)

// StatusInput -> GenTxBatchStatus.
type StatusInput struct {
	FiID         string
	GenTxBatchID int64
	Status       string
	Reason       string
	Actor        string
}

// GenTxBatchStatus is Core's batch header after the transition, with wire counts by status.
type GenTxBatchStatus struct {
	Status     string
	Drafted    int
	Authorized int
	Rejected   int
	Expired    int
	Denied     int
	Cancelled  int
}
