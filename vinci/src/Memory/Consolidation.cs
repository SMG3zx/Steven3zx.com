using System.Net.Http.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Vinci.Memory;

public sealed record ExtractedMemory(string Content, string Category, double Confidence, IReadOnlyList<string> EntityNames, string? ValidFrom, string? ValidUntil);
public sealed record ExtractedEntity(string Name, string Type, IReadOnlyList<string> Aliases, string? Description);
public sealed record ExtractedClaim(string Statement, string Predicate, string SubjectName, string? ObjectName, double Confidence);
public sealed record ExtractedProcedure(string Title, string Goal, IReadOnlyList<string> Steps, IReadOnlyList<string> Preconditions, IReadOnlyList<string> KnownFailures, double Confidence);

/// What a model found worth keeping in one event.
public sealed record Extraction(
    IReadOnlyList<ExtractedMemory> Memories,
    IReadOnlyList<ExtractedEntity> Entities,
    IReadOnlyList<ExtractedClaim> Claims,
    IReadOnlyList<ExtractedProcedure> Procedures)
{
    public static readonly Extraction Empty = new([], [], [], []);
}

/// Turns raw events into durable knowledge, and a session into a reflection.
public interface IConsolidator
{
    Task<Extraction> Extract(MemoryEvent e);
    Task<SessionReflection> Reflect(IReadOnlyList<MemoryEvent> events);
}

/// A model's answer is checked field by field before any of it is stored:
/// a missing list is empty, a missing confidence is 0.7, and anything of the
/// wrong shape is an error rather than a guess.
public static partial class Consolidation
{
    [GeneratedRegex(@"^```json\s*|\s*```$")]
    private static partial Regex Fence();

    public static Extraction ParseExtraction(JsonNode? value)
    {
        var root = Object(value, "extraction");
        return new Extraction(
            [.. List(root, "memories").Select(m => new ExtractedMemory(
                Text(m, "content"), OneOf(m, "category", Vocabulary.MemoryCategories, "fact"), Confidence(m),
                Texts(m, "entityNames"), OptionalText(m, "validFrom"), OptionalText(m, "validUntil")))],
            [.. List(root, "entities").Select(e => new ExtractedEntity(
                Text(e, "name"), OneOf(e, "type", Vocabulary.EntityTypes, "concept"), Texts(e, "aliases"), OptionalText(e, "description")))],
            [.. List(root, "claims").Select(c => new ExtractedClaim(
                Text(c, "statement"), Text(c, "predicate"), Text(c, "subjectName"), OptionalText(c, "objectName"), Confidence(c)))],
            [.. List(root, "procedures").Select(p => new ExtractedProcedure(
                Text(p, "title"), Text(p, "goal"),
                Texts(p, "steps", notEmpty: true) is { Count: > 0 } steps ? steps : throw Invalid("steps", "at least one step"),
                Texts(p, "preconditions"), Texts(p, "knownFailures"), Confidence(p)))]);
    }

    public static SessionReflection ParseReflection(JsonNode? value)
    {
        var root = Object(value, "reflection");
        return new SessionReflection(Text(root, "reflection"), Texts(root, "lessons", notEmpty: true), Texts(root, "failures", notEmpty: true));
    }

    private static JsonObject Schema(JsonObject properties, params string[] required) => new()
    {
        ["type"] = "object",
        ["properties"] = properties,
        ["required"] = new JsonArray([.. required.Select(r => (JsonNode)r)]),
    };

    private static JsonObject Field(string type, JsonObject? items = null, string[]? allowed = null)
    {
        var field = new JsonObject { ["type"] = type };
        if (items is not null) field["items"] = items.DeepClone();
        if (allowed is not null) field["enum"] = new JsonArray([.. allowed.Select(a => (JsonNode)a)]);
        return field;
    }

    private static JsonObject Array(JsonObject items) => Field("array", items);

    /// The shape Extraction is parsed from, written out so the server can hold the model to it.
    public static JsonObject ExtractionSchema() => Schema(new JsonObject
    {
        ["memories"] = Array(Schema(new JsonObject
        {
            ["content"] = Field("string"), ["category"] = Field("string", allowed: Vocabulary.MemoryCategories),
            ["confidence"] = Field("number"), ["entityNames"] = Array(Field("string")),
        }, "content", "category", "confidence", "entityNames")),
        ["entities"] = Array(Schema(new JsonObject
        {
            ["name"] = Field("string"), ["type"] = Field("string", allowed: Vocabulary.EntityTypes), ["aliases"] = Array(Field("string")),
        }, "name", "type", "aliases")),
        ["claims"] = Array(Schema(new JsonObject
        {
            ["statement"] = Field("string"), ["predicate"] = Field("string"), ["subjectName"] = Field("string"),
            ["objectName"] = Field("string"), ["confidence"] = Field("number"),
        }, "statement", "predicate", "subjectName", "confidence")),
        ["procedures"] = Array(Schema(new JsonObject
        {
            ["title"] = Field("string"), ["goal"] = Field("string"), ["steps"] = Array(Field("string")), ["confidence"] = Field("number"),
        }, "title", "goal", "steps", "confidence")),
    }, "memories", "entities", "claims", "procedures");

