// Procs: one method per stored procedure (sql/03-procs.sql). Binds parameters, reads result sets, and
// returns contracts. No SQL text lives here besides procedure names: all data logic is in the database.
using System.Data;
using CoreApi.Contracts;
using Microsoft.Data.SqlClient;

namespace CoreApi.Data;

public sealed class Procs(SqlExecutor sql)
{
    // ---- Staging (bulkwire-service) ----------------------------------------------------------------

    public Task<ValidateAccountsResponse> ValidateAccountsAsync(string customerId, IReadOnlyCollection<string> accounts, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_ValidateAccounts",
            p =>
            {
                p.Add(new SqlParameter("@CustomerId", SqlDbType.VarChar, 64) { Value = customerId });
                p.Add(Tvp.AccountNumbers("@Accounts", accounts));
            },
            async (r, c) =>
            {
                var results = new List<AccountValidation>();
                while (await r.ReadAsync(c))
                    results.Add(new AccountValidation(
                        r.GetString("AccountNumber"), r.GetBoolean("Exists"), r.GetBoolean("OwnedByCustomer"), NullableString(r, "Status")));
                return new ValidateAccountsResponse(results);
            }, ct);

    // ---- Activities ---------------------------------------------------------------------------------

    public Task<EnsureGenTxBatchResponse> EnsureGenTxBatchAsync(EnsureGenTxBatchRequest q, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_EnsureGenTxBatch",
            p =>
            {
                p.AddWithValue("@ExternalBatchId", q.ExternalBatchId);
                p.AddWithValue("@CustomerId", q.CustomerId);
                p.AddWithValue("@InitiatedBy", q.InitiatedBy);
                p.AddWithValue("@RowCount", q.RowCount);
                p.AddWithValue("@AmountCents", q.AmountCents);
            },
            async (r, c) =>
            {
                await r.ReadAsync(c);
                return new EnsureGenTxBatchResponse(r.GetInt64("GenTxBatchId"), r.GetBoolean("Created"), r.GetString("Status"));
            }, ct);

    public Task<PolicyCheckResponse> CheckGroupLimitAsync(long genTxBatchId, PolicyCheckRequest q, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_CheckGroupLimit",
            p =>
            {
                p.AddWithValue("@GenTxBatchId", genTxBatchId);
                p.AddWithValue("@OriginatingAccount", q.OriginatingAccount);
                p.Add(new SqlParameter("@ProcessDate", SqlDbType.Date) { Value = q.ProcessDate.ToDateTime(TimeOnly.MinValue) });
                p.AddWithValue("@AmountCents", q.AmountCents);
            },
            async (r, c) =>
            {
                await r.ReadAsync(c);
                return new PolicyCheckResponse(r.GetString("Decision"), NullableString(r, "Code"),
                    r.GetInt64("LimitCents"), r.GetInt64("UsedCents"), r.GetInt64("RequestedCents"));
            }, ct);

    public Task<DraftChunkResponse> InsertDraftWiresAsync(long genTxBatchId, DraftChunkRequest q, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_InsertDraftWires",
            p =>
            {
                p.AddWithValue("@GenTxBatchId", genTxBatchId);
                p.AddWithValue("@ChunkId", q.ChunkId);
                p.AddWithValue("@Actor", q.Actor);
                p.Add(new SqlParameter("@ForceRejectCode", SqlDbType.VarChar, 40) { Value = (object?)q.ForceReject?.Code ?? DBNull.Value });
                p.Add(new SqlParameter("@ForceRejectMessage", SqlDbType.NVarChar, 200) { Value = (object?)q.ForceReject?.Message ?? DBNull.Value });
                p.Add(Tvp.WireDraft("@Wires", q.Wires.ToList()));
            },
            async (r, c) =>
            {
                // Result set 1: chunk-level totals. Result set 2: this chunk's rejected wires.
                await r.ReadAsync(c);
                int drafted = r.GetInt32("ChunkDrafted"), rejected = r.GetInt32("ChunkRejected");
                int inserted = r.GetInt32("InsertedThisCall"), already = r.GetInt32("AlreadyPresent");
                await r.NextResultAsync(c);
                var list = new List<RejectedWire>();
                while (await r.ReadAsync(c))
                    list.Add(new RejectedWire(r.GetInt32("RowNumber"), r.GetString("ExternalWireId"),
                        r.GetString("RejectCode"), NullableString(r, "RejectMessage")));
                return new DraftChunkResponse(drafted, rejected, inserted, already, list);
            }, ct);

    public Task<ChunkTotalsResponse> AuthorizeChunkAsync(long genTxBatchId, string chunkId, AuthorizeChunkRequest q, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_AuthorizeChunk",
            p =>
            {
                p.AddWithValue("@GenTxBatchId", genTxBatchId);
                p.AddWithValue("@ChunkId", chunkId);
                p.AddWithValue("@ApprovalRef", q.ApprovalRef);
                p.AddWithValue("@Actor", q.Actor);
            },
            ReadChunkTotals, ct);

