using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Vinci.Memory;

/// The optional model services the memory system can lean on. Each is off
/// until its base URL is set, and each takes a handler so tests can stand in
/// for the network.
public abstract class ProviderClient(string? baseUrl, HttpMessageHandler? handler)
{
    protected string? BaseUrl { get; } = baseUrl?.TrimEnd('/') is { Length: > 0 } url ? url : null;
    protected HttpClient Http { get; } = handler is null ? new HttpClient() : new HttpClient(handler);

    public bool Enabled => BaseUrl is not null;

    protected async Task<JsonNode?> Post(string path, object body, string service)
    {
        using var reply = await Http.PostAsJsonAsync($"{BaseUrl}{path}", body, Json.Options);
        if (!reply.IsSuccessStatusCode) throw new InvalidOperationException($"{service} returned {(int)reply.StatusCode}");
        return await reply.Content.ReadFromJsonAsync<JsonNode>(Json.Options);
    }
}

/// Text to vectors, by POST /v1/embeddings (the OpenAI shape, which Ollama,
/// llama.cpp and vLLM all serve). The default model is Qwen3-Embedding-4B.
/// A query is wrapped in an instruction so the model embeds it as a question
/// rather than as a document; documents are embedded without one, as Qwen
/// specifies for that model.
public sealed class EmbeddingClient(string? baseUrl = null, HttpMessageHandler? handler = null)
    : ProviderClient(baseUrl ?? Env.Get("EMBEDDING_BASE_URL"), handler)
{
    public const string DefaultModel = "qwen3-embedding:4b";
    public const int DefaultDimensions = 2560;

    private readonly string instruction = Env.Get("EMBEDDING_QUERY_INSTRUCTION")
        ?? "Retrieve memories and source events relevant to the user's query";

    /// The model whose vectors are stored. Vectors from two models are not comparable,
    /// so the schema records this name and refuses to mix them.
    public string Model { get; } = Env.Get("EMBEDDING_MODEL") ?? DefaultModel;

    public Task<List<double[]>> EmbedDocuments(IReadOnlyList<string> inputs) => Request(inputs);

    public async Task<double[]> EmbedQuery(string input) =>
        (await Request([$"Instruct: {instruction}\nQuery: {input}"]))[0];

    private async Task<List<double[]>> Request(IReadOnlyList<string> inputs)
    {
        if (!Enabled) return [];
        var payload = await Post("/embeddings", new { model = Model, input = inputs }, "embedding service");
        var data = payload?["data"] as JsonArray;
        var rows = data?.Select((item, position) => (Index: (int)Json.Num(item?["index"], position), Vector: item?["embedding"] as JsonArray))
            .OrderBy(row => row.Index).ToList();
        if (rows is not { Count: > 0 } || rows.Count != inputs.Count || rows.Any(row => row.Vector is not { Count: > 0 }))
            throw new InvalidOperationException("embedding service returned an invalid vector response");
        return rows.Select(row => row.Vector!.Select(n => Json.Num(n, double.NaN)).ToArray()).ToList();
    }
}

public sealed record RerankCandidate(string Id, string Content, double Score = 0);

/// Reorders candidates by a cross-encoder's judgement, by POST /rerank.
public sealed class RerankerClient(string? baseUrl = null, HttpMessageHandler? handler = null)
    : ProviderClient(baseUrl ?? Env.Get("RERANKER_BASE_URL"), handler)
{
    public async Task<IReadOnlyList<RerankCandidate>> Rerank(string query, IReadOnlyList<RerankCandidate> candidates)
    {
        if (!Enabled || candidates.Count == 0) return candidates;
        var payload = await Post("/rerank", new { query, texts = candidates.Select(c => c.Content), top_n = candidates.Count }, "reranker service");
        if ((payload as JsonArray ?? payload?["results"] as JsonArray) is not { } results) throw Invalid();
        var ranked = new List<RerankCandidate>();
        foreach (var item in results)
        {
            var (index, score) = (Json.Num(item?["index"], double.NaN), Json.Num(item?["score"], double.NaN));
            if (index != Math.Floor(index) || !double.IsFinite(score)) throw Invalid();
            if (index >= 0 && index < candidates.Count) ranked.Add(candidates[(int)index] with { Score = score });
        }
        return ranked;

        static InvalidOperationException Invalid() => new("reranker service returned an invalid response");
    }
}

public sealed record FaithfulnessVerdict(bool Faithful, string JudgedBy, double? Score = null, string? Rationale = null);

/// An optional independent judge of whether an answer follows from its
/// evidence, by POST /judge.
public sealed class FaithfulnessJudge(string? baseUrl = null, HttpMessageHandler? handler = null)
    : ProviderClient(baseUrl ?? Env.Get("FAITHFULNESS_JUDGE_BASE_URL"), handler)
{
    public async Task<FaithfulnessVerdict?> Judge(string query, string answer, IReadOnlyList<string> evidence)
    {
        if (!Enabled) return null;
        var payload = await Post("/judge", new { query, answer, evidence }, "faithfulness judge");
        bool? verdict = payload?["faithful"]?.GetValueKind() is JsonValueKind.True or JsonValueKind.False ? Json.Flag(payload["faithful"])
            : Json.Str(payload?["verdict"]) switch { "supported" => true, "unsupported" => false, _ => null };
        if (verdict is null) throw new InvalidOperationException("faithfulness judge returned an invalid verdict");
        return new FaithfulnessVerdict(verdict.Value, "external",
            payload?["score"]?.GetValueKind() == JsonValueKind.Number ? Json.Num(payload["score"]) : null,
            payload?["rationale"]?.GetValueKind() == JsonValueKind.String ? Json.Str(payload["rationale"]) : null);
    }

    /// The fallback with no judge: the answer is not empty and the evidence
    /// contains every term the answer was expected to rest on.
    public static bool Lexical(string answer, IReadOnlyList<string> evidence, IReadOnlyList<string> expectedTerms)
    {
        var text = string.Join(' ', evidence).ToLowerInvariant();
        return answer.Trim().Length > 0 && expectedTerms.All(term => text.Contains(term.ToLowerInvariant(), StringComparison.Ordinal));
    }
}

/// State is "disabled", "ready", "reachable" (answers, but not healthy) or "unreachable".
public sealed record ProviderStatus(bool Configured, string State, int? HttpStatus = null)
{
    public static async Task<ProviderStatus> Probe(string? baseUrl, int timeoutMs = 1500, HttpMessageHandler? handler = null)
    {
        if (baseUrl?.TrimEnd('/') is not { Length: > 0 } normalized) return new(false, "disabled");
        using var http = handler is null ? new HttpClient() : new HttpClient(handler, disposeHandler: false);
        using var patience = new CancellationTokenSource(timeoutMs);
        try
        {
            using var reply = await http.GetAsync($"{normalized}/health", patience.Token);
            return new(true, reply.IsSuccessStatusCode ? "ready" : "reachable", (int)reply.StatusCode);
        }
        catch (Exception e) when (e is HttpRequestException or OperationCanceledException)
        {
            return new(true, "unreachable");
        }
    }
}
