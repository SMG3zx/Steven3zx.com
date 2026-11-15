using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Vinci.Memory;

/// What Codex runs on every prompt, tool use and reply: `vinci memory hook`
/// reads the event from standard input and saves it as part of the session's
/// conversation. It must never get in Codex's way, so every failure is
/// swallowed and anything that could not be sent is kept in a spool file
/// and sent with the next event.
public sealed class CodexHook(
    HttpClient http,
    string apiUrl,
    string? apiKey,
    string userId,
    string spoolFile,
    int maxContentChars
)
{
    public static CodexHook FromEnvironment(string home) =>
        new(
            new HttpClient { Timeout = TimeSpan.FromSeconds(8) },
            Env.BaseUrl("MEMORY_API_URL") ?? "http://127.0.0.1:4781",
            Env.Get("MEMORY_API_KEY"),
            Env.Get("MEMORY_USER_ID") ?? "default",
            Env.Get("MEMORY_HOOK_SPOOL_FILE") ?? Path.Combine(home, "codex-hook-spool.jsonl"),
            (int)Env.Number("MEMORY_HOOK_MAX_CHARS", 20000)
        );

    private static string? Text(JsonNode? node) =>
        node?.GetValueKind() == JsonValueKind.String && node.GetValue<string>().Trim().Length > 0
            ? node.GetValue<string>()
            : null;

    /// The words in a value that may be text, a list of parts, or an object holding some.
    private static string? ContentText(JsonNode? value) =>
        value switch
        {
            null => null,
            JsonArray list => list.Select(ContentText).Where(part => part is not null).ToList()
                is { Count: > 0 } parts
                ? string.Join('\n', parts)
                : null,
            JsonObject map => Text(map["text"]) ?? Text(map["content"]) ?? Text(map["output"]),
            _ => Text(value),
        };

    private string Trim(string value) =>
        value.Length > maxContentChars ? $"{value[..maxContentChars]}\n[truncated]" : value;

    private static string StableKey(params string?[] values) =>
        Convert.ToHexStringLower(
            SHA256.HashData(Encoding.UTF8.GetBytes(JsonSerializer.Serialize(values)))
        )[..32];

    private async Task<bool> Send(JsonNode body)
    {
        try
        {
            using var request = new HttpRequestMessage(
                HttpMethod.Post,
                $"{apiUrl}/v1/conversations"
            )
            {
                Content = JsonContent.Create(body, options: Json.Options),
            };
            if (apiKey is not null)
                request.Headers.Authorization = new("Bearer", apiKey);
            using var response = await http.SendAsync(request);
            return response.IsSuccessStatusCode;
        }
        catch (Exception e) when (e is HttpRequestException or TaskCanceledException)
        {
            return false;
        }
    }

    /// Sends what an earlier run could not, keeping what still cannot be sent.
    private async Task FlushSpool()
    {
        string[] pending;
        try
        {
            pending = (await File.ReadAllTextAsync(spoolFile)).Split(
                '\n',
                StringSplitOptions.RemoveEmptyEntries
            );
        }
        catch (IOException)
        {
            return;
        }
        var remaining = new List<string>();
        foreach (var line in pending)
        {
            try
            {
                if (JsonNode.Parse(line) is { } body && await Send(body))
                    continue;
            }
            catch (JsonException) { }
            remaining.Add(line);
        }
        try
        {
            await File.WriteAllTextAsync(
                spoolFile,
                remaining.Count > 0 ? string.Join('\n', remaining) + "\n" : ""
            );
        }
        catch (IOException) { }
    }

    private async Task Persist(
        JsonObject hookEvent,
        string kind,
        string content,
        JsonObject? metadata = null
    )
    {
        var sessionId = Text(hookEvent["session_id"]) ?? "unknown-session";
        var hookName = Text(hookEvent["hook_event_name"]) ?? "unknown";
        var turnId = Text(hookEvent["turn_id"]) ?? StableKey(hookName, content);
        var message = metadata is null ? [] : Json.Clone(metadata);
        message["hook"] = hookName;
        var body = new JsonObject
        {
            ["sessionId"] = sessionId,
            ["userId"] = userId,
            ["projectId"] = Text(hookEvent["cwd"])?.Replace('\\', '/'),
            ["messages"] = new JsonArray(
                new JsonObject
                {
                    ["kind"] = kind,
                    ["content"] = content,
                    ["idempotencyKey"] =
                        $"codex-hook:{sessionId}:{turnId}:{hookName}:{StableKey(kind, content)}",
                    ["metadata"] = message,
                }
            ),
        };
        await FlushSpool();
        if (await Send(body))
            return;
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(Path.GetFullPath(spoolFile))!);
            await File.AppendAllTextAsync(spoolFile, body.ToJsonString(Json.Options) + "\n");
        }
        catch (IOException)
        {
            // Capturing memory must never block Codex, even with nowhere to spool.
        }
    }

    private static void AssistantMessages(JsonNode? value, List<string> found)
    {
        switch (value)
        {
            case JsonArray list:
                foreach (var item in list)
                    AssistantMessages(item, found);
                break;
            case JsonObject map:
                if (
                    Text(map["role"]) == "assistant"
                    && (
                        ContentText(map["content"])
                        ?? ContentText(map["text"])
                        ?? ContentText(map["output"])
                    )
                        is { } text
                    && text.Trim().Length > 0
                )
                    found.Add(text);
                foreach (var (_, child) in map)
                    AssistantMessages(child, found);
                break;
        }
    }

    /// The last thing the assistant said in a transcript of JSON lines.
    public static async Task<string?> LastAssistantMessage(string? transcriptPath)
    {
        if (transcriptPath is null)
            return null;
        try
        {
            var values = new JsonArray();
            foreach (var line in await File.ReadAllLinesAsync(transcriptPath))
            {
                if (line.Trim().Length == 0)
                    continue;
                try
                {
                    values.Add(JsonNode.Parse(line));
                }
                catch (JsonException) { }
            }
            var found = new List<string>();
            AssistantMessages(values, found);
            return found.LastOrDefault();
        }
        catch (IOException)
        {
            return null;
        }
    }

    public async Task Run(TextReader input)
    {
        JsonObject hookEvent;
        try
        {
            hookEvent =
                JsonNode.Parse(await input.ReadToEndAsync()) as JsonObject
                ?? throw new JsonException();
        }
        catch (JsonException)
        {
            return;
        }
        switch (Text(hookEvent["hook_event_name"]))
        {
            case "UserPromptSubmit":
                if (Text(hookEvent["prompt"]) is { } prompt)
                    await Persist(hookEvent, "user_message", Trim(prompt));
                break;
            case "Stop":
                if (await LastAssistantMessage(Text(hookEvent["transcript_path"])) is { } reply)
                    await Persist(hookEvent, "agent_message", Trim(reply));
                break;
            case "PostToolUse":
                var tool = Text(hookEvent["tool_name"]) ?? "unknown-tool";
                var output = hookEvent["tool_output"] ?? hookEvent["tool_result"];
                var content = new JsonObject
                {
                    ["tool"] = tool,
                    ["input"] = hookEvent["tool_input"]?.DeepClone(),
                    ["output"] = output?.DeepClone(),
                }.ToJsonString(Json.Options);
                await Persist(
                    hookEvent,
                    "tool_result",
                    Trim(content),
                    new JsonObject { ["toolName"] = tool }
                );
                break;
        }
    }
}
