// Commands into the workflow: Update-with-Start (draft, authorize) and plain Updates (cancel, deny, resolve).
//
// PATTERN: Update-with-Start — https://docs.temporal.io/sending-messages#update-with-start
// One call either starts the batch's workflow and delivers the Update, or (if it is already running)
// delivers the Update to the existing execution. Combined with:
//   - Workflow ID "bulkwire/{fiId}/{batchId}" (PATTERN: Business Workflow ID): every entry point computes the
//     same ID, so a second Draft click, another tab, a retried HTTP call, or Core's approval callback all reach
//     the SAME execution (WorkflowIDConflictPolicy USE_EXISTING);
//   - WorkflowIDReusePolicy REJECT_DUPLICATE: a closed batch can never be restarted by a late message;
//   - Update ID = Idempotency-Key (portal) or approvalRef (Core): request-level dedup; a retried request
//     attaches to the same Update instead of applying it twice.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"bulkwire/internal/auth"
	"bulkwire/internal/claimcheck"
	"bulkwire/internal/model"
	"bulkwire/internal/staging"
	"bulkwire/internal/temporalx"
	"bulkwire/internal/workflow"
)

// updateTimeout bounds how long a command waits for the workflow to accept and complete the Update.
// Updates need a running worker; if none answers in time the caller gets 503 and can simply retry
// (same Update ID), because nothing was half-done.
const updateTimeout = 15 * time.Second

// batchInput copies the manifest and the FI's CURRENT draft defaults into the workflow input. From then on
// the running batch never changes behavior, whatever happens to the defaults.
func (s *Server) batchInput(m model.Manifest) (workflow.BatchInput, error) {
	d, err := s.Defaults.get(m.FiID)
	if err != nil {
		return workflow.BatchInput{}, err
	}
	return workflow.BatchInput{
		BatchID: m.BatchID, FiID: m.FiID, CustomerID: m.CustomerID, InitiatedBy: m.UploadedBy,
		RowCount: m.ValidRowCount, TotalAmountCents: m.TotalAmountCents,
		ChunkSize: d.chunkSize, Concurrency: d.concurrency, ApprovalWindow: d.approvalWindow,
		RetryBudget: d.retryBudget, RetryMaxInterval: d.retryMaxInterval,
		FileName: m.FileName, FileSHA256: m.FileSHA256, UploadedAt: m.UploadedAt, GroupCount: len(m.Groups),
	}, nil
}

func (s *Server) startOptions(m model.Manifest) client.StartWorkflowOptions {
	fi := auth.Directory[m.FiID]
	return client.StartWorkflowOptions{
		ID:                       temporalx.WorkflowID(m.FiID, m.BatchID),
		TaskQueue:                s.Cfg.Temporal.TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		// Not encrypted (Search Attributes are not payloads), so no PII: ids and a status only.
		TypedSearchAttributes: temporal.NewSearchAttributes(
			temporalx.SAFiID.ValueSet(m.FiID),
			temporalx.SACustomerID.ValueSet(m.CustomerID),
			temporalx.SAInitiatedBy.ValueSet(m.UploadedBy),
			temporalx.SABulkWireStatus.ValueSet(string(workflow.PhaseDrafting)),
			temporalx.SANeedsAttention.ValueSet(false),
		),
		// PATTERN: Workflow User Metadata — https://docs.temporal.io/workflow-execution
		// Shown in the Web UI (encrypted payloads, decoded by the Codec Server). Masked data only.
		StaticSummary: fmt.Sprintf("Bulk wire · %s · %s · %d wires · %s", fi.DisplayName,
			auth.CustomerName(m.FiID, m.CustomerID), m.ValidRowCount, model.FormatDollars(m.TotalAmountCents)),
		StaticDetails: fmt.Sprintf("**File:** %s\n\n**SHA-256:** `%s`\n\n**Uploaded:** %s by %s\n\n**Groups:** %d (originating account + process date)\n\n**Batch ID:** %s",
			m.FileName, m.FileSHA256, m.UploadedAt.UTC().Format(time.RFC3339), m.UploadedBy, len(m.Groups), m.BatchID),
		// PATTERN: Fairness by Tenant — https://docs.temporal.io/develop/task-queue-priority-fairness
		// Every Activity this workflow schedules inherits the key, so the Temporal Service dispatches Core
		// work fairly across FIs sharing the task queue (and can rate-limit per FI).
		Priority: temporal.Priority{FairnessKey: m.FiID},
	}
}

