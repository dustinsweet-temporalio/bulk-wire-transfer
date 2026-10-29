// Message handlers: one Query (`status`) and five Updates (`draft`, `authorize`, `deny`, `cancel`,
// `resolve`). There are no Signals: every inbound message is an Update, so the caller learns synchronously
// whether it was accepted (validator) and gets the resulting view back.
//
// Handlers only RECORD intent and return immediately; they never run Activities. The main workflow loop
// notices the recorded intent (workflow.Await conditions) and does the work. That keeps every side effect in
// one readable sequence (workflow.go) and lets handlers finish instantly.
//
// DETERMINISM / correctness: registerHandlers runs before the workflow's first blocking call. With
// Update-with-Start the first Update is delivered at the very start of the execution, so the handler must
// already exist when the workflow function first yields.
package workflow

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.temporal.io/sdk/workflow"
)

func registerHandlers(ctx workflow.Context, s *batchState) error {
	// PATTERN: Query for State — https://docs.temporal.io/evaluate/features/workflow-message-passing
	// The portal polls this every second. A Query writes nothing to history, so polling is cheap.
	if err := workflow.SetQueryHandler(ctx, QueryStatus, func() (BatchView, error) { return s.view(), nil }); err != nil {
		return err
	}

	// PATTERN: Update Validator — https://docs.temporal.io/encyclopedia/workflow-message-passing
	// A validator must NOT mutate state and must not block. A rejection is returned to the caller and never
	// enters history, so invalid requests leave no trace on the batch.
	updates := []struct {
		name      string
		handler   any
		validator any
	}{
		{UpdateDraft, s.handleDraft, s.validateDraft},
		{UpdateAuthorize, s.handleAuthorize, s.validateAuthorize},
		{UpdateDeny, s.handleDeny, s.validateDeny},
		{UpdateCancel, s.handleCancel, s.validateCancel},
		{UpdateResolve, s.handleResolve, s.validateResolve},
	}
	for _, u := range updates {
		if err := workflow.SetUpdateHandlerWithOptions(ctx, u.name, u.handler,
			workflow.UpdateHandlerOptions{Validator: u.validator}); err != nil {
			return err
		}
	}
	return nil
}

// ---- draft ----------------------------------------------------------------------------------------------------

func (s *batchState) validateDraft(_ workflow.Context, req DraftRequest) error {
	if s.phase.Terminal() {
		return fmt.Errorf("batch is %s and can no longer be drafted", s.phase)
	}
	return nil
}

// handleDraft is idempotent: a second Draft click, another tab, or a retried request leaves the first one.
func (s *batchState) handleDraft(ctx workflow.Context, req DraftRequest) (BatchView, error) {
	if s.draft == nil {
		s.draft = &DraftView{RequestedBy: req.RequestedBy, RequestedAt: workflow.Now(ctx)}
		s.addTimeline(ctx, EvDraftRequested, "by "+req.RequestedBy)
	}
	return s.view(), nil
}

// ---- authorize ------------------------------------------------------------------------------------------------

func (s *batchState) validateAuthorize(_ workflow.Context, req AuthorizeRequest) error {
	switch {
	case req.ApprovalRef == "":
		return errors.New("approvalRef is required")
	case s.phase.Terminal():
		return fmt.Errorf("batch is %s; a late approval cannot reopen it", s.phase)
	case s.closeRequest != nil:
		return fmt.Errorf("batch is closing (%s)", s.closeRequest.Phase)
	case s.authorization != nil && s.authorization.ApprovalRef != req.ApprovalRef:
		return fmt.Errorf("batch is already authorized with a different approval (%s)", s.authorization.ApprovalRef)
	}
	return nil
}

// handleAuthorize records Core's approval (idempotent for the same approvalRef). It may arrive before the
// draft (approve-before-draft): the workflow then drafts and authorizes in one execution.
func (s *batchState) handleAuthorize(ctx workflow.Context, req AuthorizeRequest) (BatchView, error) {
	if s.authorization == nil {
		s.authorization = &req
		names := make([]string, len(req.Approvers))
		for i, a := range req.Approvers {
			names[i] = a.DisplayName
		}
		// WHY " and ": display names already contain commas ("Alex Rivera, Controller").
		s.addTimeline(ctx, EvAuthorizationReceived, fmt.Sprintf("%s · approved by %s", req.ApprovalRef, strings.Join(names, " and ")))
	}
	return s.view(), nil
}