    public Task<ChunkTotalsResponse> RejectChunkAsync(long genTxBatchId, string chunkId, RejectChunkRequest q, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_RejectChunk",
            p =>
            {
                p.AddWithValue("@GenTxBatchId", genTxBatchId);
                p.AddWithValue("@ChunkId", chunkId);
                p.AddWithValue("@Code", q.Code);
                p.Add(new SqlParameter("@Message", SqlDbType.NVarChar, 200) { Value = (object?)q.Message ?? DBNull.Value });
                p.AddWithValue("@Actor", q.Actor);
            },
            ReadChunkTotals, ct);

    public Task<GenTxBatchSummary> SetGenTxBatchStatusAsync(long genTxBatchId, SetStatusRequest q, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_SetGenTxBatchStatus",
            p =>
            {
                p.AddWithValue("@GenTxBatchId", genTxBatchId);
                p.AddWithValue("@Status", q.Status);
                p.Add(new SqlParameter("@Reason", SqlDbType.NVarChar, 200) { Value = (object?)q.Reason ?? DBNull.Value });
                p.AddWithValue("@Actor", q.Actor);
            },
            async (r, c) => await ReadSummaryAsync(r, c)
                ?? throw CoreException.NotFound("GENTX_BATCH_NOT_FOUND", $"Core batch {genTxBatchId} does not exist"), ct);

    // ---- Portal reads -------------------------------------------------------------------------------

    public Task<GenTxBatchSummary?> GetGenTxBatchByExternalIdAsync(string externalBatchId, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_GetGenTxBatchByExternalId",
            p => p.AddWithValue("@ExternalBatchId", externalBatchId),
            ReadSummaryAsync, ct);

    public Task<WiresPage> GetWiresPageAsync(long genTxBatchId, string? customerId, string? status, int offset, int limit, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_GetWiresPage",
            p =>
            {
                p.AddWithValue("@GenTxBatchId", genTxBatchId);
                p.Add(new SqlParameter("@CustomerId", SqlDbType.VarChar, 64) { Value = (object?)customerId ?? DBNull.Value });
                p.Add(new SqlParameter("@Status", SqlDbType.VarChar, 20) { Value = (object?)status ?? DBNull.Value });
                p.AddWithValue("@Offset", offset);
                p.AddWithValue("@Limit", limit);
            },
            async (r, c) =>
            {
                var items = new List<WireView>();
                while (await r.ReadAsync(c))
                    items.Add(new WireView(
                        r.GetInt64("WireId"), r.GetString("ExternalWireId"), r.GetString("ChunkId"), r.GetInt32("RowNumber"),
                        Masking.Mask(r.GetString("OriginatingAccount")), DateOnly.FromDateTime(r.GetDateTime("ProcessDate")),
                        r.GetInt64("AmountCents"), r.GetString("Currency"), r.GetString("BeneficiaryName"),
                        Masking.Mask(r.GetString("BeneficiaryAccount")), r.GetString("BeneficiaryRouting"),
                        NullableString(r, "BeneficiaryBank"), NullableString(r, "Reference"), NullableString(r, "Subsidiary"),
                        r.GetString("Status"), NullableString(r, "RejectCode"), NullableString(r, "RejectMessage"),
                        NullableString(r, "ApprovalRef"), Utc(r.GetDateTime("UpdatedAt"))));
                await r.NextResultAsync(c);
                await r.ReadAsync(c);
                return new WiresPage(r.GetInt32("Total"), offset, limit, items);
            }, ct);

    public Task<IReadOnlyList<AccountView>> GetAccountsAsync(string? customerId, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_GetCustomerAccounts",
            p => p.Add(new SqlParameter("@CustomerId", SqlDbType.VarChar, 64) { Value = (object?)customerId ?? DBNull.Value }),
            async (r, c) =>
            {
                var list = new List<AccountView>();
                while (await r.ReadAsync(c)) list.Add(ReadAccount(r));
                return (IReadOnlyList<AccountView>)list;
            }, ct);

    public Task<AccountView> SetAccountHoldAsync(string accountNumber, bool onHold, string? reason, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_SetAccountHold",
            p =>
            {
                p.AddWithValue("@AccountNumber", accountNumber);
                p.AddWithValue("@OnHold", onHold);
                p.Add(new SqlParameter("@Reason", SqlDbType.NVarChar, 200) { Value = (object?)reason ?? DBNull.Value });
            },
            async (r, c) =>
            {
                await r.ReadAsync(c);
                return ReadAccount(r);
            }, ct);

    // ---- Approvals ----------------------------------------------------------------------------------

    public Task<ApprovalRecord> RecordApprovalSignatureAsync(string batchId, string customerId, string userId,
        string initiatedBy, long amountCents, long dualControlThresholdCents, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_RecordApprovalSignature",
            p =>
            {
                p.AddWithValue("@ExternalBatchId", batchId);
                p.AddWithValue("@CustomerId", customerId);
                p.AddWithValue("@UserId", userId);
                p.AddWithValue("@InitiatedBy", initiatedBy);
                p.AddWithValue("@AmountCents", amountCents);
                p.AddWithValue("@DualControlThresholdCents", dualControlThresholdCents);
            },
            ReadApprovalAsync, ct);