// updateWithStart starts the batch's workflow if needed and delivers one Update, waiting for its result.
func (s *Server) updateWithStart(ctx context.Context, m model.Manifest, name, updateID string, arg any) (workflow.BatchView, error) {
	in, err := s.batchInput(m)
	if err != nil {
		return workflow.BatchView{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	startOp := s.Temporal.NewWithStartWorkflowOperation(s.startOptions(m), workflow.BulkWireBatch, in)
	handle, err := s.Temporal.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: startOp,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateID:     updateID,
			UpdateName:   name,
			Args:         []any{arg},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	if err != nil {
		return workflow.BatchView{}, err
	}
	var view workflow.BatchView
	return view, handle.Get(ctx, &view)
}

// update delivers an Update to an EXISTING workflow (no start).
func (s *Server) update(ctx context.Context, workflowID, name, updateID string, arg any) (workflow.BatchView, error) {
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	handle, err := s.Temporal.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID: workflowID, UpdateID: updateID, UpdateName: name, Args: []any{arg},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return workflow.BatchView{}, err
	}
	var view workflow.BatchView
	return view, handle.Get(ctx, &view)
}

// writeCommandError maps Temporal outcomes to HTTP:
//   - a validator rejection (the workflow said no)        -> 409 REJECTED_BY_WORKFLOW
//   - start refused by REJECT_DUPLICATE (batch is closed)  -> 409 BATCH_CLOSED
//   - workflow does not exist / already completed          -> 404 / 409 BATCH_CLOSED
//   - no worker answered in time                           -> 503 ORCHESTRATION_UNAVAILABLE ("try again")
func (s *Server) writeCommandError(ctx context.Context, w http.ResponseWriter, workflowID string, err error) {
	var already *serviceerror.WorkflowExecutionAlreadyStarted
	var rejected *temporal.ApplicationError
	var timeout *client.WorkflowUpdateServiceTimeoutOrCanceledError
	var notFound *serviceerror.NotFound
	switch {
	case errors.As(err, &already):
		writeError(w, http.StatusConflict, "BATCH_CLOSED", "This batch is closed; it cannot be changed or restarted.")
	case errors.As(err, &rejected):
		writeError(w, http.StatusConflict, "REJECTED_BY_WORKFLOW", rejected.Message())
	case errors.As(err, &timeout), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusServiceUnavailable, "ORCHESTRATION_UNAVAILABLE", "The bulk wire service is busy right now. Try again.")
	case errors.As(err, &notFound):
		if s.isClosed(ctx, workflowID) {
			writeError(w, http.StatusConflict, "BATCH_CLOSED", "This batch is closed; it cannot be changed.")
			return
		}
		writeError(w, http.StatusNotFound, "NOT_FOUND", "This batch has not been drafted, so there is nothing to change.")
	default:
		s.Log.Error("command failed", "workflowId", workflowID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "ORCHESTRATION_UNAVAILABLE", "The bulk wire service is unavailable. Try again.")
	}
}

func (s *Server) isClosed(ctx context.Context, workflowID string) bool {
	desc, err := s.Temporal.DescribeWorkflowExecution(ctx, workflowID, "")
	return err == nil && desc.GetWorkflowExecutionInfo().GetStatus() != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING
}

func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		writeError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "send an Idempotency-Key header (one per action per page load)")
		return "", false
	}
	return key, true
}

// draft: Initiator or Approver of the batch's customer. Settings come from the FI's draft defaults.
func (s *Server) draft(w http.ResponseWriter, r *http.Request, c auth.Claims) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	m, ok := s.manifestFor(r.Context(), w, c, r.PathValue("batchId"))
	if !ok {
		return
	}
	if c.CustomerID != m.CustomerID || (c.Role != auth.RoleInitiator && c.Role != auth.RoleApprover) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "only the company's initiators and approvers can draft a batch")
		return
	}
	if m.Status != model.StagingValid {
		writeError(w, http.StatusConflict, "BATCH_INVALID", "This file has errors. Correct it and upload it again.")
		return
	}
	wfID := temporalx.WorkflowID(m.FiID, m.BatchID)
	view, err := s.updateWithStart(r.Context(), m, workflow.UpdateDraft, key, workflow.DraftRequest{RequestedBy: c.UserID()})
	if err != nil {
		s.writeCommandError(r.Context(), w, wfID, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"batchId": m.BatchID, "workflowId": wfID, "view": view})
}

