// core-api composition root: one FI's core-banking stand-in ("Core").
//
// Thin endpoints over stored procedures (all data logic lives in SQL), an explicit 30 s SQL command
// timeout, and an explicit error contract that tells callers whether to retry. Two containers run this
// image, one per FI, each connected only to its own SQL Server.
//
// Middleware order (outermost first) and why:
//   RequestLog      one structured line per request, with the final status and error code
//   InFlight        DEMO-ONLY load stats; outside error handling so it sees the final status of every call
//   ErrorHandling   CoreException -> { code, message, retryable }
//   Fault           DEMO-ONLY injected faults/latency; inside error handling so injected errors use the
//                   same contract as real ones
using System.Text.Json;
using CoreApi.Auth;
using CoreApi.Config;
using CoreApi.Data;
using CoreApi.Demo;
using CoreApi.Endpoints;
using CoreApi.Orchestration;

var builder = WebApplication.CreateBuilder(args);
builder.Logging.ClearProviders();
builder.Logging.AddJsonConsole(o => o.JsonWriterOptions = new JsonWriterOptions { Indented = false });

var options = CoreOptions.FromConfiguration(builder.Configuration);
builder.Services.AddSingleton(options);
builder.Services.AddSingleton(TimeProvider.System);
builder.Services.AddSingleton<SqlExecutor>();
builder.Services.AddSingleton<Procs>();
builder.Services.AddSingleton<DemoTokenValidator>();
builder.Services.AddSingleton(sp => new FaultState(sp.GetRequiredService<TimeProvider>(), options.SimulatedLatencyMs));
builder.Services.AddSingleton<CallStats>();
builder.Services.AddTransient<CallbackFaultHandler>();

// Approval callbacks to bulkwire-service. The DEMO-ONLY fault handler is OUTSIDE the resilience handler so
// an injected failure is not retried away. Retrying a POST is safe here: the callback is idempotent
// because the workflow Update ID is the approvalRef. Total time budget 15 s.
builder.Services.AddHttpClient<BulkwireCallbackClient>(c => c.BaseAddress = new Uri(options.BulkwireInternalUrl))
    .AddHttpMessageHandler<CallbackFaultHandler>()
    .AddStandardResilienceHandler(r =>
    {
        r.TotalRequestTimeout.Timeout = TimeSpan.FromSeconds(15);
        r.AttemptTimeout.Timeout = TimeSpan.FromSeconds(5);
    });

var app = builder.Build();

app.UseMiddleware<RequestLogMiddleware>();
app.UseMiddleware<InFlightMiddleware>();
app.UseMiddleware<ErrorHandlingMiddleware>();
app.UseMiddleware<FaultMiddleware>();

AccountsEndpoints.Map(app);
BatchesEndpoints.Map(app);
ApprovalsEndpoints.Map(app);
AdminEndpoints.Map(app);   // DEMO-ONLY

// 503 while this FI's SQL Server is down or paused (the Control Center shows it).
app.MapGet("/healthz", async (SqlExecutor sql, CancellationToken ct) =>
    await sql.PingAsync(ct)
        ? Results.Ok(new { status = "ok", fiId = options.FiId })
        : Results.Json(new { status = "database unavailable", fiId = options.FiId }, statusCode: 503));

app.Run();
