// SqlErrorMapper: SQL Server error number -> Core's HTTP error contract.
//
// This mapping is the boundary between "a business rule said no" (non-retryable: the workflow parks the
// chunk for a human) and "the infrastructure hiccupped" (retryable: Temporal retries with backoff). It is
// a pure function so every row of the table is unit-tested without a database.
namespace CoreApi.Data;

public static class SqlErrorMapper
{
    // Connection/transport failures and transient engine conditions. All of these clear up on their own
    // (restart, failover, network blip), so retrying is the right response.
    private static readonly HashSet<int> TransientNumbers =
    [
        -1,     // connection error
        2,      // server not found / not accessible
        53,     // network path not found (container stopped)
        64,     // connection dropped
        233,    // no process on the other end of the pipe
        1205,   // deadlock victim (catalog F11)
        4060,   // cannot open database (still recovering after a restart)
        10053,  // transport-level error: connection aborted
        10054,  // transport-level error: connection reset by peer
        10060,  // connection attempt timed out
        10061,  // connection refused
        11001,  // host not found (DNS)
        18456,  // login failed (seen while SQL Server is starting up)
        40613,  // database unavailable
    ];

    public static CoreException Map(int number, string message, Exception? inner = null)
    {
        if (number is >= 51001 and <= 51022)
        {
            var (code, text) = SplitCode(message);
            var status = number switch
            {
                51001 or 51002 or 51003 => 422, // ACCOUNT_ON_HOLD / ACCOUNT_CLOSED / ACCOUNT_NOT_FOUND
                51010 => 409,                   // GENTX_BATCH_CLOSED
                51011 => 404,                   // GENTX_BATCH_NOT_FOUND
                51012 => 409,                   // INVALID_TRANSITION
                51020 => 403,                   // APPROVAL_NOT_ALLOWED
                51021 => 409,                   // ALREADY_SIGNED
                51022 => 409,                   // APPROVAL_CLOSED
                _ => 500,
            };
            return new CoreException(status, code, text, retryable: status == 500, inner);
        }

        if (number == -2)
            // A REAL SqlClient command timeout: the statement exceeded the 30 s CommandTimeout.
            return new CoreException(504, "SQL_TIMEOUT", "Core database did not answer within the SQL command timeout", true, inner);

        if (TransientNumbers.Contains(number))
            return new CoreException(503, "CORE_UNAVAILABLE", "Core database is temporarily unavailable", true, inner);

        if (number is 2627 or 2601)
            // Duplicate key: only possible when a stale attempt and its retry race on the same chunk. The
            // retry that follows observes the rows as alreadyPresent, so this is safe to retry.
            return new CoreException(503, "CORE_UNAVAILABLE", "Concurrent duplicate attempt; retry", true, inner);

        return new CoreException(500, "CORE_INTERNAL", "Unexpected Core database error", true, inner);
    }

    // Procedure messages are '{CODE}: {human message}'; split on the first ':'.
    public static (string Code, string Message) SplitCode(string message)
    {
        var i = message.IndexOf(':');
        return i <= 0 ? ("CORE_ERROR", message.Trim()) : (message[..i].Trim(), message[(i + 1)..].Trim());
    }
}
