// JSON contracts for account validation (staging), the portal's account views, and holds.
namespace CoreApi.Contracts;

public sealed record ValidateAccountsRequest(string CustomerId, IReadOnlyList<string> Accounts);
public sealed record AccountValidation(string Account, bool Exists, bool OwnedByCustomer, string? Status);
public sealed record ValidateAccountsResponse(IReadOnlyList<AccountValidation> Results);

// The full number is only an identifier in API calls; the UI displays accountNumberMasked.
public sealed record AccountView(
    string AccountNumber,
    string AccountNumberMasked,
    string CustomerId,
    string CustomerName,
    string Nickname,
    string Status,
    string? HoldReason,
    long AvailableBalanceCents,
    long DailyWireLimitCents);

public sealed record HoldRequest(bool OnHold, string? Reason);

public static class Masking
{
    // '4401220033' -> '••0033' (the display rule everywhere in the portal).
    public static string Mask(string? account) =>
        string.IsNullOrEmpty(account) ? "" : "••" + (account.Length <= 4 ? account : account[^4..]);
}
