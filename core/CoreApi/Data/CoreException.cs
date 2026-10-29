// CoreException: Core's error contract as an exception. ErrorHandlingMiddleware turns it into
// HTTP {status} with body { code, message, retryable }.
//
// `retryable` is what the bulk wire Activities classify on: retryable errors (503/504/500) are retried by
// Temporal with backoff; non-retryable ones (422/409/404/400) park the chunk for a human.
namespace CoreApi.Data;

public sealed class CoreException(int status, string code, string message, bool retryable, Exception? inner = null)
    : Exception(message, inner)
{
    public int Status { get; } = status;
    public string Code { get; } = code;
    public bool Retryable { get; } = retryable;

    public static CoreException BadRequest(string message) => new(400, "CORE_CONTRACT", message, retryable: false);
    public static CoreException NotFound(string code, string message) => new(404, code, message, retryable: false);
}
