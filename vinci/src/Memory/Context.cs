using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Vinci.Memory;

public sealed record ContextPack(
    string Context,
    bool CitationLocked,
    string CitationFormat,
    IReadOnlyList<JsonObject> Results,
    int OmittedResults,
    bool Truncated,
    int CharacterCount
);

/// Turns ranked evidence into the block of text an agent reads.
public static class ContextBuilder
{
    /// Puts the strongest evidence at the two ends of the context, where a
    /// model attends best, and drops duplicates.
    public static List<JsonObject> Order(IReadOnlyList<JsonObject> results)
    {
        var unique = results.DistinctBy(Key).ToList();
        var start = new List<JsonObject>();
        var end = new List<JsonObject>();
        for (var index = 0; index < unique.Count; index++)
        {
            if (index % 2 == 0)
                start.Add(unique[index]);
            else
                end.Insert(0, unique[index]);
        }
        return [.. start, .. end];
    }

    /// One line per piece of evidence, each with its [type:id] and its sources,
    /// kept inside a character budget. Evidence that does not fit is left out whole.
    public static ContextPack Build(IReadOnlyList<JsonObject> results, int maxChars)
    {
        var lines = new List<string>();
        var included = new List<JsonObject>();
        var used = 0;
        var ordered = Order(results);
        foreach (var result in ordered)
        {
            List<string> provenance =
            [
                .. Json.Strings(result["sourceEventIds"]),
                .. Json.Strings(result["sourceChunkIds"]),
            ];
            var sources =
                provenance.Count > 0 ? $" | sources: {string.Join(", ", provenance)}" : "";
            var line = $"[{Key(result)}] {Json.Str(result["content"])}{sources}";
            var separator = lines.Count > 0 ? 1 : 0;
            if (used + separator + line.Length > maxChars)
                continue;
            lines.Add(line);
            included.Add(result);
            used += separator + line.Length;
        }
        return new ContextPack(
            string.Join('\n', lines),
            CitationLocked: true,
            CitationFormat: "Use [type:id] citations and do not assert facts without retrieved evidence.",
            included,
            OmittedResults: Math.Max(0, ordered.Count - included.Count),
            Truncated: included.Count < ordered.Count,
            CharacterCount: used
        );
    }

    private static string Key(JsonObject result) =>
        $"{Json.Str(result["type"], "evidence")}:{Json.Str(result["id"], "unknown")}";
}

public sealed record CitationValidation(
    bool Valid,
    IReadOnlyList<string> Citations,
    string? Reason = null,
    IReadOnlyList<string>? Invalid = null,
    IReadOnlyList<string>? UncitedSegments = null
);

/// An answer may only say what the retrieved evidence supports, and must say
/// which evidence: every sentence carries a [type:id] that was retrieved.
public static partial class Citations
{
    [GeneratedRegex(@"\[([a-z_]+):([^\]]+)\]", RegexOptions.IgnoreCase)]
    private static partial Regex Citation();

    [GeneratedRegex(@"[^.!?]+(?:[.!?]+\s*(?:\[[a-z_]+:[^\]]+\]\s*)*)?", RegexOptions.IgnoreCase)]
    private static partial Regex Segment();

    [GeneratedRegex(@"\r?\n+")]
    private static partial Regex LineBreaks();

    public static CitationValidation Validate(string answer, IReadOnlyList<JsonObject> results)
    {
        var available = new HashSet<string>();
        foreach (var result in results)
        {
            var type = Json.Str(result["type"], "evidence");
            if (result["id"] is { } id)
                available.Add($"{type}:{Json.Str(id)}");
            if (result["evidence"] is not JsonArray evidence)
                continue;
            foreach (var item in evidence.OfType<JsonObject>())
                if (item["id"] is { } evidenceId)
                    available.Add($"{type}:{Json.Str(evidenceId)}");
        }
        var citations = Citation()
            .Matches(answer)
            .Select(m => $"{m.Groups[1].Value}:{m.Groups[2].Value}")
            .ToList();
        if (citations.Count == 0)
            return new(false, [], "Answer must cite retrieved evidence using [type:id].");
        var invalid = citations.Where(c => !available.Contains(c)).ToList();
        if (invalid.Count > 0)
            return new(
                false,
                citations,
                "Answer contains a citation that was not retrieved.",
                Invalid: invalid
            );
        var uncited = LineBreaks()
            .Split(answer)
            .SelectMany(line => Segment().Matches(line).Select(m => m.Value.Trim()))
            .Where(segment => segment.Length > 0 && !Citation().IsMatch(segment))
            .ToList();
        return uncited.Count > 0
            ? new(
                false,
                citations,
                "Every factual answer segment must cite retrieved evidence.",
                UncitedSegments: uncited
            )
            : new(true, citations);
    }
}

public sealed class ContentTooLargeException(long bytes, long maxBytes)
    : Exception($"content is too large ({bytes} bytes); maximum is {maxBytes} bytes")
{
    public long Bytes => bytes;
    public long MaxBytes => maxBytes;
}

/// Content is measured in UTF-8 bytes and turned away before it is stored.
public static class ContentLimits
{
    public const int DefaultMaxBytes = 1_000_000;

    public static int Validate(string content)
    {
        var max = Env.Number("MAX_CONTENT_BYTES", DefaultMaxBytes);
        if (max != Math.Floor(max) || max < 1_024 || max > 10_000_000)
            throw new InvalidOperationException(
                "MAX_CONTENT_BYTES must be an integer between 1024 and 10000000"
            );
        var bytes = System.Text.Encoding.UTF8.GetByteCount(content);
        return bytes > max ? throw new ContentTooLargeException(bytes, (long)max) : bytes;
    }
}
