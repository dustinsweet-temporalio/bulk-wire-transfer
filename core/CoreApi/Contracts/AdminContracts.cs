// DEMO-ONLY: JSON contracts for the admin API that control-service (the Control Center) calls.
namespace CoreApi.Contracts;

public sealed record FaultRequest(string Mode, int? DurationSeconds, int? Count);
public sealed record LatencyRequest(int ChunkLatencyMs);

public sealed record FaultSnapshot(string Mode, double? RemainingSeconds, int? RemainingCount);

public sealed record CallRecord(DateTime At, string Operation, int Status, long Ms, string? Code);

public sealed record StatsSnapshot(
    string FiId,
    int InFlight,
    int MaxInFlight,
    long CallsTotal,
    long CallsFailed,
    CallsPerSecond CallsPerSecond,
    CallsPerSecond FailuresPerSecond,
    IReadOnlyList<CallRecord> Recent);

public sealed record CallsPerSecond(double Last1s, double Last10s);

public sealed record AdminState(
    string FiId,
    FaultSnapshot Fault,
    int ChunkLatencyMs,
    IReadOnlyList<AccountView> Holds,
    StatsSnapshot Stats);

public sealed record ErrorResponse(string Code, string Message, bool Retryable);
