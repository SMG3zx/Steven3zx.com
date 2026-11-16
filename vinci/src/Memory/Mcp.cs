using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Vinci.Memory;

/// The memory tools for an agent, spoken over standard input and output
/// (the Model Context Protocol, newline-delimited JSON-RPC). Each tool is a
/// thin call to the memory API, so the API stays the one place with rules.
public sealed class McpServer(HttpClient http, string apiUrl, string? apiKey)
{
    public const string ProtocolVersion = "2025-06-18";

    private sealed record Tool(
        string Name,
        string Description,
        JsonObject Schema,
        Func<JsonObject, (string Path, JsonNode? Body)> Route
    );

    private static JsonObject Prop(
        string type,
        string? description = null,
        JsonNode? items = null,
        string[]? options = null,
        double? min = null,
        double? max = null
    )
    {
        var prop = new JsonObject { ["type"] = type };
        if (options is not null)
            prop["enum"] = new JsonArray([.. options.Select(o => (JsonNode)o)]);
        if (items is not null)
            prop["items"] = items;
        if (min is not null)
            prop["minimum"] = min;
        if (max is not null)
            prop["maximum"] = max;
        if (description is not null)
            prop["description"] = description;
        return prop;
    }

    private static JsonObject Schema(
        string[] required,
        params (string Name, JsonObject Prop)[] props
    ) =>
        new()
        {
            ["type"] = "object",
            ["properties"] = new JsonObject(
                props.Select(p => KeyValuePair.Create(p.Name, (JsonNode?)p.Prop.DeepClone()))
            ),
            ["required"] = new JsonArray([.. required.Select(r => (JsonNode)r)]),
        };

    private static readonly (string, JsonObject) User = ("userId", Prop("string")),
        Project = ("projectId", Prop("string")),
        Session = ("sessionId", Prop("string"));
    private static readonly (string, JsonObject) Metadata = (
        "metadata",
        new JsonObject { ["type"] = "object" }
    );

    private static JsonObject Strings() =>
        Prop("array", items: new JsonObject { ["type"] = "string" });

    private static JsonObject Kind() => Prop("string", options: Vocabulary.EventKinds);

    private static JsonObject Safety() =>
        Prop("string", options: ["read-only", "approval-required", "trusted"]);

    private static (string Path, JsonNode? Body) Post(string path, JsonObject input) =>
        (path, input);

    private static string Segment(JsonObject input) => Uri.EscapeDataString(Json.Str(input["id"]));

    /// The body without the id that went into the path.
    private static JsonObject Without(JsonObject input, params string[] keys)
    {
        var copy = Json.Clone(input);
        foreach (var key in keys)
            copy.Remove(key);
        return copy;
    }

