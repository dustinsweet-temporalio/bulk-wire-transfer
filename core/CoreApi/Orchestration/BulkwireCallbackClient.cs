// BulkwireCallbackClient: Core -> bulkwire-service. Notifies the orchestration layer that a batch was fully
// approved or denied, and looks up a staged batch's owner and totals for decisions made before the draft.
//
// Core owns the approval decision. Once it is final, Core tells the orchestration layer, which delivers an
// `authorize` (or `deny`) Update to the batch's workflow with Update ID = approvalRef. Because of that
// Update ID, sending the same callback twice is harmless: the portal's "Retry notification" button and the
// resilience handler's retries both rely on it.
//
// PRODUCTION NOTE: a real core writes the approval and an outbox row in one transaction and delivers
// callbacks from the outbox, so a crash between "approved" and "notified" cannot lose the notification.
using System.Net.Http.Json;
using CoreApi.Config;
using CoreApi.Contracts;
using CoreApi.Data;

namespace CoreApi.Orchestration;

public sealed class BulkwireCallbackClient(HttpClient http, CoreOptions options, ILogger<BulkwireCallbackClient> log)
{
    public const string InternalTokenHeader = "X-Internal-Token";

    public Task<string> NotifyApprovedAsync(ApprovedCallback body, CancellationToken ct) =>
        SendAsync("/internal/approvals", body, body.BatchId, ct);

    public Task<string> NotifyDeniedAsync(DeniedCallback body, CancellationToken ct) =>
        SendAsync("/internal/denials", body, body.BatchId, ct);

    // The staged batch (null if it was never staged or has expired). Used only before Core has a batch record of
    // its own, so that the amount behind approval limits and dual control is computed server side.
    public async Task<StagedBatch?> GetStagedBatchAsync(string batchId, CancellationToken ct)
    {
        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Get,
                $"/internal/staged-batches/{Uri.EscapeDataString(options.FiId)}/{Uri.EscapeDataString(batchId)}");
            request.Headers.Add(InternalTokenHeader, options.InternalToken);
            using var response = await http.SendAsync(request, ct);
            if (response.StatusCode == System.Net.HttpStatusCode.NotFound) return null;
            response.EnsureSuccessStatusCode();
            return await response.Content.ReadFromJsonAsync<StagedBatch>(ct);
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException or TimeoutException
                                       or Polly.Timeout.TimeoutRejectedException)
        {
            log.LogWarning("staged batch lookup for {BatchId} failed: {Error}", batchId, ex.Message);
            throw new CoreException(503, "ORCHESTRATION_UNAVAILABLE", "The batch cannot be checked right now. Try again.", true, ex);
        }
    }

    private async Task<string> SendAsync<T>(string path, T body, string batchId, CancellationToken ct)
    {
        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Post, path) { Content = JsonContent.Create(body) };
            request.Headers.Add(InternalTokenHeader, options.InternalToken);
            using var response = await http.SendAsync(request, ct);
            if (response.IsSuccessStatusCode) return OrchestrationOutcome.Notified;

            log.LogWarning("callback {Path} for batch {BatchId} returned {Status}", path, batchId, (int)response.StatusCode);
            return OrchestrationOutcome.NotifyFailed;
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException or TimeoutException
                                       or Polly.Timeout.TimeoutRejectedException)
        {
            log.LogWarning("callback {Path} for batch {BatchId} failed: {Error}", path, batchId, ex.Message);
            return OrchestrationOutcome.NotifyFailed;
        }
    }
}
