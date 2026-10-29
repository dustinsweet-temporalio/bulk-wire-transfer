// DEMO-ONLY: measures Core load from the bulk wire Activities: requests in flight right now, the maximum
// seen, totals, failures, and calls per second over 1 s and 10 s windows. The Control Center charts these
// during the payroll-day / fairness demo to show the per-FI rate limit working.
using CoreApi.Config;
using CoreApi.Contracts;
using CoreApi.Endpoints;

namespace CoreApi.Demo;

public sealed class CallStats(CoreOptions options, TimeProvider clock)
{
    private readonly Lock _gate = new();
    private readonly Queue<(DateTimeOffset At, bool Failed)> _window = new();   // completions in the last 10 s
    private readonly LinkedList<CallRecord> _recent = new();                     // last 50 calls
    private int _inFlight, _maxInFlight;
    private long _total, _failed;

    public void Started()
    {
        lock (_gate)
        {
            _inFlight++;
            _maxInFlight = Math.Max(_maxInFlight, _inFlight);
        }
    }

    public void Finished(string operation, int status, long ms, string? code)
    {
        var now = clock.GetUtcNow();
        var failed = status >= 400;
        lock (_gate)
        {
            _inFlight--;
            _total++;
            if (failed) _failed++;
            _window.Enqueue((now, failed));
            Trim(now);
            _recent.AddFirst(new CallRecord(now.UtcDateTime, operation, status, ms, code));
            if (_recent.Count > 50) _recent.RemoveLast();
        }
    }

    public StatsSnapshot Snapshot()
    {
        var now = clock.GetUtcNow();
        lock (_gate)
        {
            Trim(now);
            int last1 = 0, last10 = _window.Count, fail1 = 0, fail10 = 0;
            foreach (var (at, failed) in _window)
            {
                var recent = now - at <= TimeSpan.FromSeconds(1);
                if (recent) last1++;
                if (failed) { fail10++; if (recent) fail1++; }
            }
            return new StatsSnapshot(options.FiId, _inFlight, _maxInFlight, _total, _failed,
                new CallsPerSecond(last1, last10 / 10.0), new CallsPerSecond(fail1, fail10 / 10.0), _recent.ToList());
        }
    }

    public void Reset()
    {
        lock (_gate)
        {
            _window.Clear(); _recent.Clear();
            _maxInFlight = _inFlight; _total = 0; _failed = 0;
        }
    }

    private void Trim(DateTimeOffset now)
    {
        while (_window.Count > 0 && now - _window.Peek().At > TimeSpan.FromSeconds(10)) _window.Dequeue();
    }
}

public sealed class InFlightMiddleware(RequestDelegate next, CallStats stats)
{
    public async Task InvokeAsync(HttpContext http)
    {
        var op = ActivityRoutes.Classify(http.Request);
        if (op is null)
        {
            await next(http);
            return;
        }

        stats.Started();
        var started = System.Diagnostics.Stopwatch.GetTimestamp();
        try
        {
            await next(http);
        }
        finally
        {
            var ms = (long)System.Diagnostics.Stopwatch.GetElapsedTime(started).TotalMilliseconds;
            stats.Finished(op, http.Response.StatusCode, ms, http.Items[RequestItems.ErrorCode] as string);
        }
    }
}
