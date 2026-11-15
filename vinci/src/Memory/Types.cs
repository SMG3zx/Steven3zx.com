using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.Json.Serialization;

namespace Vinci.Memory;

/// Who and what a piece of memory belongs to, and how wide a recall may look.
public record MemoryScope
{
    public string? UserId { get; init; }
    public string? ProjectId { get; init; }
    public string? SessionId { get; init; }
    public bool? IncludeHistorical { get; init; }
    public bool? IncludeTestData { get; init; }
    /// Any of "lexical", "vector", "graph". Null means all three.
    public IReadOnlyList<string>? RetrievalMethods { get; init; }
}

/// Something that happened: a message, a tool call or result, a decision, an
/// artifact or an error. Events are the source of truth; memories point back at them.
public record MemoryEvent : MemoryScope
{
    public string? Id { get; init; }
    public required string Kind { get; init; }
    public required string Content { get; init; }
    public string? Source { get; init; }
    public JsonObject? Metadata { get; init; }
    public string? OccurredAt { get; init; }
    public string? IdempotencyKey { get; init; }
}

public record ConversationMessage
{
    public string? Id { get; init; }
    public string? IdempotencyKey { get; init; }
    public required string Kind { get; init; }
    public required string Content { get; init; }
    public string? Source { get; init; }
    public JsonObject? Metadata { get; init; }
    public string? OccurredAt { get; init; }
}

public record ConversationInput : MemoryScope
{
    public required IReadOnlyList<ConversationMessage> Messages { get; init; }
}

public record SessionSummaryInput : MemoryScope
{
    public required string Content { get; init; }
    public IReadOnlyList<string>? SourceEventIds { get; init; }
    public double? Confidence { get; init; }
    public string? IdempotencyKey { get; init; }
}

public record SessionReflectionInput : MemoryScope
{
    public required string Content { get; init; }
    public IReadOnlyList<string>? Lessons { get; init; }
    public IReadOnlyList<string>? Failures { get; init; }
    public IReadOnlyList<string>? SourceEventIds { get; init; }
    public string? IdempotencyKey { get; init; }
}

public sealed record SessionReflection(string Reflection, IReadOnlyList<string> Lessons, IReadOnlyList<string> Failures);

/// Work the worker does later: consolidate an event, reflect on a session,
/// embed a document, extract a graph, or reconsolidate a memory.
public sealed record MemoryJob
{
    public required string Id { get; init; }
    public required string Kind { get; init; }
    public required string TargetId { get; init; }
    public string? Status { get; init; }
    public string? AvailableAt { get; init; }
    public long? Attempts { get; init; }
    public string? Error { get; init; }
    public string? LeaseUntil { get; init; }
    public string? ApprovalStatus { get; init; }
    public string? Proposal { get; init; }
}

public record CuratedMemory : MemoryScope
{
    public string? Id { get; init; }
    public required string Content { get; init; }
    public string? Category { get; init; }
    public string? Status { get; init; }
    public double? Confidence { get; init; }
    public JsonObject? Metadata { get; init; }
    public IReadOnlyList<string>? SourceEventIds { get; init; }
    public IReadOnlyList<string>? EntityIds { get; init; }
    public string? SupersedesMemoryId { get; init; }
    public string? Subject { get; init; }
    public string? Predicate { get; init; }
    public string? Object { get; init; }
    public string? ValidFrom { get; init; }
    public string? ValidUntil { get; init; }
}

public record Entity : MemoryScope
{
    public string? Id { get; init; }
    public required string Name { get; init; }
    public required string Type { get; init; }
    public IReadOnlyList<string>? Aliases { get; init; }
    public string? Description { get; init; }
}

public record DocumentInput : MemoryScope
{
    public string? Id { get; init; }
    public required string Title { get; init; }
    public required string Source { get; init; }
    public string? Content { get; init; }
    public JsonObject? Metadata { get; init; }
}

public record ChunkInput : MemoryScope
{
    public string? Id { get; init; }
    public required string DocumentId { get; init; }
    public required string Content { get; init; }
    public required int Ordinal { get; init; }
    public IReadOnlyList<double>? Embedding { get; init; }
    public IReadOnlyList<string>? EntityIds { get; init; }
}

