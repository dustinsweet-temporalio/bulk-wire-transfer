// DEMO-ONLY: the fault and latency settings the Control Center injects into this Core.
//
// Faults model EXTERNAL conditions only (an outage, a real SQL timeout, a response lost after commit, a
// generic server bug, an undelivered callback) plus a fixed per-call processing latency. Nothing here makes
// Core behave differently under concurrency: modelling "concurrency makes SQL slower" would flatter or smear
// the design and is deliberately absent. Real infrastructure failures (kill, stop, pause) are produced by
// control-service through Docker, not by this code.
using CoreApi.Contracts;

namespace CoreApi.Demo;

public static class FaultModes
{
    public const string None = "none";
    public const string Outage = "outage";             // every Activity-facing call -> 503 for durationSeconds
    public const string SqlTimeout = "sql-timeout";    // next `count` chunk calls hit a REAL 30 s SQL timeout -> 504
    public const string LoseResponse = "lose-response"; // next `count` draft-chunk calls commit, then answer 503
    public const string Error500 = "error-500";        // next `count` Activity-facing calls -> 500
    public const string CallbackFail = "callback-fail"; // next `count` approval callbacks are not delivered

    public static readonly string[] All = [None, Outage, SqlTimeout, LoseResponse, Error500, CallbackFail];
}

public sealed class FaultState(TimeProvider clock, int defaultLatencyMs)
{
    private readonly Lock _gate = new();
    private string _mode = FaultModes.None;
    private DateTimeOffset? _until;
    private int _remaining;
    private readonly int _defaultLatencyMs = defaultLatencyMs;
    private int _latencyMs = defaultLatencyMs;

    public int DefaultLatencyMs => _defaultLatencyMs;

    public void Set(string mode, int? durationSeconds, int? count)
    {
        lock (_gate)
        {
            _mode = mode;
            _until = mode == FaultModes.Outage ? clock.GetUtcNow().AddSeconds(durationSeconds ?? 45) : null;
            _remaining = mode switch
            {
                FaultModes.SqlTimeout or FaultModes.Error500 => count ?? 3,
                FaultModes.LoseResponse => count ?? 2,
                FaultModes.CallbackFail => count ?? 1,
                _ => 0,
            };
        }
    }

    public int LatencyMs
    {
        get { lock (_gate) return _latencyMs; }
        set { lock (_gate) _latencyMs = Math.Max(0, value); }
    }

    public void Reset()
    {
        lock (_gate)
        {
            _mode = FaultModes.None; _until = null; _remaining = 0; _latencyMs = _defaultLatencyMs;
        }
    }

    public bool IsOutageActive()
    {
        lock (_gate)
        {
            if (_mode != FaultModes.Outage) return false;
            if (clock.GetUtcNow() < _until) return true;
            _mode = FaultModes.None; _until = null;   // the outage window has passed
            return false;
        }
    }

    public bool IsArmed(string mode)
    {
        lock (_gate) return _mode == mode && _remaining > 0;
    }

    // Atomically uses one occurrence of a counted fault. Concurrent calls never consume more than `count`.
    public bool TryConsume(string mode)
    {
        lock (_gate)
        {
            if (_mode != mode || _remaining <= 0) return false;
            if (--_remaining == 0) _mode = FaultModes.None;
            return true;
        }
    }

    public FaultSnapshot Snapshot()
    {
        IsOutageActive();
        lock (_gate)
        {
            return _mode switch
            {
                FaultModes.Outage => new FaultSnapshot(_mode, Math.Max(0, (_until!.Value - clock.GetUtcNow()).TotalSeconds), null),
                FaultModes.None => new FaultSnapshot(_mode, null, null),
                _ => new FaultSnapshot(_mode, null, _remaining),
            };
        }
    }
}
