// Package activities holds every Activity of the BulkWireBatch workflow. All I/O in the system's
// orchestration lives here; the workflow package performs none.
//
// Rules every Activity follows:
//   - PATTERN: Tenant Context in Input. Every input carries FiID; coreclient maps it to THAT FI's Core
//     (CORE_ENDPOINTS) and sends X-Internal-Token. An unknown FiID is a non-retryable CoreContractError. Because
//     each FI has its own Core, an outage at one FI's Core only slows that FI's batches.
//   - PATTERN: Idempotent Activity (https://temporal.io/blog/idempotency-and-durable-execution). Every
//     Activity is safe to execute more than once: Core procedures are idempotent by ExternalBatchId /
//     ExternalWireId / ChunkId, and claim-check reads are pure.
//   - PATTERN: Claim Check (https://docs.temporal.io/troubleshooting/blob-size-limit-error). Inputs are
//     references; outputs are counts and short lists. No Activity returns rows.
//   - PATTERN: Retry Alerting via Metrics (https://docs.temporal.io/design-patterns/retry-metrics). Every
//     Core-calling Activity counts attempts past CORE_RETRY_ALERT_THRESHOLD (see alertOnRetries).
//   - Every HTTP call uses the Activity context, whose deadline is the StartToClose timeout (60 s), so a hung
//     Core call is abandoned exactly when Temporal gives up on the attempt.
//   - No heartbeats. WHY: each call is one sub-minute, set-based Core operation; StartToClose (60 s) already
//     detects a hung attempt, and heartbeats would add cost without information. Consequence worth knowing:
//     an operator's Activity Pause (https://docs.temporal.io/activity-operations) takes effect only after
//     the current attempt ends, because pause is delivered through heartbeats.
//   - Log with activity.GetLogger; never log account numbers or beneficiary data.
package activities

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"bulkwire/internal/claimcheck"
	"bulkwire/internal/coreclient"
	"bulkwire/internal/model"
	"bulkwire/internal/staging"
)

// Activities holds the dependencies; register with w.RegisterActivity(acts). Metrics come from
// activity.GetMetricsHandler(ctx) at call time, so no handler is stored here.
type Activities struct {
	Core                  *coreclient.Client
	ClaimCheck            claimcheck.Store
	AlertAttemptThreshold int // CORE_RETRY_ALERT_THRESHOLD
}

// MetricCoreAttemptsOverThreshold counts Core Activity attempts past the alert threshold, tagged by activity and FI.
const MetricCoreAttemptsOverThreshold = "bulkwire_core_attempts_over_threshold"

// alertOnRetries runs at the top of every Core-calling Activity. Retries are invisible to workflow code by
// design, so this metric is how on-call learns about a stuck Core long before the retry budget runs out.
// PRODUCTION NOTE: alert on this counter's rate (and on temporal_activity_schedule_to_start_latency).
func (a *Activities) alertOnRetries(ctx context.Context, fiID string) {
	info := activity.GetInfo(ctx)
	recordAttempt(activity.GetMetricsHandler(ctx), info.ActivityType.Name, fiID, int(info.Attempt), a.AlertAttemptThreshold)
}

func recordAttempt(h client.MetricsHandler, activityType, fiID string, attempt, threshold int) {
	if attempt > threshold {
		h.WithTags(map[string]string{"activity": activityType, "fi": fiID}).Counter(MetricCoreAttemptsOverThreshold).Inc(1)
	}
}

// PlanChunks reads the staging manifest and plans chunks with the pure planner. Reading only the manifest
// (group offsets and counts, never rows) is what makes this cheap.
func (a *Activities) PlanChunks(ctx context.Context, in PlanInput) (Plan, error) {
	m, err := a.readManifest(ctx, in.FiID, in.BatchID)
	if err != nil {
		return Plan{}, err
	}
	chunks, err := staging.Plan(m, in.ChunkSize)
	if err != nil {
		return Plan{}, temporal.NewNonRetryableApplicationError(err.Error(), model.ErrTypeStagingInvalid, nil)
	}
	activity.GetLogger(ctx).Info("planned chunks", "batchId", in.BatchID, "groups", len(m.Groups), "chunks", len(chunks))
	return Plan{Groups: m.Groups, Chunks: chunks}, nil
}

