// Account endpoints: staging validation (internal, TVP), the portal's account list, and holds placed or
// released by bank operations staff.
using CoreApi.Contracts;
using CoreApi.Data;

namespace CoreApi.Endpoints;

public static class AccountsEndpoints
{
    public static void Map(WebApplication app)
    {
        // Internal: bulkwire-service validates every distinct originating account of an upload in ONE call.
        app.MapPost("/api/accounts:validate", async (ValidateAccountsRequest body, Procs procs, CancellationToken ct) =>
        {
            var accounts = body.Accounts.Where(a => !string.IsNullOrWhiteSpace(a)).Distinct().ToList();
            if (accounts.Count == 0) return Results.Ok(new ValidateAccountsResponse([]));
            if (accounts.Any(a => a.Length > 17)) throw CoreException.BadRequest("account numbers are at most 17 characters");
            return Results.Ok(await procs.ValidateAccountsAsync(body.CustomerId, accounts, ct));
        }).RequireInternalToken();

        // Portal: the signed-in customer's accounts. Bank operations staff may pass ?customerId=, or omit it
        // to see every account at this FI (their accounts tab groups them by customer).
        app.MapGet("/api/accounts", async (HttpContext http, string? customerId, Procs procs, CancellationToken ct) =>
        {
            var user = http.PortalUser();
            var scope = user.IsBankOps ? customerId : user.CustomerId
                ?? throw new CoreException(403, "FORBIDDEN", "no customer on this sign-in", false);
            return Results.Ok(await procs.GetAccountsAsync(scope, ct));
        }).RequirePortalUser();

        // Portal, BankOps only: the legitimate bank-staff action that creates and clears the "account on
        // hold" condition used in the demo (catalog F21, F22).
        app.MapPost("/api/accounts/{accountNumber}/hold", async (HttpContext http, string accountNumber, HoldRequest body, Procs procs, CancellationToken ct) =>
        {
            if (!http.PortalUser().IsBankOps)
                throw new CoreException(403, "FORBIDDEN", "only bank operations staff can place or release holds", false);
            return Results.Ok(await procs.SetAccountHoldAsync(accountNumber, body.OnHold, body.Reason, ct));
        }).RequirePortalUser();
    }
}