// cancel: the batch's initiator, while DRAFTING or DRAFTED (the workflow's validator enforces the phase).
// A staged-only batch has no workflow and nothing to cancel (404).
func (s *Server) cancel(w http.ResponseWriter, r *http.Request, c auth.Claims) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &body)
	if c.CustomerID == "" || c.Role != auth.RoleInitiator {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "only the company's initiators can cancel a batch")
		return
	}
	wfID := temporalx.WorkflowID(c.FiID, r.PathValue("batchId"))
	if v, ok := s.queryView(r.Context(), wfID); ok && v.CustomerID != c.CustomerID {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "batch not found")
		return
	}
	view, err := s.update(r.Context(), wfID, workflow.UpdateCancel, key, workflow.CancelRequest{By: c.UserID(), Reason: body.Reason})
	if err != nil {
		s.writeCommandError(r.Context(), w, wfID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batchId": r.PathValue("batchId"), "workflowId": wfID, "view": view})
}

// resolve: bank operations' decision for a parked chunk (or "_batch").
func (s *Server) resolve(w http.ResponseWriter, r *http.Request, c auth.Claims) {
	if !c.IsBankOps() {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "only bank operations staff can resolve parked items")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		ChunkID  string `json:"chunkId"`
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := decodeJSON(r, &body); err != nil || body.ChunkID == "" || body.Decision == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "chunkId and decision are required")
		return
	}
	wfID := temporalx.WorkflowID(c.FiID, r.PathValue("batchId"))
	view, err := s.update(r.Context(), wfID, workflow.UpdateResolve, key,
		workflow.ResolveRequest{ChunkID: body.ChunkID, Decision: body.Decision, By: c.UserID(), Note: body.Note})
	if err != nil {
		s.writeCommandError(r.Context(), w, wfID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"view": view})
}

// ---- internal callbacks from Core --------------------------------------------------------------------------

// internalApproval: Core fully approved the batch. Update-with-Start `authorize` with Update ID = approvalRef,
// so Core's re-sends (resilience retries, the portal's "Retry notification") deduplicate. If nobody drafted
// yet (approve-before-draft), this call STARTS the workflow, which drafts and then authorizes.
func (s *Server) internalApproval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FiID        string              `json:"fiId"`
		BatchID     string              `json:"batchId"`
		ApprovalRef string              `json:"approvalRef"`
		Approvers   []workflow.Approver `json:"approvers"`
		ApprovedAt  time.Time           `json:"approvedAt"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid approval callback: "+err.Error())
		return
	}
	if body.FiID == "" || body.BatchID == "" || body.ApprovalRef == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "fiId, batchId and approvalRef are required")
		return
	}
	m, err := staging.ReadManifest(r.Context(), s.Store, body.FiID, body.BatchID)
	if errors.Is(err, claimcheck.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no staged batch with that id")
		return
	} else if err != nil {
		writeError(w, http.StatusServiceUnavailable, "CLAIM_CHECK_UNAVAILABLE", "staged data is temporarily unavailable")
		return
	}
	wfID := temporalx.WorkflowID(m.FiID, m.BatchID)
	view, err := s.updateWithStart(r.Context(), m, workflow.UpdateAuthorize, body.ApprovalRef,
		workflow.AuthorizeRequest{ApprovalRef: body.ApprovalRef, Approvers: body.Approvers, ApprovedAt: body.ApprovedAt})
	if err != nil {
		// A duplicate callback after the batch already completed with THIS approval is a success, not an error.
		if v, ok := s.queryView(r.Context(), wfID); ok && v.Phase == workflow.PhaseAuthorized &&
			v.Authorization != nil && v.Authorization.ApprovalRef == body.ApprovalRef {
			writeJSON(w, http.StatusOK, map[string]any{"result": "ALREADY_APPLIED", "workflowId": wfID, "phase": v.Phase})
			return
		}
		s.writeCommandError(r.Context(), w, wfID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": "AUTHORIZE_DELIVERED", "workflowId": wfID, "phase": view.Phase})
}

// internalDenial: Core recorded a denial. Update `deny` (never starts a workflow). If no workflow exists (denied
// before draft), Core already holds the denial record and there is nothing to stop.
func (s *Server) internalDenial(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FiID     string `json:"fiId"`
		BatchID  string `json:"batchId"`
		DeniedBy string `json:"deniedBy"`
		Reason   string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil || body.FiID == "" || body.BatchID == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "fiId and batchId are required")
		return
	}
	wfID := temporalx.WorkflowID(body.FiID, body.BatchID)
	view, err := s.update(r.Context(), wfID, workflow.UpdateDeny, "deny-"+body.BatchID,
		workflow.DenyRequest{DeniedBy: body.DeniedBy, Reason: body.Reason})
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) && !s.isClosed(r.Context(), wfID) {
		writeJSON(w, http.StatusOK, map[string]any{"result": "NO_WORKFLOW"})
		return
	}
	if err != nil {
		s.writeCommandError(r.Context(), w, wfID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": "DENY_DELIVERED", "workflowId": wfID, "phase": view.Phase})
}
