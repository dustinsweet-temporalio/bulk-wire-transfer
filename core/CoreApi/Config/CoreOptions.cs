// CoreOptions: configuration for ONE FI's Core instance, read from environment variables at startup.
//
// Two containers run this same image: core-lakeshore (FI_ID=lakeshore) and core-prairie (FI_ID=prairie), each
// with a connection string to its own SQL Server only. Nothing here is shared between FIs.
namespace CoreApi.Config;

public sealed class CoreOptions
{
    public required string FiId { get; init; }
    public required string ConnectionString { get; init; }

    // WHY 30 s: a typical command timeout for set-based OLTP writes. The Activity StartToClose (60 s) is set
    // just above it, so a slow statement fails here first with a real timeout the workflow can retry.
    public int SqlCommandTimeoutSeconds { get; init; } = 30;

    // DEMO-ONLY: the one-time code every approver "receives". PRODUCTION NOTE: real MFA delivery/verification.
    public required string DemoTac { get; init; }
    public long DualControlThresholdCents { get; init; }

    // DEMO-ONLY: simulated core processing time per chunk call (adjustable from the Control Center).
    public int SimulatedLatencyMs { get; init; }

    public required string InternalToken { get; init; }
    public required string AuthSigningKey { get; init; }
    public required string BulkwireInternalUrl { get; init; }

    public static CoreOptions FromConfiguration(IConfiguration c)
    {
        string Required(string key) =>
            c[key] is { Length: > 0 } v ? v : throw new InvalidOperationException($"{key} is required");

        return new CoreOptions
        {
            FiId = Required("FI_ID"),
            ConnectionString = Required("CORE_CONNECTION_STRING"),
            SqlCommandTimeoutSeconds = int.Parse(c["CORE_SQL_COMMAND_TIMEOUT_SECONDS"] ?? "30"),
            DemoTac = Required("CORE_DEMO_TAC"),
            DualControlThresholdCents = long.Parse(Required("CORE_DUAL_CONTROL_THRESHOLD_CENTS")),
            SimulatedLatencyMs = int.Parse(c["CORE_SIMULATED_LATENCY_MS"] ?? "0"),
            InternalToken = Required("INTERNAL_TOKEN"),
            AuthSigningKey = Required("AUTH_SIGNING_KEY"),
            BulkwireInternalUrl = Required("BULKWIRE_INTERNAL_URL"),
        };
    }
}
