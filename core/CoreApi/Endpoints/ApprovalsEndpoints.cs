// Approval endpoints, called by the portal. Core owns the decision: MFA/TAC, determination (role, limit,
// segregation of duties), and dual control. Only a FINAL decision is sent to the orchestration layer
// (bulkwire-service), which turns it into an `authorize` or `deny` Update on the batch's workflow.
//
// PATTERN: Approval — https://docs.temporal.io/design-patterns/approval
// The workflow waits durably for this decision; it never makes it.
using CoreApi.Auth;
using CoreApi.Config;
using CoreApi.Contracts;
using CoreApi.Data;
using CoreApi.Orchestration;

namespace CoreApi.Endpoints;

public static class ApprovalsEndpoints
{
    public static void Map(WebApplication app)
    {
        var group = app.MapGroup("/api/approvals").RequirePortalUser();

        group.MapPost("", async (HttpContext http, ApproveRequest body, Procs procs, CoreOptions options,
            BulkwireCallbackClient callback, CancellationToken ct) =>
        {
            var user = http.PortalUser();

            // 1. MFA / TAC. PRODUCTION NOTE: real MFA delivery and verification (push, hardware token, ...).
            if (body.Tac != options.DemoTac)
                throw new CoreException(401, "TAC_INVALID", "The security code is not correct", false);

            // 2. What is being approved: Core's own batch record, or before the draft, the staged batch.
            var subject = await SubjectAsync(body.BatchId, user, procs, callback, ct);

            // 3. Determination + dual control, in the system of record.
            var record = await procs.RecordApprovalSignatureAsync(body.BatchId, user.CustomerId ?? "", user.UserId,
                subject.InitiatedBy, subject.AmountCents, options.DualControlThresholdCents, ct);

            // 4. Fully approved -> tell the orchestration layer. Re-calling after approval re-sends the
            // callback; that is safe because the workflow Update ID is the approvalRef.
            var orchestration = OrchestrationOutcome.NotApplicable;
            if (record.Status == "Approved")
            {
                var approvers = record.Signatures.Select(s => new CallbackApprover(s.UserId, s.DisplayName, s.SignedAt)).ToList();
                orchestration = await callback.NotifyApprovedAsync(new ApprovedCallback(options.FiId, body.BatchId,
                    record.ApprovalRef!, approvers, record.CompletedAt ?? DateTime.UtcNow), ct);
            }
            return Results.Ok(ApprovalResponses.From(record, orchestration));
        });

        group.MapPost("/deny", async (HttpContext http, DenyRequest body, Procs procs, CoreOptions options,
            BulkwireCallbackClient callback, CancellationToken ct) =>
        {
            var user = http.PortalUser();
            await SubjectAsync(body.BatchId, user, procs, callback, ct); // the batch must exist and be this company's
            var record = await procs.RecordDenialAsync(body.BatchId, user.CustomerId ?? "", user.UserId, body.Reason, ct);
            var orchestration = await callback.NotifyDeniedAsync(new DeniedCallback(options.FiId, body.BatchId, user.UserId, body.Reason), ct);
            return Results.Ok(ApprovalResponses.From(record, orchestration));
        });

        group.MapGet("/{batchId}", async (HttpContext http, string batchId, Procs procs, CancellationToken ct) =>
        {
            var user = http.PortalUser();
            var record = await procs.GetApprovalAsync(batchId, ct);
            // Another company's batch is "not found"; bank operations staff may read any (exceptions page). The
            // owner is the approval row's customer, or before anyone signed, Core's batch record's customer.
            var owner = record.CustomerId ?? (await procs.GetGenTxBatchByExternalIdAsync(batchId, ct))?.CustomerId;
            if (owner is not null && !user.IsBankOps && owner != user.CustomerId)
                throw new CoreException(404, "BATCH_NOT_FOUND", "batch not found", false);
            return Results.Ok(ApprovalResponses.From(record, OrchestrationOutcome.NotApplicable));
        });
    }

    private sealed record Subject(string InitiatedBy, long AmountCents);

    // The batch an approval or denial is about. After the draft, Core's own batch record is authoritative (the
    // procedures also re-check its owner). Before the draft, Core has no record yet, so it asks the orchestration
    // layer for the staged batch, server to server.
    // WHY not the request body: the amount decides approval limits and dual control. Taking it from the browser
    // would let an approver send a smaller amount and approve a batch above their limit, or alone.
    private static async Task<Subject> SubjectAsync(string batchId, PortalUser user, Procs procs,
        BulkwireCallbackClient bulkwire, CancellationToken ct)
    {
        var genTx = await procs.GetGenTxBatchByExternalIdAsync(batchId, ct);
        if (genTx is not null)
            return new Subject(genTx.InitiatedBy, genTx.ExpectedAmountCents);

        var staged = await bulkwire.GetStagedBatchAsync(batchId, ct);
        if (staged is null || staged.CustomerId != user.CustomerId)
            throw new CoreException(404, "BATCH_NOT_FOUND", "batch not found", false);
        if (staged.Status != "VALID")
            throw new CoreException(409, "STAGING_INVALID", "this file has errors and cannot be approved", false);
        return new Subject(staged.UploadedBy, staged.AmountCents);
    }
}
