// Approval endpoints, called by the portal. Core owns the decision: MFA/TAC, determination (role, limit,
// segregation of duties), and dual control. Only a FINAL decision is sent to the orchestration layer
// (bulkwire-service), which turns it into an `authorize` or `deny` Update on the batch's workflow.
//
// PATTERN: Approval — https://docs.temporal.io/design-patterns/approval
// The workflow waits durably for this decision; it never makes it.
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

            // 2. Determination + dual control, in the system of record.
            var record = await procs.RecordApprovalSignatureAsync(body.BatchId, user.CustomerId ?? "", user.UserId,
                body.InitiatedBy, body.AmountCents, options.DualControlThresholdCents, ct);

            // 3. Fully approved -> tell the orchestration layer. Re-calling after approval re-sends the
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
            var record = await procs.RecordDenialAsync(body.BatchId, user.CustomerId ?? "", user.UserId, body.Reason, ct);
            var orchestration = await callback.NotifyDeniedAsync(new DeniedCallback(options.FiId, body.BatchId, user.UserId, body.Reason), ct);
            return Results.Ok(ApprovalResponses.From(record, orchestration));
        });

        group.MapGet("/{batchId}", async (string batchId, Procs procs, CancellationToken ct) =>
            Results.Ok(ApprovalResponses.From(await procs.GetApprovalAsync(batchId, ct), OrchestrationOutcome.NotApplicable)));
    }
}