    private static readonly Tool[] Tools =
    [
        new(
            "remember_event",
            "Persist an immutable user, agent, tool, decision, artifact, or error event. The result includes a traceId receipt for the audit console.",
            Schema(
                ["kind", "content"],
                ("kind", Kind()),
                ("content", Prop("string")),
                ("source", Prop("string")),
                User,
                Project,
                Session,
                Metadata
            ),
            input => Post("/v1/events", input)
        ),
        new(
            "remember_events",
            "Persist an ordered batch of immutable user, agent, tool, decision, artifact, or error events. The result includes a traceId receipt for the audit console.",
            Schema(
                ["events"],
                ("events", Prop("array", items: new JsonObject { ["type"] = "object" }))
            ),
            input => Post("/v1/events/batch", input)
        ),
        new(
            "remember_conversation",
            "Persist an ordered agent conversation with durable session and project provenance. Use this for real user, agent, and tool messages; it is separate from test or benchmark data. The result includes a traceId receipt.",
            Schema(
                ["sessionId", "messages"],
                ("sessionId", Prop("string")),
                User,
                Project,
                ("messages", Prop("array", items: new JsonObject { ["type"] = "object" }))
            ),
            input => Post("/v1/conversations", input)
        ),
        new(
            "reflect_session",
            "Persist a session reflection, lessons learned, and Reflexion-style failure memories without deleting the original events.",
            Schema(
                ["sessionId", "content"],
                ("sessionId", Prop("string")),
                ("content", Prop("string")),
                ("lessons", Strings()),
                ("failures", Strings()),
                ("sourceEventIds", Strings()),
                ("executable", Prop("boolean")),
                ("command", Prop("string")),
                ("parameters", new JsonObject { ["type"] = "object" }),
                ("safety", Safety()),
                User,
                Project
            ),
            input =>
                Post(
                    $"/v1/sessions/{Uri.EscapeDataString(Json.Str(input["sessionId"]))}/reflection",
                    input
                )
        ),
        new(
            "recall_memory",
            "Search durable events and curated memories for relevant context.",
            Schema(
                ["query"],
                ("query", Prop("string")),
                User,
                Project,
                Session,
                ("includeHistorical", Prop("boolean")),
                ("includeTestData", Prop("boolean")),
                ("limit", Prop("integer", min: 1, max: 50))
            ),
            input => Post("/v1/recall", input)
        ),
        new(
            "build_context",
            "Retrieve a bounded, provenance-preserving context pack ready for agent prompt injection.",
            Schema(
                ["query"],
                ("query", Prop("string")),
                User,
                Project,
                Session,
                ("includeHistorical", Prop("boolean")),
                ("limit", Prop("integer", min: 1, max: 50)),
                ("maxChars", Prop("integer", min: 256, max: 100000))
            ),
            input => Post("/v1/context", input)
        ),
        new(
            "create_memory",
            "Create a curated durable memory with optional provenance links to source events.",
            Schema(
                ["content"],
                ("content", Prop("string")),
                ("category", Prop("string", options: Vocabulary.MemoryCategories)),
                ("status", Prop("string", options: ["active", "superseded", "retracted"])),
                ("confidence", Prop("number", min: 0, max: 1)),
                Metadata,
                User,
                Project,
                Session,
                ("sourceEventIds", Strings()),
                ("entityIds", Strings()),
                ("supersedesMemoryId", Prop("string")),
                ("subject", Prop("string")),
                ("predicate", Prop("string")),
                ("object", Prop("string")),
                ("validFrom", Prop("string")),
                ("validUntil", Prop("string"))
            ),
            input => Post("/v1/memories", input)
        ),
        new(
            "retract_memory",
            "Retract a curated memory without deleting its event or provenance history.",
            Schema(["id"], ("id", Prop("string")), ("reason", Prop("string"))),
            input =>
                Post(
                    $"/v1/memories/{Segment(input)}/retract",
                    input["reason"] is null
                        ? new JsonObject()
                        : new JsonObject { ["reason"] = Json.Str(input["reason"]) }
                )
        ),
        new(
            "record_memory_utility",
            "Record whether a retrieved memory was useful so future ranking can learn from actual use.",
            Schema(
                ["id", "useful"],
                ("id", Prop("string")),
                ("useful", Prop("boolean")),
                ("feedback", Prop("string"))
            ),
            input => Post($"/v1/memories/{Segment(input)}/utility", input)
        ),
        new(
            "archive_memory",
            "Archive a selected durable memory while preserving immutable events and provenance links.",
            Schema(["id"], ("id", Prop("string")), ("reason", Prop("string"))),
            input =>
                Post(
                    $"/v1/memories/{Segment(input)}/archive",
                    input["reason"] is null
                        ? new JsonObject()
                        : new JsonObject { ["reason"] = Json.Str(input["reason"]) }
                )
        ),
        new(
            "upsert_entity",
            "Create or update a canonical entity used to connect memories and events. The result includes a traceId receipt for the audit console.",
            Schema(
                ["name", "type"],
                ("name", Prop("string")),
                ("type", Prop("string", options: Vocabulary.EntityTypes)),
                ("aliases", Strings()),
                ("description", Prop("string")),
                User,
                Project,
                Session
            ),
            input => Post("/v1/entities", input)
        ),
        new(
            "upsert_document",
            "Persist a source document that can be chunked and cited by claims. The result includes a traceId receipt.",
            Schema(
                ["title", "source"],
                ("title", Prop("string")),
                ("source", Prop("string")),
                ("content", Prop("string")),
                Metadata,
                User,
                Project,
                Session
            ),
            input => Post("/v1/documents", input)
        ),
        new(
            "upsert_chunk",
            "Persist a source chunk and link it to a document and optional entities.",
            Schema(
                ["documentId", "content", "ordinal"],
                ("documentId", Prop("string")),
                ("content", Prop("string")),
                ("ordinal", Prop("integer", min: 0)),
                ("embedding", Prop("array", items: new JsonObject { ["type"] = "number" })),
                ("entityIds", Strings()),
                User,
                Project,
                Session
            ),
            input => Post("/v1/chunks", input)
        ),
        new(
            "create_claim",
            "Create a structured claim between entities with source chunk/event provenance.",
            Schema(
                ["statement", "predicate", "subjectEntityId"],
                ("statement", Prop("string")),
                ("predicate", Prop("string")),
                ("subjectEntityId", Prop("string")),
                ("objectEntityId", Prop("string")),
                ("confidence", Prop("number", min: 0, max: 1)),
                ("sourceChunkIds", Strings()),
                ("sourceEventIds", Strings()),
                User,
                Project,
                Session
            ),
            input => Post("/v1/claims", input)
        ),
        new(
            "create_procedure",
            "Persist a source-backed procedural runbook for a repeatable task.",
            Schema(
                ["title", "goal", "steps"],
                ("title", Prop("string")),
                ("goal", Prop("string")),
                ("steps", Strings()),
                ("preconditions", Strings()),
                ("knownFailures", Strings()),
                ("confidence", Prop("number", min: 0, max: 1)),
                User,
                Project,
                Session,
                ("sourceEventIds", Strings()),
                ("executable", Prop("boolean")),
                ("command", Prop("string")),
                ("parameters", new JsonObject { ["type"] = "object" }),
                ("safety", Safety())
            ),
            input => Post("/v1/procedures", input)
        ),
    ];

