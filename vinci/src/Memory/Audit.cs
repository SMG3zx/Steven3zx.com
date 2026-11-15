using System.Globalization;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Vinci.Memory;

/// One step that was taken: a retrieval, a model call, a tool call, a check.
/// Runs that share a TraceId belong to one request, and ParentRunId nests them.
/// Kind is "chain", "llm", "tool", "retrieval", "validation" or "security";
/// Status is "running", "success" or "error".
public sealed class AuditRun
{
    public required string Id { get; init; }
    public required string TraceId { get; init; }
    public string? ParentRunId { get; init; }
    public required string Name { get; init; }
    public required string Kind { get; init; }
    public string Status { get; set; } = "running";
    public required string StartedAt { get; init; }
    public string? EndedAt { get; set; }
    public double? DurationMs { get; set; }
    public JsonNode? Input { get; init; }
    public JsonNode? Output { get; set; }
    public JsonObject Metadata { get; set; } = [];
    public IReadOnlyList<string> Tags { get; init; } = [];
    public string? Error { get; set; }
}

/// A bounded, short-lived record of what the service did and why, with
/// anything that looks like a credential removed before it is kept.
public sealed partial class AuditRegistry
{
    [GeneratedRegex(
        "(authorization|api[-_]?key|access[-_]?token|refresh[-_]?token|token|password|secret|credential)",
        RegexOptions.IgnoreCase
    )]
    private static partial Regex SecretKey();

    [GeneratedRegex(@"\b(?:sk|sess|tok|key|auth)[-_a-z0-9]{12,}\b", RegexOptions.IgnoreCase)]
    private static partial Regex SecretValue();

    private readonly List<AuditRun> runs = [];
    private readonly int maxRuns;
    private readonly TimeSpan retention;
    private readonly string? filePath = Env.Get("AUDIT_FILE");
    private readonly Lock gate = new();

    public AuditRegistry(int? maxRuns = null, double? retentionHours = null)
    {
        this.maxRuns = (int)
            Math.Min(
                100_000,
                Math.Max(100, Math.Floor(maxRuns ?? Env.Number("AUDIT_MAX_RUNS", 5_000)))
            );
        retention = TimeSpan.FromHours(
            Math.Max(0, retentionHours ?? Env.Number("AUDIT_RETENTION_HOURS", 24))
        );
    }

    /// A copy with every secret-looking key blanked and every secret-looking
    /// value inside text replaced.
    public static JsonNode? Redact(JsonNode? value, int depth = 0)
    {
        if (depth > 8)
            return "[redacted-depth]";
        return value switch
        {
            null => null,
            JsonArray list => new JsonArray([.. list.Select(item => Redact(item, depth + 1))]),
            JsonObject map => new JsonObject(
                map.Select(entry =>
                    KeyValuePair.Create(
                        entry.Key,
                        SecretKey().IsMatch(entry.Key)
                            ? "[REDACTED]"
                            : Redact(entry.Value, depth + 1)
                    )
                )
            ),
            _ when value.GetValueKind() == JsonValueKind.String => SecretValue()
                .Replace(value.GetValue<string>(), "[REDACTED]"),
            _ => value.DeepClone(),
        };
    }

    /// Reads back the runs a previous process kept. A missing or damaged
    /// file is not a reason for the service to fail to start.
    public void Load()
    {
        if (filePath is null)
            return;
        try
        {
            var kept = JsonSerializer.Deserialize<List<AuditRun>>(
                File.ReadAllText(filePath),
                Json.Options
            ) ?? [];
            lock (gate)
            {
                runs.Clear();
                runs.AddRange(kept);
                Prune();
            }
        }
        catch (Exception e) when (e is IOException or JsonException or UnauthorizedAccessException)
        { }
    }

    private void Persist()
    {
        if (filePath is null)
            return;
        try
        {
            File.WriteAllText(filePath, JsonSerializer.Serialize(runs, Json.Options));
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException) { }
    }

