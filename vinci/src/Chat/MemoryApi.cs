using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Vinci.Chat;

/// The memory service is not running, or refused the call. The chat carries on without it.
public sealed class MemoryUnavailable(string message) : Exception(message);

/// The memory API as the chat uses it: recall, remember, record a conversation,
/// and sync journal facts. Every call is optional. When the service is down the
/// journal still works, and the chat says so instead of failing.
public sealed class MemoryApi(string baseUrl, string? apiKey, HttpMessageHandler? handler = null)
{
    private static readonly JsonSerializerOptions Options = new(JsonSerializerDefaults.Web);
    private readonly HttpClient http = handler is null
        ? new HttpClient { Timeout = TimeSpan.FromMinutes(2) }
        : new HttpClient(handler);

    public static MemoryApi FromEnvironment() =>
        new(
            (
                Environment.GetEnvironmentVariable("MEMORY_API_URL") ?? "http://127.0.0.1:4781"
            ).TrimEnd('/'),
            Environment.GetEnvironmentVariable("MEMORY_API_KEY") is { Length: > 0 } key ? key : null
        );

    public async Task<JsonNode?> Post(string path, object body)
    {
        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Post, $"{baseUrl}{path}")
            {
                Content = JsonContent.Create(body, options: Options),
            };
            if (apiKey is not null)
                request.Headers.Authorization = new("Bearer", apiKey);
            using var reply = await http.SendAsync(request);
            var data = await reply.Content.ReadFromJsonAsync<JsonNode>(Options);
            if (!reply.IsSuccessStatusCode)
                throw new MemoryUnavailable(
                    data?["error"]?.GetValue<string>()
                        ?? $"memory service returned {(int)reply.StatusCode}"
                );
            return data;
        }
        catch (Exception e)
            when (e
                    is HttpRequestException
                        or TaskCanceledException
                        or JsonException
                        or NotSupportedException
            )
        {
            throw new MemoryUnavailable(
                $"memory service unavailable at {baseUrl} ({e.Message}). Start it with: vinci memory serve"
            );
        }
    }
}
