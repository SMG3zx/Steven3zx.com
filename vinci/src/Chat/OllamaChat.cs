using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Vinci.Chat;

/// The chat model, spoken through Ollama's native /api/chat. The native route
/// is used because only it honours `think: false`, which keeps Qwen3 from
/// reasoning on every turn.
///
/// Tools are not sent as native tool definitions: with Qwen3 on this Ollama
/// build the model's tool calls were often dropped by the server's parser and
/// the turn came back empty. The model writes its action as a JSON object in
/// plain text instead, and the conversation reads it. See Conversation.
public sealed class OllamaChat(
    string baseUrl,
    string model,
    bool think,
    HttpMessageHandler? handler = null
)
{
    private static readonly JsonSerializerOptions Options = new(JsonSerializerDefaults.Web);
    private readonly HttpClient http = handler is null
        ? new HttpClient { Timeout = TimeSpan.FromMinutes(5) }
        : new HttpClient(handler);

    public string Model => model;

    public async Task<string> Send(IReadOnlyList<JsonObject> messages)
    {
        var body = new JsonObject
        {
            ["model"] = model,
            ["messages"] = new JsonArray([.. messages.Select(m => (JsonNode)m.DeepClone())]),
            ["stream"] = false,
            ["think"] = think,
        };
        using var reply = await http.PostAsJsonAsync(
            $"{baseUrl.TrimEnd('/')}/api/chat",
            body,
            Options
        );
        if (!reply.IsSuccessStatusCode)
            throw new InvalidOperationException(
                $"chat model {model} returned {(int)reply.StatusCode}"
            );
        var root = await reply.Content.ReadFromJsonAsync<JsonObject>(Options)
            ?? throw new InvalidOperationException("chat model returned an empty reply");
        return root["message"]?["content"]?.GetValue<string>() ?? "";
    }
}