    public Task<ApprovalRecord> RecordDenialAsync(string batchId, string customerId, string userId, string? reason, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_RecordDenial",
            p =>
            {
                p.AddWithValue("@ExternalBatchId", batchId);
                p.AddWithValue("@CustomerId", customerId);
                p.AddWithValue("@UserId", userId);
                p.Add(new SqlParameter("@Reason", SqlDbType.NVarChar, 200) { Value = (object?)reason ?? DBNull.Value });
            },
            ReadApprovalAsync, ct);

    public Task<ApprovalRecord> GetApprovalAsync(string batchId, CancellationToken ct) =>
        sql.RunAsync("dbo.usp_GetApproval", p => p.AddWithValue("@ExternalBatchId", batchId), ReadApprovalAsync, ct);

    // ---- DEMO-ONLY ----------------------------------------------------------------------------------

    public Task DemoResetTransactionsAsync(CancellationToken ct) =>
        sql.ExecuteAsync("dbo.usp_DemoResetTransactions", _ => { }, ct);

    // DEMO-ONLY: runs WAITFOR DELAY '00:00:35' under the normal 30 s command timeout -> real SqlException -2.
    public Task SimulateLongRunningLockAsync(CancellationToken ct) =>
        sql.ExecuteAsync("dbo.usp_SimulateLongRunningLock", _ => { }, ct);

    // ---- Readers ------------------------------------------------------------------------------------

    private static async Task<ChunkTotalsResponse> ReadChunkTotals(SqlDataReader r, CancellationToken c)
    {
        await r.ReadAsync(c);
        return new ChunkTotalsResponse(r.GetInt32("ChunkAuthorized"), r.GetInt32("ChunkRejected"), r.GetInt32("FlippedThisCall"));
    }

    private static async Task<GenTxBatchSummary?> ReadSummaryAsync(SqlDataReader r, CancellationToken c)
    {
        if (!await r.ReadAsync(c)) return null;
        return new GenTxBatchSummary(
            r.GetInt64("GenTxBatchId"), r.GetString("ExternalBatchId"), r.GetString("CustomerId"), r.GetString("InitiatedBy"),
            r.GetInt32("ExpectedRowCount"), r.GetInt64("ExpectedAmountCents"), r.GetString("Status"), NullableString(r, "StatusReason"),
            Utc(r.GetDateTime("CreatedAt")), Utc(r.GetDateTime("UpdatedAt")),
            new StatusCounts(r.GetInt32("DraftedCount"), r.GetInt32("AuthorizedCount"), r.GetInt32("RejectedCount"),
                r.GetInt32("ExpiredCount"), r.GetInt32("DeniedCount"), r.GetInt32("CancelledCount"), r.GetInt32("WireCount")));
    }

    private static async Task<ApprovalRecord> ReadApprovalAsync(SqlDataReader r, CancellationToken c)
    {
        string? status = null, approvalRef = null, deniedBy = null, denyReason = null, customerId = null;
        int required = 0;
        DateTime? completedAt = null;
        if (await r.ReadAsync(c))
        {
            status = r.GetString("Status");
            approvalRef = NullableString(r, "ApprovalRef");
            required = r.GetByte("RequiredApprovers");
            deniedBy = NullableString(r, "DeniedBy");
            denyReason = NullableString(r, "DenyReason");
            completedAt = r.IsDBNull("CompletedAt") ? null : Utc(r.GetDateTime("CompletedAt"));
            customerId = r.GetString("CustomerId");
        }
        await r.NextResultAsync(c);
        var signatures = new List<Signature>();
        while (await r.ReadAsync(c))
            signatures.Add(new Signature(r.GetString("UserId"), r.GetString("DisplayName"), Utc(r.GetDateTime("SignedAt"))));
        return new ApprovalRecord(status, approvalRef, required, deniedBy, denyReason, completedAt, signatures, customerId);
    }

    private static AccountView ReadAccount(SqlDataReader r) =>
        new(r.GetString("AccountNumber"), Masking.Mask(r.GetString("AccountNumber")), r.GetString("CustomerId"),
            r.GetString("CustomerName"), r.GetString("Nickname"), r.GetString("Status"), NullableString(r, "HoldReason"),
            r.GetInt64("AvailableBalanceCents"), r.GetInt64("DailyWireLimitCents"));

    private static string? NullableString(SqlDataReader r, string column) => r.IsDBNull(column) ? null : r.GetString(column);

    // SQL Server datetime2 has no time zone; every Core timestamp is written with SYSUTCDATETIME(), so mark it UTC
    // (JSON then carries the "Z" that other services' RFC 3339 parsers require).
    private static DateTime Utc(DateTime d) => DateTime.SpecifyKind(d, DateTimeKind.Utc);
}