// EnsureGenTxBatch creates (or finds) Core's batch header and returns its GenTxBatchId. Idempotent by batch ID.
func (a *Activities) EnsureGenTxBatch(ctx context.Context, in EnsureInput) (int64, error) {
	a.alertOnRetries(ctx, in.FiID)
	res, err := a.Core.EnsureGenTxBatch(ctx, in.FiID, coreclient.EnsureGenTxBatchRequest{
		ExternalBatchID: in.BatchID, CustomerID: in.CustomerID, InitiatedBy: in.InitiatedBy,
		RowCount: in.RowCount, AmountCents: in.AmountCents,
	})
	if err != nil {
		return 0, coreclient.AsActivityError(err)
	}
	return res.GenTxBatchID, nil
}

// CheckGroupPolicy asks Core for one group's limit decision.
// PATTERN: Business Outcome as Result (https://docs.temporal.io/design-patterns/non-retryable-errors):
// a DENY is returned as a decision, not as an error, so it is recorded and never retried.
func (a *Activities) CheckGroupPolicy(ctx context.Context, in PolicyInput) (PolicyDecision, error) {
	a.alertOnRetries(ctx, in.FiID)
	res, err := a.Core.CheckGroupPolicy(ctx, in.FiID, in.GenTxBatchID, coreclient.PolicyCheckRequest{
		GroupKey: in.Group.GroupKey, OriginatingAccount: in.Group.OriginatingAccount,
		ProcessDate: in.Group.ProcessDate, AmountCents: in.Group.TotalAmountCents,
	})
	if err != nil {
		return PolicyDecision{}, coreclient.AsActivityError(err)
	}
	d := PolicyDecision{Decision: res.Decision, Code: res.Code, LimitCents: res.LimitCents, UsedCents: res.UsedCents, RequestedCents: res.RequestedCents}
	if d.Decision == DecisionDeny {
		d.Message = fmt.Sprintf("Daily limit exceeded: %s requested, %s limit, %s already used today",
			millions(res.RequestedCents), millions(res.LimitCents), millions(res.UsedCents))
	}
	return d, nil
}

// InsertDraftChunk reads ONE chunk's rows from the claim check and writes them to Core in one TVP call.
// WHY the Activity (not the workflow) reads the rows: rows never enter workflow history; only this
// Activity's input (a reference) and its result (counts) do.
func (a *Activities) InsertDraftChunk(ctx context.Context, in DraftChunkInput) (ChunkDraftResult, error) {
	a.alertOnRetries(ctx, in.FiID)
	m, err := a.readManifest(ctx, in.FiID, in.BatchID)
	if err != nil {
		return ChunkDraftResult{}, err
	}
	rows, err := staging.ReadRange(ctx, a.ClaimCheck, m, in.Chunk.Offset, in.Chunk.Length)
	if err != nil {
		return ChunkDraftResult{}, claimCheckError(err)
	}

	req := coreclient.DraftChunkRequest{ChunkID: in.Chunk.ChunkID, Actor: in.Actor, Wires: rows}
	if in.ForceReject != nil {
		req.ForceReject = &coreclient.ForceReject{Code: in.ForceReject.Code, Message: in.ForceReject.Message}
	}
	res, err := a.Core.DraftChunk(ctx, in.FiID, in.GenTxBatchID, req)
	if err != nil {
		return ChunkDraftResult{}, coreclient.AsActivityError(err)
	}
	if res.AlreadyPresent > 0 {
		// A previous attempt committed but its response never arrived. Nothing is duplicated; Core reports
		// the chunk's true totals.
		activity.GetLogger(ctx).Info("chunk already present in Core", "chunkId", in.Chunk.ChunkID, "alreadyPresent", res.AlreadyPresent)
	}
	out := ChunkDraftResult{ChunkDrafted: res.ChunkDrafted, ChunkRejected: res.ChunkRejected,
		InsertedThisCall: res.InsertedThisCall, AlreadyPresent: res.AlreadyPresent}
	for _, r := range res.Rejected[:min(len(res.Rejected), MaxRejectionsPerResult)] {
		out.Rejected = append(out.Rejected, model.Rejection{RowNumber: r.RowNumber, ExternalWireID: r.ExternalWireID, Code: r.Code, Message: r.Message})
	}
	return out, nil
}