public record ClaimInput : MemoryScope
{
    public string? Id { get; init; }
    public required string Statement { get; init; }
    public required string Predicate { get; init; }
    public required string SubjectEntityId { get; init; }
    public string? ObjectEntityId { get; init; }
    public double? Confidence { get; init; }
    public IReadOnlyList<string>? SourceChunkIds { get; init; }
    public IReadOnlyList<string>? SourceEventIds { get; init; }
}

public record ProcedureInput : MemoryScope
{
    public string? Id { get; init; }
    public required string Title { get; init; }
    public required string Goal { get; init; }
    public required IReadOnlyList<string> Steps { get; init; }
    public IReadOnlyList<string>? Preconditions { get; init; }
    public IReadOnlyList<string>? KnownFailures { get; init; }
    public double? Confidence { get; init; }
    public IReadOnlyList<string>? SourceEventIds { get; init; }
    public bool? Executable { get; init; }
    public string? Command { get; init; }
    public JsonObject? Parameters { get; init; }
    /// "read-only", "approval-required" or "trusted".
    public string? Safety { get; init; }
}

/// The names the memory service accepts, so a typo is rejected at the door.
public static class Vocabulary
{
    public static readonly string[] EventKinds = ["user_message", "agent_message", "tool_call", "tool_result", "decision", "artifact", "error"];
    public static readonly string[] JobKinds = ["consolidate_event", "reflect_session", "embed_document", "extract_graph", "reconsolidate_memory"];
    public static readonly string[] MemoryCategories = ["working", "profile", "fact", "preference", "decision", "episode", "belief", "reflection", "failure", "semantic", "procedure", "summary", "claim"];
    public static readonly string[] MemoryStatuses = ["active", "superseded", "archived", "retracted"];
    public static readonly string[] RetrievalMethods = ["lexical", "vector", "graph"];
    public static readonly string[] EntityTypes = ["person", "project", "organization", "technology", "file", "tool", "concept", "other"];
}

/// Reading loosely typed JSON the way the stored rows need it: a missing or
/// odd value becomes a default instead of an exception.
public static class Json
{
    public static readonly JsonSerializerOptions Options = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        // Text goes out as written: an apostrophe stays an apostrophe.
        Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
    };

    /// The number in a node. Null is the fallback; text that is not a number is NaN.
    public static double Num(JsonNode? node, double fallback = 0)
    {
        if (node is null) return fallback;
        // A value's kind is asked for, not its type: a number may be held as
        // an int, a long or a double depending on who made the node.
        switch (node.GetValueKind())
        {
            case JsonValueKind.Number: return Parse(node.ToJsonString());
            case JsonValueKind.True: return 1;
            case JsonValueKind.False: return 0;
            case JsonValueKind.String:
                var text = node.GetValue<string>().Trim();
                return text.Length == 0 ? 0 : Parse(text);
            default: return double.NaN;
        }

        static double Parse(string text) =>
            double.TryParse(text, System.Globalization.NumberStyles.Float, System.Globalization.CultureInfo.InvariantCulture, out var parsed) ? parsed : double.NaN;
    }

    /// True only for a JSON true.
    public static bool Flag(JsonNode? node) => node?.GetValueKind() == JsonValueKind.True;

    /// The text of a node. Null is the fallback; a number or flag is written out.
    public static string Str(JsonNode? node, string fallback = "")
    {
        if (node is null) return fallback;
        return node.GetValueKind() == JsonValueKind.String ? node.GetValue<string>() : node.ToJsonString();
    }

    /// The strings in an array node, or nothing when it is not an array.
    public static List<string> Strings(JsonNode? node) =>
        node is JsonArray array ? [.. array.Select(item => Str(item, "null"))] : [];

    public static JsonObject Clone(JsonObject source) => (JsonObject)source.DeepClone();
}

/// Settings come from the environment, as they did before the port.
public static class Env
{
    public static string? Get(string name) =>
        Environment.GetEnvironmentVariable(name) is { Length: > 0 } value ? value : null;

    public static double Number(string name, double fallback) =>
        Get(name) is { } raw && double.TryParse(raw, System.Globalization.NumberStyles.Float, System.Globalization.CultureInfo.InvariantCulture, out var value)
            ? value
            : Get(name) is null ? fallback : double.NaN;

    /// A base URL with no trailing slash, or null when the setting is empty.
    public static string? BaseUrl(string name) => Get(name)?.TrimEnd('/') is { Length: > 0 } url ? url : null;
}