    public static IReadOnlyList<string> ToolNames => [.. Tools.Select(t => t.Name)];

    /// Answers one message, or returns null for a notification that needs no answer.
    public async Task<JsonObject?> Handle(JsonObject message)
    {
        var method = Json.Str(message["method"]);
        var id = message["id"];
        if (id is null)
            return null;
        try
        {
            JsonNode? result = method switch
            {
                "initialize" => new JsonObject
                {
                    ["protocolVersion"] = Json.Str(
                        (message["params"] as JsonObject)?["protocolVersion"],
                        ProtocolVersion
                    ),
                    ["capabilities"] = new JsonObject { ["tools"] = new JsonObject() },
                    ["serverInfo"] = new JsonObject
                    {
                        ["name"] = "vinci-memory",
                        ["version"] = "0.1.0",
                    },
                },
                "ping" => new JsonObject(),
                "tools/list" => new JsonObject
                {
                    ["tools"] = new JsonArray([
                        .. Tools.Select(t =>
                            (JsonNode)
                                new JsonObject
                                {
                                    ["name"] = t.Name,
                                    ["description"] = t.Description,
                                    ["inputSchema"] = Json.Clone(t.Schema),
                                }
                        ),
                    ]),
                },
                "tools/call" => await Call(message["params"] as JsonObject ?? []),
                _ => throw new McpError(-32601, $"method not found: {method}"),
            };
            return new JsonObject
            {
                ["jsonrpc"] = "2.0",
                ["id"] = id.DeepClone(),
                ["result"] = result,
            };
        }
        catch (McpError e)
        {
            return new JsonObject
            {
                ["jsonrpc"] = "2.0",
                ["id"] = id.DeepClone(),
                ["error"] = new JsonObject { ["code"] = e.Code, ["message"] = e.Message },
            };
        }
    }

    private sealed class McpError(int code, string message) : Exception(message)
    {
        public int Code => code;
    }

    /// A tool that fails is still a reply, marked as an error, so the agent can read why.
    private async Task<JsonObject> Call(JsonObject parameters)
    {
        var name = Json.Str(parameters["name"]);
        var tool = Array.Find(Tools, t => t.Name == name)
            ?? throw new McpError(-32602, $"unknown tool: {name}");
        var input = parameters["arguments"] as JsonObject ?? [];
        foreach (var required in tool.Schema["required"]!.AsArray())
            if (input[required!.GetValue<string>()] is null)
                return Reply($"{name}: {required} is required", isError: true);
        try
        {
            var (path, body) = tool.Route(input);
            using var request = new HttpRequestMessage(
                HttpMethod.Post,
                $"{apiUrl.TrimEnd('/')}{path}"
            )
            {
                Content = JsonContent.Create(body, options: Json.Options),
            };
            if (apiKey is not null)
                request.Headers.Authorization = new("Bearer", apiKey);
            using var response = await http.SendAsync(request);
            var data = await response.Content.ReadFromJsonAsync<JsonNode>(Json.Options);
            if (!response.IsSuccessStatusCode)
            {
                var trace =
                    data?["traceId"]?.GetValueKind() == JsonValueKind.String
                        ? $" (trace: {Json.Str(data["traceId"])})"
                        : "";
                return Reply(
                    $"{Json.Str(data?["error"], $"memory service returned {(int)response.StatusCode}")}{trace}",
                    isError: true
                );
            }
            return Reply(
                data?.ToJsonString(new JsonSerializerOptions(Json.Options) { WriteIndented = true })
                    ?? "null"
            );
        }
        catch (Exception e)
            when (e is HttpRequestException or JsonException or TaskCanceledException)
        {
            return Reply($"memory service unavailable: {e.Message}", isError: true);
        }
    }

    private static JsonObject Reply(string text, bool isError = false)
    {
        var reply = new JsonObject
        {
            ["content"] = new JsonArray(new JsonObject { ["type"] = "text", ["text"] = text }),
        };
        if (isError)
            reply["isError"] = true;
        return reply;
    }

    /// Reads one message per line until the input ends.
    public async Task Run(TextReader input, TextWriter output, CancellationToken stop)
    {
        while (!stop.IsCancellationRequested && await input.ReadLineAsync(stop) is { } line)
        {
            if (line.Trim().Length == 0)
                continue;
            JsonObject? answer;
            try
            {
                answer = JsonNode.Parse(line) is JsonObject message ? await Handle(message) : null;
            }
            catch (JsonException)
            {
                answer = new JsonObject
                {
                    ["jsonrpc"] = "2.0",
                    ["id"] = null,
                    ["error"] = new JsonObject { ["code"] = -32700, ["message"] = "parse error" },
                };
            }
            if (answer is null)
                continue;
            await output.WriteLineAsync(answer.ToJsonString(Json.Options));
            await output.FlushAsync(stop);
        }
    }
}
