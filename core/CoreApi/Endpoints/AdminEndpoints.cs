// DEMO-ONLY: admin API for control-service (the Control Center). Not routed by the gateway's portal
// blocks; requires the internal token. Fault injection, latency, holds, stats, and data reset.
using CoreApi.Config;
using CoreApi.Contracts;
using CoreApi.Data;
using CoreApi.Demo;

namespace CoreApi.Endpoints;

public static class AdminEndpoints
{
    public static void Map(WebApplication app)
    {
        var admin = app.MapGroup("/admin").RequireInternalToken();

        admin.MapGet("/state", async (CoreOptions options, FaultState faults, CallStats stats, Procs procs, CancellationToken ct) =>
        {
            // Holds are read from SQL; while SQL Server is down the rest of the state is still useful.
            IReadOnlyList<AccountView> holds;
            try { holds = (await procs.GetAccountsAsync(null, ct)).Where(a => a.Status != "Active").ToList(); }
            catch (CoreException) { holds = []; }
            return Results.Ok(new AdminState(options.FiId, faults.Snapshot(), faults.LatencyMs, holds, stats.Snapshot()));
        });

        admin.MapPost("/faults", (FaultRequest body, FaultState faults) =>
        {
            if (!FaultModes.All.Contains(body.Mode)) throw CoreException.BadRequest($"unknown fault mode '{body.Mode}'");
            faults.Set(body.Mode, body.DurationSeconds, body.Count);
            return Results.Ok(faults.Snapshot());
        });

        // "Simulated core processing time" per draft-chunk/authorize call.
        admin.MapPost("/latency", (LatencyRequest body, FaultState faults) =>
        {
            faults.LatencyMs = body.ChunkLatencyMs;
            return Results.Ok(new { chunkLatencyMs = faults.LatencyMs });
        });

        admin.MapPost("/accounts/{accountNumber}/hold", async (string accountNumber, HoldRequest body, Procs procs, CancellationToken ct) =>
            Results.Ok(await procs.SetAccountHoldAsync(accountNumber, body.OnHold, body.Reason, ct)));

        admin.MapGet("/stats", (CallStats stats) => Results.Ok(stats.Snapshot()));
        admin.MapPost("/stats/reset", (CallStats stats) =>
        {
            stats.Reset();
            return Results.Ok(stats.Snapshot());
        });

        admin.MapPost("/reset-data", async (Procs procs, FaultState faults, CallStats stats, CancellationToken ct) =>
        {
            await procs.DemoResetTransactionsAsync(ct);
            faults.Reset();
            stats.Reset();
            return Results.Ok(new { reset = true, chunkLatencyMs = faults.LatencyMs });
        });
    }
}
