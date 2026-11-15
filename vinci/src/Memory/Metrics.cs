using System.Globalization;
using System.Text.Json.Nodes;

namespace Vinci.Memory;

/// Counts and timings of retrievals, kept in memory and bounded. It never
/// holds the content of a query or a memory.
public sealed class MetricsRegistry(TimeProvider? clock = null)
{
    private const int MaxLatencySamples = 1_000;
    private const int MaxHistoryBuckets = 60;

    private sealed class Bucket
    {
        public int Retrievals,
            Errors,
            LatencySamples,
            ValidationRejected;
        public double LatencySumMs;
    }

    private readonly TimeProvider clock = clock ?? TimeProvider.System;
    private readonly DateTimeOffset startedAt = (clock ?? TimeProvider.System).GetUtcNow();
    private readonly Dictionary<string, int> routes = [];
    private readonly Dictionary<string, int> methods = [];
    private readonly List<double> latencies = [];
    private readonly SortedDictionary<long, Bucket> history = [];
    private int retrievalCount,
        retrievalErrors,
        validationCount,
        validationRejected;
    private double estimatedCostUsd;

    private Bucket Current()
    {
        var minute = clock.GetUtcNow().ToUnixTimeMilliseconds() / 60_000 * 60_000;
        if (!history.TryGetValue(minute, out var bucket))
            history[minute] = bucket = new Bucket();
        while (history.Count > MaxHistoryBuckets)
            history.Remove(history.Keys.First());
        return bucket;
    }

    public void RecordRetrieval(
        string route,
        IEnumerable<string> usedMethods,
        double durationMs,
        bool ok,
        double estimatedCost = 0
    )
    {
        lock (history)
        {
            var duration = double.IsNaN(durationMs) ? 0 : Math.Max(0, durationMs);
            retrievalCount++;
            estimatedCostUsd += double.IsNaN(estimatedCost) ? 0 : Math.Max(0, estimatedCost);
            if (!ok)
                retrievalErrors++;
            routes[route] = routes.GetValueOrDefault(route) + 1;
            foreach (var method in usedMethods)
                methods[method] = methods.GetValueOrDefault(method) + 1;
            latencies.Add(duration);
            if (latencies.Count > MaxLatencySamples)
                latencies.RemoveAt(0);
            var bucket = Current();
            bucket.Retrievals++;
            if (!ok)
                bucket.Errors++;
            bucket.LatencySumMs += duration;
            bucket.LatencySamples++;
        }
    }

    public void RecordAnswerValidation(bool valid)
    {
        lock (history)
        {
            validationCount++;
            if (valid)
                return;
            validationRejected++;
            Current().ValidationRejected++;
        }
    }

    public JsonObject Snapshot()
    {
        lock (history)
        {
            var sorted = latencies.Order().ToList();
            var errorRate = retrievalCount > 0 ? (double)retrievalErrors / retrievalCount : 0;
            var p95 =
                sorted.Count > 0
                    ? sorted[Math.Min(sorted.Count - 1, (int)Math.Floor(sorted.Count * 0.95))]
                    : 0;
            var errorRateAlert = Env.Number("METRICS_ERROR_RATE_ALERT", 0.1);
            var p95AlertMs = Env.Number("METRICS_P95_ALERT_MS", 1_000);
            var alerts = new JsonArray();
            if (retrievalCount >= 5 && errorRate >= errorRateAlert)
                alerts.Add(
                    new JsonObject
                    {
                        ["kind"] = "retrieval-error-rate",
                        ["message"] =
                            $"Retrieval error rate is {Fixed(errorRate * 100)}% (threshold {Fixed(errorRateAlert * 100)}%).",
                    }
                );
            if (retrievalCount >= 5 && p95 >= p95AlertMs)
                alerts.Add(
                    new JsonObject
                    {
                        ["kind"] = "retrieval-p95-latency",
                        ["message"] =
                            $"Retrieval p95 latency is {Fixed(p95)} ms (threshold {p95AlertMs.ToString(CultureInfo.InvariantCulture)} ms).",
                    }
                );
            return new JsonObject
            {
                ["uptimeSeconds"] = Math.Round((clock.GetUtcNow() - startedAt).TotalSeconds),
                ["retrievals"] = new JsonObject
                {
                    ["count"] = retrievalCount,
                    ["errors"] = retrievalErrors,
                    ["errorRate"] = errorRate,
                    ["meanLatencyMs"] = sorted.Count > 0 ? sorted.Average() : 0,
                    ["p95LatencyMs"] = p95,
                    ["routes"] = Counts(routes),
                    ["methods"] = Counts(methods),
                    ["retainedLatencySamples"] = sorted.Count,
                    ["estimatedCostUsd"] = estimatedCostUsd,
                },
                ["answerValidation"] = new JsonObject
                {
                    ["count"] = validationCount,
                    ["rejected"] = validationRejected,
                    ["rejectionRate"] =
                        validationCount > 0 ? (double)validationRejected / validationCount : 0,
                },
                ["alerts"] = alerts,
                ["trend"] = new JsonArray([
                    .. history.Select(entry =>
                        (JsonNode)
                            new JsonObject
                            {
                                ["minute"] = DateTimeOffset
                                    .FromUnixTimeMilliseconds(entry.Key)
                                    .ToString(
                                        "yyyy-MM-ddTHH:mm:ss.fffZ",
                                        CultureInfo.InvariantCulture
                                    ),
                                ["retrievals"] = entry.Value.Retrievals,
                                ["errors"] = entry.Value.Errors,
                                ["meanLatencyMs"] =
                                    entry.Value.LatencySamples > 0
                                        ? entry.Value.LatencySumMs / entry.Value.LatencySamples
                                        : 0,
                                ["validationRejected"] = entry.Value.ValidationRejected,
                            }
                    ),
                ]),
            };
        }

        static string Fixed(double value) => value.ToString("0.0", CultureInfo.InvariantCulture);
        static JsonObject Counts(Dictionary<string, int> counts) =>
            new(counts.Select(c => KeyValuePair.Create(c.Key, (JsonNode?)c.Value)));
    }
}