    public static JsonObject ReflectionSchema() => Schema(new JsonObject
    {
        ["reflection"] = Field("string"), ["lessons"] = Array(Field("string")), ["failures"] = Array(Field("string")),
    }, "reflection", "lessons", "failures");

    /// The JSON inside a model's reply, with any ```json fence removed.
    public static JsonNode? ParseReply(string content) => JsonNode.Parse(Fence().Replace(content, ""));

    public static IConsolidator Create() =>
        Env.Get("CONSOLIDATION_MODE") == "heuristic" ? new HeuristicConsolidator() : new OpenAICompatibleConsolidator();

    private static FormatException Invalid(string field, string expected) => new($"{field}: expected {expected}");

    private static JsonObject Object(JsonNode? value, string field) => value as JsonObject ?? throw Invalid(field, "an object");

    private static IEnumerable<JsonObject> List(JsonObject parent, string field) => parent[field] switch
    {
        null => [],
        JsonArray array => array.Select(item => Object(item, field)),
        _ => throw Invalid(field, "an array"),
    };

    private static string Text(JsonObject parent, string field) =>
        parent[field] is JsonValue value && value.TryGetValue<string>(out var text) && text.Length > 0 ? text : throw Invalid(field, "non-empty text");

    private static string? OptionalText(JsonObject parent, string field) => parent[field] switch
    {
        null => null,
        JsonValue value when value.TryGetValue<string>(out var text) => text,
        _ => throw Invalid(field, "text"),
    };

    private static List<string> Texts(JsonObject parent, string field, bool notEmpty = false) => parent[field] switch
    {
        null => [],
        JsonArray array => [.. array.Select(item =>
            item is JsonValue value && value.TryGetValue<string>(out var text) && (!notEmpty || text.Length > 0) ? text : throw Invalid(field, "an array of text"))],
        _ => throw Invalid(field, "an array"),
    };

    private static string OneOf(JsonObject parent, string field, string[] allowed, string fallback) =>
        parent[field] is null ? fallback
            : OptionalText(parent, field) is { } text && allowed.Contains(text) ? text
            : throw Invalid(field, $"one of {string.Join(", ", allowed)}");

    private static double Confidence(JsonObject parent)
    {
        if (parent["confidence"] is not { } node) return 0.7;
        var value = node.GetValueKind() == System.Text.Json.JsonValueKind.Number ? Json.Num(node) : double.NaN;
        return value is >= 0 and <= 1 ? value : throw Invalid("confidence", "a number from 0 to 1");
    }
}

/// Asks any server that speaks the OpenAI chat-completions protocol.
public sealed class OpenAICompatibleConsolidator(HttpMessageHandler? handler = null) : IConsolidator
{
    private readonly string? baseUrl = Env.BaseUrl("MODEL_BASE_URL");
    private readonly string model = Env.Get("MODEL_NAME") ?? "local-model";
    private readonly string? apiKey = Env.Get("MODEL_API_KEY");
    private readonly HttpClient http = handler is null ? new HttpClient() : new HttpClient(handler);

    public async Task<Extraction> Extract(MemoryEvent e) => Consolidation.ParseExtraction(await Ask(
        "consolidation", "extraction", Consolidation.ExtractionSchema(),
        "Extract only durable, source-grounded knowledge from the event. Do not invent facts. Use empty arrays when the event contains no durable information.",
        new JsonObject { ["kind"] = e.Kind, ["content"] = e.Content, ["source"] = e.Source, ["occurredAt"] = e.OccurredAt, ["metadata"] = e.Metadata?.DeepClone() }));

    public async Task<SessionReflection> Reflect(IReadOnlyList<MemoryEvent> events) => Consolidation.ParseReflection(await Ask(
        "reflection", "reflection", Consolidation.ReflectionSchema(),
        "Reflect only on the supplied session events. Do not invent facts. Failures must describe actionable lessons from explicit errors or unsuccessful tool results; use empty arrays when absent.",
        new JsonArray([.. events.Select(e => (JsonNode)new JsonObject
        {
            ["id"] = e.Id, ["kind"] = e.Kind, ["content"] = e.Content, ["occurredAt"] = e.OccurredAt, ["metadata"] = e.Metadata?.DeepClone(),
        })])));