    private void Prune()
    {
        if (retention > TimeSpan.Zero)
        {
            var cutoff = DateTimeOffset.UtcNow - retention;
            runs.RemoveAll(run => Time(run.StartedAt) < cutoff);
        }
        if (runs.Count > maxRuns)
            runs.RemoveRange(0, runs.Count - maxRuns);
    }

    private static DateTimeOffset Time(string iso) =>
        DateTimeOffset.Parse(iso, CultureInfo.InvariantCulture, DateTimeStyles.AssumeUniversal);

    private static string Now() =>
        DateTimeOffset.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ", CultureInfo.InvariantCulture);

    public AuditRun Start(
        string name,
        string kind,
        JsonNode? input = null,
        JsonObject? metadata = null,
        IReadOnlyList<string>? tags = null,
        string? traceId = null,
        string? parentRunId = null
    )
    {
        var run = new AuditRun
        {
            Id = Guid.NewGuid().ToString(),
            TraceId = traceId ?? Guid.NewGuid().ToString(),
            ParentRunId = parentRunId,
            Name = name,
            Kind = kind,
            StartedAt = Now(),
            Input = Redact(input),
            Metadata = (JsonObject?)Redact(metadata) ?? [],
            Tags = tags ?? [],
        };
        lock (gate)
        {
            runs.Add(run);
            Prune();
            Persist();
        }
        return run;
    }

    public AuditRun Finish(AuditRun run, JsonNode? output = null, JsonObject? metadata = null) =>
        End(run, "success", metadata, () => run.Output = Redact(output));

    public AuditRun Fail(AuditRun run, string error, JsonObject? metadata = null) =>
        End(run, "error", metadata, () => run.Error = SecretValue().Replace(error, "[REDACTED]"));

    private AuditRun End(AuditRun run, string status, JsonObject? metadata, Action record)
    {
        lock (gate)
        {
            run.Status = status;
            run.EndedAt = Now();
            run.DurationMs = Math.Max(
                0,
                (Time(run.EndedAt) - Time(run.StartedAt)).TotalMilliseconds
            );
            record();
            if (metadata is not null)
                foreach (var (key, value) in (JsonObject)Redact(metadata)!)
                    run.Metadata[key] = value?.DeepClone();
            Persist();
        }
        return run;
    }

    /// The most recent runs first. Name also matches against the trace id.
    public List<AuditRun> Query(
        string? traceId = null,
        string? status = null,
        string? kind = null,
        string? name = null,
        int limit = 50
    )
    {
        lock (gate)
        {
            Prune();
            var matching = runs.Where(run =>
                (string.IsNullOrEmpty(traceId) || run.TraceId == traceId)
                && (string.IsNullOrEmpty(status) || run.Status == status)
                && (string.IsNullOrEmpty(kind) || run.Kind == kind)
                && (
                    string.IsNullOrEmpty(name)
                    || run.Name.Contains(name, StringComparison.OrdinalIgnoreCase)
                    || run.TraceId.Contains(name, StringComparison.OrdinalIgnoreCase)
                )
            );
            return [.. matching.TakeLast(Math.Min(200, Math.Max(1, limit))).Reverse()];
        }
    }

    /// Every run of one request, oldest first.
    public List<AuditRun> Trace(string traceId)
    {
        lock (gate)
        {
            Prune();
            return
            [
                .. runs.Where(run => run.TraceId == traceId).OrderBy(run => Time(run.StartedAt)),
            ];
        }
    }

    public void Clear()
    {
        lock (gate)
        {
            runs.Clear();
            Persist();
        }
    }

    public JsonObject Stats()
    {
        lock (gate)
        {
            Prune();
            return new JsonObject
            {
                ["count"] = runs.Count,
                ["maxRuns"] = maxRuns,
                ["retentionHours"] = retention.TotalHours,
                ["oldestStartedAt"] = runs.FirstOrDefault()?.StartedAt,
                ["newestStartedAt"] = runs.LastOrDefault()?.StartedAt,
            };
        }
    }
}
