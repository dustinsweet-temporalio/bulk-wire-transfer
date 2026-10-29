// DEMO-ONLY: applies the injected faults and the simulated processing latency to the Activity-facing
// write routes (POST /api/gentx-batches/**). Portal routes are never affected, so the portal keeps working
// during an "Core outage" exactly as a core's customer-facing reads might.
using CoreApi.Contracts;
using CoreApi.Data;

namespace CoreApi.Demo;

public sealed class FaultMiddleware(RequestDelegate next, FaultState faults, ILogger<FaultMiddleware> log)
{
    public async Task InvokeAsync(HttpContext http, Procs procs)
    {
        var op = ActivityRoutes.Classify(http.Request);
        if (op is null)
        {
            await next(http);
            return;
        }

        if (faults.IsOutageActive())
            throw new CoreException(503, "CORE_UNAVAILABLE", "Core is unavailable (DEMO-ONLY injected outage)", retryable: true);

        if (faults.TryConsume(FaultModes.Error500))
            throw new CoreException(500, "CORE_INTERNAL", "Unexpected Core error (DEMO-ONLY injected 500)", retryable: true);

        if (op is ActivityRoutes.DraftChunk or ActivityRoutes.AuthorizeChunk)
        {
            // DEMO-ONLY: "simulated core processing time". A FIXED delay per chunk call, independent of how
            // many calls are in flight, so progress is watchable without misrepresenting concurrency effects.
            if (faults.LatencyMs > 0) await Task.Delay(faults.LatencyMs, http.RequestAborted);

            // DEMO-ONLY: a REAL SQL timeout. The long-running procedure runs under the normal 30 s command
            // timeout, SqlClient raises SqlException -2, and SqlErrorMapper turns it into 504 SQL_TIMEOUT.
            // Nothing is written for this call.
            if (faults.TryConsume(FaultModes.SqlTimeout))
            {
                log.LogWarning("DEMO-ONLY sql-timeout fault: running a 35 s statement under the 30 s command timeout");
                await procs.SimulateLongRunningLockAsync(http.RequestAborted);
            }
        }

        if (op == ActivityRoutes.DraftChunk && faults.IsArmed(FaultModes.LoseResponse))
        {
            await LoseResponseAfterCommitAsync(http);
            return;
        }

        await next(http);
    }

    // DEMO-ONLY: let the endpoint run and COMMIT, then throw its 200 response away and answer 503, as if the
    // network dropped the reply. The Activity retries; usp_InsertDraftWires reports alreadyPresent.
    private async Task LoseResponseAfterCommitAsync(HttpContext http)
    {
        var original = http.Response.Body;
        await using var buffer = new MemoryStream();
        http.Response.Body = buffer;
        try
        {
            await next(http);
        }
        finally
        {
            http.Response.Body = original;
        }

        if (http.Response.StatusCode == StatusCodes.Status200OK && faults.TryConsume(FaultModes.LoseResponse))
        {
            log.LogWarning("DEMO-ONLY lose-response fault: chunk committed, response discarded");
            http.Response.Clear();
            throw new CoreException(503, "CORE_UNAVAILABLE", "Response lost after commit (DEMO-ONLY injected)", retryable: true);
        }

        buffer.Position = 0;
        await buffer.CopyToAsync(original, http.RequestAborted);
    }
}

// Which Activity-facing operation a request is (null = not an Activity call).
public static class ActivityRoutes
{
    public const string EnsureGenTxBatch = "ensure-gentx-batch";
    public const string PolicyCheck = "policy-check";
    public const string DraftChunk = "draft-chunk";
    public const string AuthorizeChunk = "authorize-chunk";
    public const string RejectChunk = "reject-chunk";
    public const string SetStatus = "set-status";

    public static string? Classify(HttpRequest request)
    {
        if (!HttpMethods.IsPost(request.Method)) return null;
        var path = request.Path.Value ?? "";
        if (!path.StartsWith("/api/gentx-batches", StringComparison.Ordinal)) return null;
        if (path == "/api/gentx-batches") return EnsureGenTxBatch;
        if (path.EndsWith("/policy-checks", StringComparison.Ordinal)) return PolicyCheck;
        if (path.EndsWith("/draft-chunks", StringComparison.Ordinal)) return DraftChunk;
        if (path.EndsWith("/authorize", StringComparison.Ordinal)) return AuthorizeChunk;
        if (path.EndsWith("/reject", StringComparison.Ordinal)) return RejectChunk;
        if (path.EndsWith("/status", StringComparison.Ordinal)) return SetStatus;
        return null;
    }
}