    /// The reply's shape is enforced by the server (json_schema), and checked again by the parsers.
    private async Task<JsonNode?> Ask(string purpose, string wanted, JsonObject schema, string system, JsonNode user)
    {
        if (baseUrl is null) throw new InvalidOperationException($"MODEL_BASE_URL is required for {purpose}");
        using var request = new HttpRequestMessage(HttpMethod.Post, $"{baseUrl}/chat/completions")
        {
            Content = JsonContent.Create(new JsonObject
            {
                ["model"] = model,
                ["temperature"] = 0,
                ["response_format"] = new JsonObject
                {
                    ["type"] = "json_schema",
                    ["json_schema"] = new JsonObject { ["name"] = wanted, ["schema"] = schema.DeepClone(), ["strict"] = true },
                },
                ["messages"] = new JsonArray(
                    new JsonObject { ["role"] = "system", ["content"] = system },
                    new JsonObject { ["role"] = "user", ["content"] = user.ToJsonString() }),
            }),
        };
        if (apiKey is not null) request.Headers.Authorization = new("Bearer", apiKey);
        using var reply = await http.SendAsync(request);
        if (!reply.IsSuccessStatusCode) throw new InvalidOperationException($"model service returned {(int)reply.StatusCode}");
        var payload = await reply.Content.ReadFromJsonAsync<JsonNode>();
        var content = Json.Str(payload?["choices"]?[0]?["message"]?["content"]);
        if (content.Length == 0) throw new InvalidOperationException($"model service returned no {wanted} content");
        return Consolidation.ParseReply(content);
    }
}

/// An offline consolidator for local use and lifecycle tests. It never
/// infers durable memory from ordinary chat: the caller must mark the event
/// metadata with { "remember": true }.
public sealed class HeuristicConsolidator : IConsolidator
{
    public Task<Extraction> Extract(MemoryEvent e) => Task.FromResult(Marked(e)
        ? Extraction.Empty with { Memories = [new ExtractedMemory(e.Content, "fact", 0.5, [], e.OccurredAt, null)] }
        : Extraction.Empty);

    public Task<SessionReflection> Reflect(IReadOnlyList<MemoryEvent> events)
    {
        var remembered = events.Count(Marked);
        return Task.FromResult(new SessionReflection(
            remembered > 0 ? $"Explicitly remembered {remembered} event(s)." : "No events were explicitly marked for durable reflection.", [], []));
    }

    private static bool Marked(MemoryEvent e) => Json.Flag(e.Metadata?["remember"]);
}

/// Every node and relationship in the graph, as written by a backup.
public sealed record MemorySnapshot(string Format, int Version, string ExportedAt, IReadOnlyList<SnapshotNode> Nodes, IReadOnlyList<SnapshotRelationship> Relationships)
{
    public const string FormatName = "personal-memory-snapshot";

    /// Refuses a snapshot that could not be restored whole: a wrong format, a
    /// duplicate node, a name that is not a plain identifier, or a
    /// relationship to a node that is not in the file.
    public (int Nodes, int Relationships) Validate()
    {
        if (Format != FormatName || Version != 1) throw new FormatException("Unsupported snapshot format");
        var keys = new HashSet<string>();
        foreach (var node in Nodes)
        {
            if (string.IsNullOrEmpty(node.Key) || !keys.Add(node.Key)) throw new FormatException($"snapshot contains duplicate or empty node key: {node.Key}");
            if (node.Labels is not { Count: > 0 } || node.Labels.Any(label => !Snapshots.Name().IsMatch(label))) throw new FormatException($"snapshot contains an invalid label for node {node.Key}");
            if (node.Properties is null) throw new FormatException($"snapshot contains invalid properties for node {node.Key}");
        }
        foreach (var relationship in Relationships)
        {
            if (relationship.Type is null || !Snapshots.Name().IsMatch(relationship.Type)) throw new FormatException($"snapshot contains an invalid relationship type: {relationship.Type}");
            if (!keys.Contains(relationship.SourceKey) || !keys.Contains(relationship.TargetKey)) throw new FormatException($"snapshot relationship {relationship.Type} references a missing node");
            if (relationship.Properties is null) throw new FormatException("snapshot contains invalid relationship properties");
        }
        return (Nodes.Count, Relationships.Count);
    }
}

public sealed record SnapshotNode(string Key, IReadOnlyList<string> Labels, JsonObject Properties);
public sealed record SnapshotRelationship(string SourceKey, string Type, string TargetKey, JsonObject Properties);

internal static partial class Snapshots
{
    [GeneratedRegex("^[A-Za-z_][A-Za-z0-9_]*$")]
    internal static partial Regex Name();
}
