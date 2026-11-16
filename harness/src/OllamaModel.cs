using System.Net.Http.Json;
using System.Text.Json.Nodes;

namespace Harness;

/// <summary>
/// A local model through Ollama's native /api/chat. The native route is used because only it honours
/// <c>think: false</c>, and because it takes tool definitions directly. Ollama has no tool-call ids, so
/// they are made up here and tool results are matched to their call by position.
/// </summary>
public sealed class OllamaModel(
    HttpClient http,
    string model,
    int numCtx = 16384,
    bool think = false
) : IModel
{
    public static OllamaModel FromEnvironment() =>
        new(
            new HttpClient
            {
                BaseAddress = new Uri(
                    Environment.GetEnvironmentVariable("OLLAMA_HOST") is { Length: > 0 } h
                        ? (h.StartsWith("http") ? h : "http://" + h)
                        : "http://127.0.0.1:11434"
                ),
                Timeout = TimeSpan.FromMinutes(10),
            },
            Environment.GetEnvironmentVariable("HARNESS_MODEL") ?? "qwen3:8b",
            int.TryParse(Environment.GetEnvironmentVariable("HARNESS_NUM_CTX"), out var n)
                ? n
                : 16384
        );

    public async Task<Message> CompleteAsync(
        string system,
        IReadOnlyList<Message> history,
        IReadOnlyList<ToolSpec> tools,
        CancellationToken ct
    )
    {
        var messages = new JsonArray
        {
            new JsonObject { ["role"] = "system", ["content"] = system },
        };
        var toolNames = new Dictionary<string, string>();
        foreach (var m in history)
        {
            foreach (var u in m.Content.OfType<ToolUse>())
                toolNames[u.Id] = u.Name;
            if (m.Role == "assistant")
            {
                var msg = new JsonObject { ["role"] = "assistant", ["content"] = m.Text };
                var uses = m.Content.OfType<ToolUse>().ToList();
                if (uses.Count > 0)
                    msg["tool_calls"] = new JsonArray(
                        uses.Select(u =>
                                (JsonNode)
                                    new JsonObject
                                    {
                                        ["function"] = new JsonObject
                                        {
                                            ["name"] = u.Name,
                                            ["arguments"] = u.Input.DeepClone(),
                                        },
                                    }
                            )
                            .ToArray()
                    );
                messages.Add(msg);
                continue;
            }
            if (m.Text.Length > 0)
                messages.Add(new JsonObject { ["role"] = "user", ["content"] = m.Text });
            foreach (var r in m.Content.OfType<ToolResult>())
                messages.Add(
                    new JsonObject
                    {
                        ["role"] = "tool",
                        ["tool_name"] = toolNames.GetValueOrDefault(r.ToolUseId, ""),
                        ["content"] = r.IsError ? "Error: " + r.Content : r.Content,
                    }
                );
        }

        var body = new JsonObject
        {
            ["model"] = model,
            ["messages"] = messages,
            ["stream"] = false,
            ["think"] = think,
            ["options"] = new JsonObject { ["num_ctx"] = numCtx },
        };
        if (tools.Count > 0)
            body["tools"] = new JsonArray(
                tools
                    .Select(t =>
                        (JsonNode)
                            new JsonObject
                            {
                                ["type"] = "function",
                                ["function"] = new JsonObject
                                {
                                    ["name"] = t.Name,
                                    ["description"] = t.Description,
                                    ["parameters"] = t.Schema.DeepClone(),
                                },
                            }
                    )
                    .ToArray()
            );

        using var res = await http.PostAsJsonAsync("api/chat", body, ct);
        var text = await res.Content.ReadAsStringAsync(ct);
        if (!res.IsSuccessStatusCode)
            throw new HttpRequestException($"Model call failed ({(int)res.StatusCode}): {text}");

        var reply = JsonNode.Parse(text)!["message"]!;
        var blocks = new List<Block>();
        var content = reply["content"]?.GetValue<string>() ?? "";
        if (content.Length > 0)
            blocks.Add(new TextBlock(content));
        var n = 0;
        foreach (var call in reply["tool_calls"]?.AsArray() ?? [])
        {
            var fn = call!["function"]!;
            var args = fn["arguments"] switch
            {
                JsonObject o => o,
                JsonValue v when v.TryGetValue<string>(out var s) => JsonNode.Parse(s)!.AsObject(),
                _ => new JsonObject(),
            };
            blocks.Add(
                new ToolUse($"call_{Guid.NewGuid():N}_{n++}", fn["name"]!.GetValue<string>(), args)
            );
        }
        return new Message(
            "assistant",
            blocks,
            JsonNode.Parse(text)!["prompt_eval_count"]?.GetValue<int>() ?? 0
        );
    }
}
