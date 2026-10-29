// SqlExecutor: the ONLY way core-api talks to SQL Server. Every call is a stored procedure with an explicit
// command timeout, and every SqlException leaves here already mapped to Core's error contract.
//
// WHY CommandTimeout = CORE_SQL_COMMAND_TIMEOUT_SECONDS (30): a typical OLTP command timeout. The bulk
// wire Activity's StartToClose (60 s) is set just above it, so a slow or blocked statement surfaces here as
// a real SqlException timeout (-2 -> 504, retryable) well before Temporal abandons the attempt.
using System.Data;
using CoreApi.Config;
using Microsoft.Data.SqlClient;

namespace CoreApi.Data;

public sealed class SqlExecutor(CoreOptions options)
{
    public async Task<T> RunAsync<T>(
        string procedure,
        Action<SqlParameterCollection> bind,
        Func<SqlDataReader, CancellationToken, Task<T>> read,
        CancellationToken ct)
    {
        var opened = false;
        try
        {
            await using var connection = new SqlConnection(options.ConnectionString);
            await connection.OpenAsync(ct);
            opened = true;
            await using var command = new SqlCommand(procedure, connection)
            {
                CommandType = CommandType.StoredProcedure,
                CommandTimeout = options.SqlCommandTimeoutSeconds,
            };
            bind(command.Parameters);
            await using var reader = await command.ExecuteReaderAsync(ct);
            return await read(reader, ct);
        }
        catch (SqlException ex) when (!opened)
        {
            // WHY: failing to open a connection is never a business rule, whatever number SqlClient reports
            // (a stopped container's host name stops resolving, and the managed network layer's numbers vary
            // by platform). Treat it as "database unavailable" so it is retried and labelled as such.
            throw new CoreException(503, "CORE_UNAVAILABLE", "Core database is temporarily unavailable", true, ex);
        }
        catch (SqlException ex)
        {
            throw SqlErrorMapper.Map(ex.Number, ex.Message, ex);
        }
        catch (InvalidOperationException ex) when (ex.Message.Contains("pool", StringComparison.OrdinalIgnoreCase))
        {
            // Connection pool exhausted while the database hangs: transient.
            throw new CoreException(503, "CORE_UNAVAILABLE", "Core database is temporarily unavailable", true, ex);
        }
    }

    public Task ExecuteAsync(string procedure, Action<SqlParameterCollection> bind, CancellationToken ct) =>
        RunAsync(procedure, bind, static async (r, c) => { while (await r.NextResultAsync(c)) { } return true; }, ct);

    // Health probe: a short timeout of its own so /healthz answers quickly while SQL Server is paused.
    public async Task<bool> PingAsync(CancellationToken ct)
    {
        try
        {
            var builder = new SqlConnectionStringBuilder(options.ConnectionString) { ConnectTimeout = 3 };
            await using var connection = new SqlConnection(builder.ConnectionString);
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(ct);
            timeout.CancelAfter(TimeSpan.FromSeconds(3));
            await connection.OpenAsync(timeout.Token);
            await using var command = new SqlCommand("SELECT 1", connection) { CommandTimeout = 3 };
            await command.ExecuteScalarAsync(timeout.Token);
            return true;
        }
        catch (Exception)
        {
            return false;
        }
    }
}
