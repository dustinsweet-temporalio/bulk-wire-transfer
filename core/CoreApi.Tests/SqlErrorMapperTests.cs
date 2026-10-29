// SqlException number -> HTTP contract. This table decides whether a failure is retried by Temporal
// (transient) or parks a chunk for a human (structural), so every row is pinned by a test.
using CoreApi.Data;

namespace CoreApi.Tests;

public sealed class SqlErrorMapperTests
{
    [Theory]
    [InlineData(51001, "ACCOUNT_ON_HOLD: account ending 0033 is on hold", 422, "ACCOUNT_ON_HOLD", false)]
    [InlineData(51002, "ACCOUNT_CLOSED: account ending 0033 is closed", 422, "ACCOUNT_CLOSED", false)]
    [InlineData(51003, "ACCOUNT_NOT_FOUND: account ending 9999 is not known", 422, "ACCOUNT_NOT_FOUND", false)]
    [InlineData(51010, "GENTX_BATCH_CLOSED: Core batch 100000 is Drafted", 409, "GENTX_BATCH_CLOSED", false)]
    [InlineData(51011, "GENTX_BATCH_NOT_FOUND: Core batch 1 does not exist", 404, "GENTX_BATCH_NOT_FOUND", false)]
    [InlineData(51012, "INVALID_TRANSITION: cannot move", 409, "INVALID_TRANSITION", false)]
    [InlineData(51020, "APPROVAL_NOT_ALLOWED: limit", 403, "APPROVAL_NOT_ALLOWED", false)]
    [InlineData(51021, "ALREADY_SIGNED: twice", 409, "ALREADY_SIGNED", false)]
    [InlineData(51022, "APPROVAL_CLOSED: closed", 409, "APPROVAL_CLOSED", false)]
    public void Business_rule_errors_are_not_retryable(int number, string message, int status, string code, bool retryable)
    {
        var ex = SqlErrorMapper.Map(number, message);
        Assert.Equal(status, ex.Status);
        Assert.Equal(code, ex.Code);
        Assert.Equal(retryable, ex.Retryable);
        Assert.DoesNotContain(code + ":", ex.Message);
    }

    [Fact]
    public void Command_timeout_is_504_and_retryable()
    {
        var ex = SqlErrorMapper.Map(-2, "Execution Timeout Expired.");
        Assert.Equal((504, "SQL_TIMEOUT", true), (ex.Status, ex.Code, ex.Retryable));
    }

    // Catalog F11 (Narrated): a deadlock victim is transient, so the Activity simply retries.
    [Theory]
    [InlineData(1205)]   // deadlock victim
    [InlineData(-1)]
    [InlineData(53)]
    [InlineData(233)]
    [InlineData(10054)]
    [InlineData(40613)]
    [InlineData(18456)]  // login failed while SQL Server restarts
    [InlineData(4060)]   // database still recovering
    public void Transport_and_transient_errors_are_503_and_retryable(int number)
    {
        var ex = SqlErrorMapper.Map(number, "transient");
        Assert.Equal((503, "CORE_UNAVAILABLE", true), (ex.Status, ex.Code, ex.Retryable));
    }

    [Theory]
    [InlineData(2627)]
    [InlineData(2601)]
    public void Duplicate_key_race_is_retryable(int number)
    {
        var ex = SqlErrorMapper.Map(number, "Violation of UNIQUE KEY constraint");
        Assert.Equal((503, true), (ex.Status, ex.Retryable));
    }

    [Fact]
    public void Anything_else_is_500_and_retryable()
    {
        var ex = SqlErrorMapper.Map(8134, "Divide by zero");
        Assert.Equal((500, "CORE_INTERNAL", true), (ex.Status, ex.Code, ex.Retryable));
    }

    [Fact]
    public void Split_code_uses_the_first_colon_only()
    {
        Assert.Equal(("ACCOUNT_ON_HOLD", "account ending 0033 is on hold (review: fraud)"),
            SqlErrorMapper.SplitCode("ACCOUNT_ON_HOLD: account ending 0033 is on hold (review: fraud)"));
    }
}
