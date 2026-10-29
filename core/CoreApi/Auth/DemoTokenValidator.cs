// DemoTokenValidator: validates the portal's sign-in token for portal-facing Core routes.
//
// bulkwire-service mints an HS256 JWT at sign-in (claims: sub, fi, cust, role, name, exp) with
// AUTH_SIGNING_KEY. Core validates the same token and takes the user and customer from it, never from the
// request body. The `fi` claim must equal this Core's FI_ID: a Lakeshore token is refused by Prairie's Core.
// PRODUCTION NOTE: real deployments validate tokens issued by the FI's identity provider (OIDC), and use
// mTLS or workload identity between services.
using System.Security.Cryptography;
using System.Text;
using CoreApi.Config;
using Microsoft.IdentityModel.JsonWebTokens;
using Microsoft.IdentityModel.Tokens;

namespace CoreApi.Auth;

public sealed record PortalUser(string UserId, string FiId, string? CustomerId, string Role, string Name)
{
    public bool IsBankOps => Role == "BankOps";
}

public enum TokenOutcome { Ok, Unauthorized, Forbidden }

public sealed class DemoTokenValidator(CoreOptions options)
{
    private readonly JsonWebTokenHandler _handler = new();
    private readonly TokenValidationParameters _parameters = new()
    {
        ValidateIssuer = false,
        ValidateAudience = false,
        ValidateLifetime = true,
        ClockSkew = TimeSpan.FromSeconds(30),
        ValidAlgorithms = [SecurityAlgorithms.HmacSha256],
        IssuerSigningKey = new SymmetricSecurityKey(Encoding.UTF8.GetBytes(options.AuthSigningKey)),
    };

    public async Task<(TokenOutcome Outcome, PortalUser? User)> ValidateAsync(string? authorizationHeader)
    {
        if (authorizationHeader is null || !authorizationHeader.StartsWith("Bearer ", StringComparison.OrdinalIgnoreCase))
            return (TokenOutcome.Unauthorized, null);

        var result = await _handler.ValidateTokenAsync(authorizationHeader["Bearer ".Length..].Trim(), _parameters);
        if (!result.IsValid) return (TokenOutcome.Unauthorized, null);

        string? Claim(string name) => result.Claims.TryGetValue(name, out var v) ? v?.ToString() : null;
        var user = new PortalUser(Claim("sub") ?? "", Claim("fi") ?? "", Claim("cust") is { Length: > 0 } c ? c : null,
            Claim("role") ?? "", Claim("name") ?? "");

        if (user.UserId.Length == 0) return (TokenOutcome.Unauthorized, null);
        // A valid token for ANOTHER FI: authenticated, but not allowed at this FI's Core.
        if (user.FiId != options.FiId) return (TokenOutcome.Forbidden, user);
        return (TokenOutcome.Ok, user);
    }
}

public static class InternalToken
{
    // Constant-time comparison so the token cannot be guessed byte by byte from response timing.
    public static bool Matches(string? presented, string expected) =>
        presented is not null &&
        CryptographicOperations.FixedTimeEquals(Encoding.UTF8.GetBytes(presented), Encoding.UTF8.GetBytes(expected));
}
