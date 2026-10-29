// HTTP plumbing shared by every endpoint: error contract, structured request logs, and the two kinds of
// caller authentication (service-to-service internal token, portal user token).
using CoreApi.Auth;
using CoreApi.Config;
using CoreApi.Contracts;
using CoreApi.Data;

namespace CoreApi.Endpoints;

public static class RequestItems
{
    public const string ErrorCode = "core.errorCode";
    public const string PortalUser = "core.portalUser";
}

// Turns CoreException (and anything unexpected) into { code, message, retryable } with the mapped status.
public sealed class ErrorHandlingMiddleware(RequestDelegate next, ILogger<ErrorHandlingMiddleware> log)
{
    public async Task InvokeAsync(HttpContext http)
    {
        try
        {
            await next(http);
        }
        catch (CoreException ex)
        {
            await WriteAsync(http, ex.Status, new ErrorResponse(ex.Code, ex.Message, ex.Retryable));
        }
        catch (BadHttpRequestException ex)
        {
            await WriteAsync(http, 400, new ErrorResponse("CORE_CONTRACT", ex.Message, false));
        }
        catch (OperationCanceledException) when (http.RequestAborted.IsCancellationRequested)
        {
            // The caller (e.g., an Activity attempt past its StartToClose) went away; nothing to answer.
        }
        catch (Exception ex)
        {
            log.LogError(ex, "unhandled error");
            await WriteAsync(http, 500, new ErrorResponse("CORE_INTERNAL", "Unexpected Core error", true));
        }
    }

    private static async Task WriteAsync(HttpContext http, int status, ErrorResponse body)
    {
        http.Items[RequestItems.ErrorCode] = body.Code;
        if (http.Response.HasStarted) return;
        http.Response.Clear();
        http.Response.StatusCode = status;
        await http.Response.WriteAsJsonAsync(body);
    }
}

// One structured log line per request: FI, route TEMPLATE (never the raw path, which can contain an
// account number), status, duration, error code.
public sealed class RequestLogMiddleware(RequestDelegate next, CoreOptions options, ILogger<RequestLogMiddleware> log)
{
    public async Task InvokeAsync(HttpContext http)
    {
        var started = System.Diagnostics.Stopwatch.GetTimestamp();
        try
        {
            await next(http);
        }
        finally
        {
            var route = (http.GetEndpoint() as RouteEndpoint)?.RoutePattern.RawText ?? "(unmatched)";
            if (route != "/healthz")
                log.LogInformation("fi={Fi} {Method} {Route} status={Status} ms={Ms} code={Code}",
                    options.FiId, http.Request.Method, route, http.Response.StatusCode,
                    (long)System.Diagnostics.Stopwatch.GetElapsedTime(started).TotalMilliseconds,
                    http.Items[RequestItems.ErrorCode] as string ?? "-");
        }
    }
}

public static class EndpointAuth
{
    public const string InternalTokenHeader = "X-Internal-Token";

    // Service-to-service routes (bulkwire-service staging, Activities, control-service admin).
    public static TBuilder RequireInternalToken<TBuilder>(this TBuilder builder) where TBuilder : IEndpointConventionBuilder =>
        builder.AddEndpointFilter(async (ctx, next) =>
        {
            var options = ctx.HttpContext.RequestServices.GetRequiredService<CoreOptions>();
            var presented = ctx.HttpContext.Request.Headers[InternalTokenHeader].ToString();
            if (!InternalToken.Matches(presented, options.InternalToken))
                throw new CoreException(401, "UNAUTHORIZED", "missing or invalid internal token", false);
            return await next(ctx);
        });

    // Portal routes: a valid sign-in token for THIS FI. The user and customer come from the token only.
    public static TBuilder RequirePortalUser<TBuilder>(this TBuilder builder) where TBuilder : IEndpointConventionBuilder =>
        builder.AddEndpointFilter(async (ctx, next) =>
        {
            var validator = ctx.HttpContext.RequestServices.GetRequiredService<DemoTokenValidator>();
            var (outcome, user) = await validator.ValidateAsync(ctx.HttpContext.Request.Headers.Authorization.ToString());
            switch (outcome)
            {
                case TokenOutcome.Unauthorized:
                    throw new CoreException(401, "UNAUTHORIZED", "sign in again", false);
                case TokenOutcome.Forbidden:
                    throw new CoreException(403, "WRONG_FI", "this token was issued for a different financial institution", false);
            }
            ctx.HttpContext.Items[RequestItems.PortalUser] = user;
            return await next(ctx);
        });

    public static PortalUser PortalUser(this HttpContext http) =>
        http.Items[RequestItems.PortalUser] as PortalUser ?? throw new InvalidOperationException("portal user filter missing");
}
