// GenTxBatch endpoints. The POST routes are called by the bulk wire Activities (internal token) and each runs
// exactly one idempotent stored procedure; the GET routes are the portal's view of the system of record.
using CoreApi.Contracts;
using CoreApi.Data;

namespace CoreApi.Endpoints;

public static class BatchesEndpoints
{
    private const int MaxWiresPerChunk = 1000;
    private const int MaxWiresPage = 200;

    private static readonly HashSet<string> Statuses =
        ["Drafting", "Drafted", "Authorizing", "Authorized", "Expired", "Denied", "Cancelled", "Rejected"];

    public static void Map(WebApplication app)
    {
        var activities = app.MapGroup("/api/gentx-batches").RequireInternalToken();

        // Activity EnsureGenTxBatch.
        activities.MapPost("", (EnsureGenTxBatchRequest body, Procs procs, CancellationToken ct) =>
            procs.EnsureGenTxBatchAsync(body, ct));

        // Activity CheckGroupPolicy. A DENY is a 200 result (business outcome), not an error.
        activities.MapPost("/{genTxBatchId:long}/policy-checks", (long genTxBatchId, PolicyCheckRequest body, Procs procs, CancellationToken ct) =>
            procs.CheckGroupLimitAsync(genTxBatchId, body, ct));

        // Activity InsertDraftChunk: one TVP call per chunk.
        activities.MapPost("/{genTxBatchId:long}/draft-chunks", (long genTxBatchId, DraftChunkRequest body, Procs procs, CancellationToken ct) =>
        {
            if (body.Wires is null || body.Wires.Count == 0) throw CoreException.BadRequest("a chunk needs at least one wire");
            if (body.Wires.Count > MaxWiresPerChunk) throw CoreException.BadRequest($"a chunk holds at most {MaxWiresPerChunk} wires");
            if (string.IsNullOrWhiteSpace(body.ChunkId) || body.ChunkId.Length > 16) throw CoreException.BadRequest("chunkId is required (max 16 chars)");
            return procs.InsertDraftWiresAsync(genTxBatchId, body, ct);
        });

        // Activity AuthorizeChunk.
        activities.MapPost("/{genTxBatchId:long}/chunks/{chunkId}/authorize", (long genTxBatchId, string chunkId, AuthorizeChunkRequest body, Procs procs, CancellationToken ct) =>
            procs.AuthorizeChunkAsync(genTxBatchId, chunkId, body, ct));

        // Activity RejectChunk (operator decision during the authorize phase).
        activities.MapPost("/{genTxBatchId:long}/chunks/{chunkId}/reject", (long genTxBatchId, string chunkId, RejectChunkRequest body, Procs procs, CancellationToken ct) =>
            procs.RejectChunkAsync(genTxBatchId, chunkId, body, ct));

        // Activity SetGenTxBatchStatus.
        activities.MapPost("/{genTxBatchId:long}/status", (long genTxBatchId, SetStatusRequest body, Procs procs, CancellationToken ct) =>
        {
            if (!Statuses.Contains(body.Status)) throw CoreException.BadRequest($"unknown status '{body.Status}'");
            return procs.SetGenTxBatchStatusAsync(genTxBatchId, body, ct);
        });

        // Portal: Core's record for a bulkwire batch (404 before the workflow has created it).
        app.MapGet("/api/gentx-batches/by-external/{batchId}", async (HttpContext http, string batchId, Procs procs, CancellationToken ct) =>
        {
            var user = http.PortalUser();
            var summary = await procs.GetGenTxBatchByExternalIdAsync(batchId, ct);
            if (summary is null || (!user.IsBankOps && summary.CustomerId != user.CustomerId))
                throw CoreException.NotFound("GENTX_BATCH_NOT_FOUND", "Core has no record of this batch yet");
            return Results.Ok(summary);
        }).RequirePortalUser();

        // Portal: the Wires tab, read straight from the system of record.
        app.MapGet("/api/gentx-batches/{genTxBatchId:long}/wires", async (HttpContext http, long genTxBatchId, string? status, int? offset, int? limit, Procs procs, CancellationToken ct) =>
        {
            var user = http.PortalUser();
            if (status is not null && !Statuses.Contains(status)) throw CoreException.BadRequest($"unknown status '{status}'");
            var page = await procs.GetWiresPageAsync(genTxBatchId, user.IsBankOps ? null : user.CustomerId, status,
                Math.Max(0, offset ?? 0), Math.Clamp(limit ?? 50, 1, MaxWiresPage), ct);
            return Results.Ok(page);
        }).RequirePortalUser();
    }
}
