using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Vinci.Chat;

/// The chat model, spoken through the OpenAI-compatible /v1/chat/completions.
/// llama-server (llama.cpp) serves this route, which is how the chat model runs
/// now. The thinking switch goes in `chat_template_kwargs`, the llama.cpp way of
/// passing `enable_thinking` to the Qwen3 chat template, so Qwen3 does not reason
/// on every turn unless CHAT_THINK=1.
///
/// Tools are not sent as native tool definitions. The model writes its action as
/// a JSON object in plain text and the conversation reads it. See Conversation.
public sealed class OpenAiChat(
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
            ["chat_template_kwargs"] = new JsonObject { ["enable_thinking"] = think },
        };
        using var reply = await http.PostAsJsonAsync(
            $"{baseUrl.TrimEnd('/')}/chat/completions",
            body,
            Options
        );
        if (!reply.IsSuccessStatusCode)
            throw new InvalidOperationException(
                $"chat model {model} returned {(int)reply.StatusCode}"
            );
        var root = await reply.Content.ReadFromJsonAsync<JsonObject>(Options)
            ?? throw new InvalidOperationException("chat model returned an empty reply");
        return root["choices"]?[0]?["message"]?["content"]?.GetValue<string>() ?? "";
    }
}