// ---- deny / cancel ---------------------------------------------------------------------------------------------

func (s *batchState) validateClose(action string) error {
	switch {
	case s.phase == PhaseAuthorizing || s.phase == PhaseAuthorized:
		return fmt.Errorf("batch is %s; it can no longer be %s", s.phase, action)
	case s.phase.Terminal():
		return fmt.Errorf("batch is already %s", s.phase)
	case s.phase == PhaseDrafted && s.authorization != nil:
		// Core approved it and the release has begun (Core is being moved to Authorizing).
		return fmt.Errorf("batch was approved (%s) and is being released; it can no longer be %s", s.authorization.ApprovalRef, action)
	}
	return nil
}

func (s *batchState) validateDeny(_ workflow.Context, _ DenyRequest) error {
	return s.validateClose("denied")
}

func (s *batchState) handleDeny(ctx workflow.Context, req DenyRequest) (BatchView, error) {
	if s.closeRequest == nil {
		s.closeRequest = &closeRequest{Phase: PhaseDenied, By: req.DeniedBy, Reason: req.Reason}
	}
	return s.view(), nil
}

func (s *batchState) validateCancel(_ workflow.Context, _ CancelRequest) error {
	return s.validateClose("cancelled")
}

func (s *batchState) handleCancel(ctx workflow.Context, req CancelRequest) (BatchView, error) {
	if s.closeRequest == nil {
		s.closeRequest = &closeRequest{Phase: PhaseCancelled, By: req.By, Reason: req.Reason}
		s.addTimeline(ctx, EvCancelRequested, "by "+req.By)
	}
	return s.view(), nil
}

// ---- resolve -----------------------------------------------------------------------------------------------------

// validateResolve accepts only a decision the parked item allows (Attention.AllowedDecisions):
//   - chunk parked while DRAFTING:    RETRY | REJECT_CHUNK | CANCEL_BATCH
//   - chunk parked while AUTHORIZING: RETRY | REJECT_CHUNK            (wires are already approved)
//   - "_batch" step while DRAFTING:   RETRY | CANCEL_BATCH            (no chunk to reject)
//   - "_batch" step otherwise:        RETRY
func (s *batchState) validateResolve(_ workflow.Context, req ResolveRequest) error {
	var att *Attention
	var pending *ResolveRequest
	if req.ChunkID == BatchTarget {
		att, pending = s.batchAttention, s.batchResolution
	} else if c := s.chunkByID(req.ChunkID); c != nil {
		att, pending = c.Attention, c.Resolution
	} else {
		return fmt.Errorf("unknown chunk %q", req.ChunkID)
	}
	switch {
	case att == nil:
		return fmt.Errorf("%s is not parked", req.ChunkID)
	case pending != nil:
		return fmt.Errorf("%s already has a pending decision (%s)", req.ChunkID, pending.Decision)
	case !slices.Contains(att.AllowedDecisions, req.Decision):
		return fmt.Errorf("decision %q is not allowed for %s now (allowed: %v)", req.Decision, req.ChunkID, att.AllowedDecisions)
	}
	return nil
}

func (s *batchState) handleResolve(ctx workflow.Context, req ResolveRequest) (BatchView, error) {
	r := req
	if req.ChunkID == BatchTarget {
		s.batchResolution = &r
	} else {
		s.chunkByID(req.ChunkID).Resolution = &r
	}
	if req.Decision == DecisionCancelBatch && s.closeRequest == nil {
		s.closeRequest = &closeRequest{Phase: PhaseCancelled, By: req.By, Reason: req.Note}
		s.addTimeline(ctx, EvCancelRequested, fmt.Sprintf("by %s (resolving %s)", req.By, req.ChunkID))
	}
	return s.view(), nil
}
