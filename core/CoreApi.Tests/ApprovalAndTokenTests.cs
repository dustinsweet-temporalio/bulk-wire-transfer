// Approval response shaping and portal-token validation, including the tenant boundary: a token issued for
// one FI is refused by the other FI's Core.
using System.Security.Claims;
using System.Text;
using CoreApi.Auth;
using CoreApi.Config;
using CoreApi.Contracts;
using Microsoft.IdentityModel.JsonWebTokens;
using Microsoft.IdentityModel.Tokens;

namespace CoreApi.Tests;

public sealed class ApprovalResponseTests
{
    private static readonly Signature Alex = new("alex", "Alex Rivera, Controller", new DateTime(2026, 11, 2, 10, 42, 0, DateTimeKind.Utc));

    [Fact]
    public void First_of_two_signatures_is_pending_second_approval()
    {
        var r = ApprovalResponses.From(new ApprovalRecord("PendingSecond", null, 2, null, null, null, [Alex], "summit-logistics"), OrchestrationOutcome.NotApplicable);
        Assert.Equal("PENDING_SECOND_APPROVAL", r.Status);
        Assert.Equal(2, r.RequiredApprovers);
        Assert.Single(r.Signatures);
        Assert.Equal("NOT_APPLICABLE", r.Orchestration);
    }

    [Fact]
    public void Approved_carries_the_reference_and_the_notification_outcome()
    {
        var r = ApprovalResponses.From(new ApprovalRecord("Approved", "APR-01JB", 1, null, null, DateTime.UtcNow, [Alex], "summit-logistics"), OrchestrationOutcome.NotifyFailed);
        Assert.Equal(("APPROVED", "APR-01JB", "NOTIFY_FAILED"), (r.Status, r.ApprovalRef, r.Orchestration));
    }

    [Theory]
    [InlineData(null, "NONE")]
    [InlineData("Denied", "DENIED")]
    public void Other_states_map_to_portal_statuses(string? db, string portal) =>
        Assert.Equal(portal, ApprovalResponses.PortalStatus(db));
}

public sealed class DemoTokenValidatorTests
{
    private const string Key = "test-signing-key-test-signing-key-test-signing-key";

    private static CoreOptions Options(string fi) => new()
    {
        FiId = fi, ConnectionString = "x", DemoTac = "246810", InternalToken = "t", AuthSigningKey = Key, BulkwireInternalUrl = "http://x",
    };

    private static string Mint(string fi, string key = Key, DateTime? expires = null) =>
        new JsonWebTokenHandler().CreateToken(new SecurityTokenDescriptor
        {
            Subject = new ClaimsIdentity([
                new Claim("sub", "jordan"), new Claim("fi", fi), new Claim("cust", "summit-logistics"),
                new Claim("role", "Initiator"), new Claim("name", "Jordan Ellis")]),
            Expires = expires ?? DateTime.UtcNow.AddHours(8),
            SigningCredentials = new SigningCredentials(new SymmetricSecurityKey(Encoding.UTF8.GetBytes(key)), SecurityAlgorithms.HmacSha256),
        });

    [Fact]
    public async Task Token_for_this_FI_is_accepted_with_user_from_claims()
    {
        var (outcome, user) = await new DemoTokenValidator(Options("lakeshore")).ValidateAsync("Bearer " + Mint("lakeshore"));
        Assert.Equal(TokenOutcome.Ok, outcome);
        Assert.Equal(("jordan", "summit-logistics", "Initiator"), (user!.UserId, user.CustomerId, user.Role));
    }

    [Fact]
    public async Task Token_for_another_FI_is_forbidden()
    {
        var (outcome, _) = await new DemoTokenValidator(Options("prairie")).ValidateAsync("Bearer " + Mint("lakeshore"));
        Assert.Equal(TokenOutcome.Forbidden, outcome);
    }

    [Fact]
    public async Task Wrong_signature_expired_or_missing_token_is_unauthorized()
    {
        var v = new DemoTokenValidator(Options("lakeshore"));
        Assert.Equal(TokenOutcome.Unauthorized, (await v.ValidateAsync("Bearer " + Mint("lakeshore", key: Key + "-other"))).Outcome);
        Assert.Equal(TokenOutcome.Unauthorized, (await v.ValidateAsync("Bearer " + Mint("lakeshore", expires: DateTime.UtcNow.AddHours(-1)))).Outcome);
        Assert.Equal(TokenOutcome.Unauthorized, (await v.ValidateAsync(null)).Outcome);
    }

    [Fact]
    public void Internal_token_compare()
    {
        Assert.True(InternalToken.Matches("abc", "abc"));
        Assert.False(InternalToken.Matches("abd", "abc"));
        Assert.False(InternalToken.Matches(null, "abc"));
    }
}
