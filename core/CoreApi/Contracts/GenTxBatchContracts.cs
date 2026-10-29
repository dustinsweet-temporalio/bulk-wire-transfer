// JSON contracts for the GenTxBatch endpoints: writes called by bulk wire Activities, reads called by the
// portal. camelCase on the wire, money in long cents, dates as yyyy-MM-dd (DateOnly).
namespace CoreApi.Contracts;

public sealed record EnsureGenTxBatchRequest(string ExternalBatchId, string CustomerId, string InitiatedBy, int RowCount, long AmountCents);
public sealed record EnsureGenTxBatchResponse(long GenTxBatchId, bool Created, string Status);

public sealed record PolicyCheckRequest(string GroupKey, string OriginatingAccount, DateOnly ProcessDate, long AmountCents);
public sealed record PolicyCheckResponse(string Decision, string? Code, long LimitCents, long UsedCents, long RequestedCents);

// One wire row, exactly as staged by bulkwire-service (field names match the Go WireRow JSON tags).
public sealed record WireDto(
    string ExternalWireId,
    int RowNumber,
    string OriginatingAccount,
    DateOnly ProcessDate,
    long AmountCents,
    string Currency,
    string BeneficiaryName,
    string BeneficiaryAccount,
    string BeneficiaryRouting,
    string? BeneficiaryBank,
    string? Reference,
    string? Subsidiary);

public sealed record ForceReject(string Code, string? Message);

public sealed record DraftChunkRequest(string ChunkId, string Actor, ForceReject? ForceReject, IReadOnlyList<WireDto> Wires);

public sealed record RejectedWire(int RowNumber, string ExternalWireId, string Code, string? Message);

// Chunk-level totals (not call-level): see usp_InsertDraftWires for why.
public sealed record DraftChunkResponse(int ChunkDrafted, int ChunkRejected, int InsertedThisCall, int AlreadyPresent, IReadOnlyList<RejectedWire> Rejected);

public sealed record AuthorizeChunkRequest(string ApprovalRef, string Actor);
public sealed record RejectChunkRequest(string Code, string? Message, string Actor);
public sealed record ChunkTotalsResponse(int ChunkAuthorized, int ChunkRejected, int FlippedThisCall);

public sealed record SetStatusRequest(string Status, string? Reason, string Actor);

public sealed record StatusCounts(int Drafted, int Authorized, int Rejected, int Expired, int Denied, int Cancelled, int Total);

public sealed record GenTxBatchSummary(
    long GenTxBatchId,
    string ExternalBatchId,
    string CustomerId,
    string InitiatedBy,
    int ExpectedRowCount,
    long ExpectedAmountCents,
    string Status,
    string? StatusReason,
    DateTime CreatedAt,
    DateTime UpdatedAt,
    StatusCounts Counts);

public sealed record WireView(
    long WireId,
    string ExternalWireId,
    string ChunkId,
    int RowNumber,
    string OriginatingAccountMasked,
    DateOnly ProcessDate,
    long AmountCents,
    string Currency,
    string BeneficiaryName,
    string BeneficiaryAccountMasked,
    string BeneficiaryRouting,
    string? BeneficiaryBank,
    string? Reference,
    string? Subsidiary,
    string Status,
    string? RejectCode,
    string? RejectMessage,
    string? ApprovalRef,
    DateTime UpdatedAt);

public sealed record WiresPage(int Total, int Offset, int Limit, IReadOnlyList<WireView> Items);
