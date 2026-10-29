// JSON contracts for approvals (portal) and the callback Core sends to bulkwire-service when a batch is
// fully approved or denied.
namespace CoreApi.Contracts;

// amountCents/rowCount/initiatedBy come from the staging summary for approve-before-draft (no GenTxBatch
// exists yet). PRODUCTION NOTE: a real core computes these itself.
public sealed record ApproveRequest(string BatchId, string Tac, long AmountCents, int RowCount, string InitiatedBy);
public sealed record DenyRequest(string BatchId, string? Reason);

public sealed record Signature(string UserId, string DisplayName, DateTime SignedAt);

// Status as stored in dbo.Approvals: PendingSecond | Approved | Denied (null = nobody has acted yet).
public sealed record ApprovalRecord(
    string? Status,
    string? ApprovalRef,
    int RequiredApprovers,
    string? DeniedBy,
    string? DenyReason,
    DateTime? CompletedAt,
    IReadOnlyList<Signature> Signatures);

public static class OrchestrationOutcome
{
    public const string Notified = "NOTIFIED";
    public const string NotifyFailed = "NOTIFY_FAILED";
    public const string NotApplicable = "NOT_APPLICABLE";
}

public sealed record ApprovalResponse(
    string Status,              // NONE | PENDING_SECOND_APPROVAL | APPROVED | DENIED
    string? ApprovalRef,
    int RequiredApprovers,
    IReadOnlyList<Signature> Signatures,
    string? DeniedBy,
    string? DenyReason,
    string Orchestration);      // NOTIFIED | NOTIFY_FAILED | NOT_APPLICABLE

public static class ApprovalResponses
{
    public static string PortalStatus(string? dbStatus) => dbStatus switch
    {
        "Approved" => "APPROVED",
        "PendingSecond" => "PENDING_SECOND_APPROVAL",
        "Denied" => "DENIED",
        _ => "NONE",
    };

    public static ApprovalResponse From(ApprovalRecord r, string orchestration) =>
        new(PortalStatus(r.Status), r.ApprovalRef, r.RequiredApprovers, r.Signatures, r.DeniedBy, r.DenyReason, orchestration);
}

// Callback bodies (core-api -> bulkwire-service /internal/*) (the internal callback contract).
public sealed record ApprovedCallback(string FiId, string BatchId, string ApprovalRef, IReadOnlyList<CallbackApprover> Approvers, DateTime ApprovedAt);
public sealed record CallbackApprover(string UserId, string DisplayName, DateTime SignedAt);
public sealed record DeniedCallback(string FiId, string BatchId, string DeniedBy, string? Reason);
