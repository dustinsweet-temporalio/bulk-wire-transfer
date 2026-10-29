// DEMO-ONLY: the `callback-fail` fault (catalog F19). Sits OUTSIDE the resilience handler in the callback
// HttpClient pipeline, so an armed fault drops the approval notification without any retry, and the portal
// shows "Approval recorded. Release notification failed — Retry".
namespace CoreApi.Demo;

public sealed class CallbackFaultHandler(FaultState faults, ILogger<CallbackFaultHandler> log) : DelegatingHandler
{
    protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken ct)
    {
        if (request.RequestUri?.AbsolutePath == "/internal/approvals" && faults.TryConsume(FaultModes.CallbackFail))
        {
            log.LogWarning("DEMO-ONLY callback-fail fault: approval notification not delivered");
            throw new HttpRequestException("approval notification not delivered (DEMO-ONLY injected)");
        }
        return base.SendAsync(request, ct);
    }
}
