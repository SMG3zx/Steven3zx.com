using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Harness;

public abstract record Block;

public sealed record TextBlock(string Text) : Block;

public sealed record ToolUse(string Id, string Name, JsonObject Input) : Block;

public sealed record ToolResult(string ToolUseId, string Content, bool IsError) : Block;

public sealed record Message(string Role, IReadOnlyList<Block> Content)
{
    public string Text => string.Concat(Content.OfType<TextBlock>().Select(b => b.Text));
}

public sealed record ToolSpec(string Name, string Description, JsonObject Schema);

/// <summary>The model is a frozen function from context to a reply. Everything else is harness.</summary>
public interface IModel
{
    Task<Message> CompleteAsync(
        string system,
        IReadOnlyList<Message> history,
        IReadOnlyList<ToolSpec> tools,
        CancellationToken ct
    );
}

public sealed class AnthropicModel(
    HttpClient http,
    string apiKey,
    string model,
    int maxTokens = 4096
) : IModel
{
    public static AnthropicModel FromEnvironment() =>
        new(
            new HttpClient { BaseAddress = new Uri("https://api.anthropic.com/") },
            Environment.GetEnvironmentVariable("ANTHROPIC_API_KEY")
                ?? throw new InvalidOperationException("ANTHROPIC_API_KEY is not set."),
            Environment.GetEnvironmentVariable("HARNESS_MODEL") ?? "claude-sonnet-5-5"
        );

    public async Task<Message> CompleteAsync(
        string system,
        IReadOnlyList<Message> history,
        IReadOnlyList<ToolSpec> tools,
        CancellationToken ct
    )
    {
        var body = new JsonObject
        {
            ["model"] = model,
            ["max_tokens"] = maxTokens,
            ["system"] = system,
            ["messages"] = new JsonArray(history.Select(ToJson).ToArray()),
        };
        if (tools.Count > 0)
            body["tools"] = new JsonArray(
                tools
                    .Select(t =>
                        (JsonNode)
                            new JsonObject
                            {
                                ["name"] = t.Name,
                                ["description"] = t.Description,
                                ["input_schema"] = t.Schema.DeepClone(),
                            }
                    )
                    .ToArray()
            );

        using var req = new HttpRequestMessage(HttpMethod.Post, "v1/messages")
        {
            Content = JsonContent.Create(body),
        };
        req.Headers.Add("x-api-key", apiKey);
        req.Headers.Add("anthropic-version", "2023-06-01");
        using var res = await http.SendAsync(req, ct);
        var text = await res.Content.ReadAsStringAsync(ct);
        if (!res.IsSuccessStatusCode)
            throw new HttpRequestException($"Model call failed ({(int)res.StatusCode}): {text}");

        var blocks = new List<Block>();
        foreach (var b in JsonNode.Parse(text)!["content"]!.AsArray())
        {
            switch (b!["type"]!.GetValue<string>())
            {
                case "text":
                    blocks.Add(new TextBlock(b["text"]!.GetValue<string>()));
                    break;
                case "tool_use":
                    blocks.Add(
                        new ToolUse(
                            b["id"]!.GetValue<string>(),
                            b["name"]!.GetValue<string>(),
                            b["input"]!.AsObject()
                        )
                    );
                    break;
            }
        }
        return new Message("assistant", blocks);
    }

    private static JsonNode ToJson(Message m) =>
        new JsonObject
        {
            ["role"] = m.Role,
            ["content"] = new JsonArray(
                m.Content.Select(b =>
                        b switch
                        {
                            TextBlock t => new JsonObject { ["type"] = "text", ["text"] = t.Text },
                            ToolUse u => new JsonObject
                            {
                                ["type"] = "tool_use",
                                ["id"] = u.Id,
                                ["name"] = u.Name,
                                ["input"] = u.Input.DeepClone(),
                            },
                            ToolResult r => new JsonObject
                            {
                                ["type"] = "tool_result",
                                ["tool_use_id"] = r.ToolUseId,
                                ["content"] = r.Content,
                                ["is_error"] = r.IsError,
                            },
                            _ => throw new InvalidOperationException(),
                        }
                    )
                    .ToArray<JsonNode>()
            ),
        };
}