// AuthorizeChunk flips one chunk's Drafted wires to Authorized. No claim-check read: the rows are in Core now.
func (a *Activities) AuthorizeChunk(ctx context.Context, in AuthorizeChunkInput) (ChunkAuthorizeResult, error) {
	a.alertOnRetries(ctx, in.FiID)
	res, err := a.Core.AuthorizeChunk(ctx, in.FiID, in.GenTxBatchID, in.ChunkID, coreclient.AuthorizeChunkRequest{ApprovalRef: in.ApprovalRef, Actor: in.Actor})
	if err != nil {
		return ChunkAuthorizeResult{}, coreclient.AsActivityError(err)
	}
	return ChunkAuthorizeResult{ChunkAuthorized: res.ChunkAuthorized, ChunkRejected: res.ChunkRejected, FlippedThisCall: res.FlippedThisCall}, nil
}

// RejectChunk carries out an operator's REJECT_CHUNK decision during the authorize phase.
func (a *Activities) RejectChunk(ctx context.Context, in RejectChunkInput) (ChunkAuthorizeResult, error) {
	a.alertOnRetries(ctx, in.FiID)
	res, err := a.Core.RejectChunk(ctx, in.FiID, in.GenTxBatchID, in.ChunkID, coreclient.RejectChunkRequest{Code: in.Code, Message: in.Message, Actor: in.Actor})
	if err != nil {
		return ChunkAuthorizeResult{}, coreclient.AsActivityError(err)
	}
	return ChunkAuthorizeResult{ChunkAuthorized: res.ChunkAuthorized, ChunkRejected: res.ChunkRejected, FlippedThisCall: res.FlippedThisCall}, nil
}

// SetGenTxBatchStatus keeps Core's batch record in step with the workflow's phase: Drafted, Authorizing,
// Authorized, and the closing outcomes Expired, Denied, Cancelled, Rejected. Idempotent in Core.
func (a *Activities) SetGenTxBatchStatus(ctx context.Context, in StatusInput) (GenTxBatchStatus, error) {
	a.alertOnRetries(ctx, in.FiID)
	res, err := a.Core.SetGenTxBatchStatus(ctx, in.FiID, in.GenTxBatchID, coreclient.SetStatusRequest{Status: in.Status, Reason: in.Reason, Actor: in.Actor})
	if err != nil {
		return GenTxBatchStatus{}, coreclient.AsActivityError(err)
	}
	c := res.Counts
	return GenTxBatchStatus{Status: res.Status, Drafted: c.Drafted, Authorized: c.Authorized, Rejected: c.Rejected,
		Expired: c.Expired, Denied: c.Denied, Cancelled: c.Cancelled}, nil
}

// readManifest loads the manifest, failing non-retryably if staging data is gone or the batch is invalid.
func (a *Activities) readManifest(ctx context.Context, fiID, batchID string) (model.Manifest, error) {
	m, err := staging.ReadManifest(ctx, a.ClaimCheck, fiID, batchID)
	if err != nil {
		return model.Manifest{}, claimCheckError(err)
	}
	if m.Status != model.StagingValid {
		// Defense in depth: bulkwire-service already refuses to draft an INVALID batch.
		return model.Manifest{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("staged batch is %s and cannot be drafted", m.Status), model.ErrTypeStagingInvalid, nil)
	}
	return m, nil
}

// claimCheckError: missing staged data parks the chunk (a human decides); anything else is retried.
func claimCheckError(err error) error {
	if errors.Is(err, claimcheck.ErrNotFound) {
		return temporal.NewNonRetryableApplicationError("staged data not found (expired or never staged)",
			model.ErrTypeClaimCheckNotFound, err)
	}
	return temporal.NewApplicationError("claim-check store unavailable: "+err.Error(), model.ErrTypeClaimCheckUnavailable)
}

// millions renders cents as "$16.5M" with integer arithmetic (money never passes through floating point).
func millions(cents int64) string {
	tenths := cents / 10_000_000 // $0.1M units, rounded down
	return fmt.Sprintf("$%d.%dM", tenths/10, tenths%10)
}
