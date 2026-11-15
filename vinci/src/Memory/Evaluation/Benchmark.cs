using System.Globalization;
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Vinci.Memory.Evaluation;

/// One question with what a good answer must contain.
public sealed record BenchmarkCase(string Id, string Category, string Query, IReadOnlyList<string> ExpectedContains,
    bool RequiresProvenance, bool ShouldAbstain, bool ReasoningIntensive);

/// Measures how well recall answers fixed questions, over HTTP, so it tests
/// the service as an agent would meet it. Reports are JSON.
public static partial class Benchmark
{
    [GeneratedRegex("[a-z0-9][a-z0-9_-]{2,}", RegexOptions.IgnoreCase)]
    private static partial Regex Word();

    [GeneratedRegex("bright|reasoning|multi[-_ ]?hop", RegexOptions.IgnoreCase)]
    private static partial Regex ReasoningWords();

    private static readonly JsonSerializerOptions Pretty = new(Json.Options) { WriteIndented = true };

    /// Reads questions from a JSON array or from JSON lines. Other datasets
    /// name the fields differently ("question", "answer", "gold"), so those
    /// are accepted too.
    public static List<BenchmarkCase> ParseCases(string text, string? format = null)
    {
        var trimmed = text.Trim();
        if (trimmed.Length == 0) return [];
        var lines = format == "jsonl" || (format != "json" && !trimmed.StartsWith('['));
        JsonNode? values = lines
            ? new JsonArray([.. trimmed.Split('\n', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries).Select(line => JsonNode.Parse(line))])
            : JsonNode.Parse(trimmed);
        if (values is not JsonArray array) throw new FormatException("benchmark case file must contain an array or JSONL objects");
        var cases = new List<BenchmarkCase>();
        for (var index = 0; index < array.Count; index++)
        {
            if (array[index] is not JsonObject item) continue;
            var expected = Json.Strings(item["expectedContains"] ?? item["expected"] ?? item["gold"]);
            if (expected.Count == 0 && item["answer"]?.GetValueKind() == JsonValueKind.String)
                expected = [.. Word().Matches(Json.Str(item["answer"])).Select(m => m.Value).Take(12)];
            var category = Json.Str(item["category"], "external");
            var query = Json.Str(item["query"] ?? item["question"]);
            if (query.Length == 0) continue;
            cases.Add(new BenchmarkCase(Json.Str(item["id"], $"external-{index + 1}"), category, query, expected,
                item["requiresProvenance"] is { } provenance ? Json.Flag(provenance) : true,
                Json.Flag(item["shouldAbstain"]),
                item["reasoningIntensive"] is { } reasoning ? Json.Flag(reasoning) : ReasoningWords().IsMatch(category)));
        }
        return cases;
    }

    public static string Sha256(string text) => Convert.ToHexStringLower(SHA256.HashData(Encoding.UTF8.GetBytes(text)));

    /// The questions that ship with vinci.
    public static string BundledQuestions()
    {
        using var stream = typeof(Benchmark).Assembly.GetManifestResourceStream("Vinci.Memory.Evaluation.questions.json")
            ?? throw new FileNotFoundException("questions.json");
        return new StreamReader(stream).ReadToEnd();
    }

    private static double P95(List<double> sorted) => sorted[Math.Min(sorted.Count - 1, (int)Math.Floor(sorted.Count * 0.95))];

    private static JsonObject Post(HttpClient http, string url, string? apiKey, string path, JsonNode body, out HttpRequestMessage request)
    {
        request = new HttpRequestMessage(HttpMethod.Post, $"{url}{path}") { Content = JsonContent.Create(body, options: Json.Options) };
        if (apiKey is not null) request.Headers.Authorization = new("Bearer", apiKey);
        using var response = http.Send(request);
        var parsed = JsonNode.Parse(response.Content.ReadAsStringAsync().GetAwaiter().GetResult()) as JsonObject ?? [];
        if (!response.IsSuccessStatusCode) throw new InvalidOperationException($"{path} returned {(int)response.StatusCode}: {Json.Str(parsed["error"])}");
        return parsed;
    }

    private static IEnumerable<string> EvidenceTexts(JsonObject result) =>
        [Json.Str(result["content"]), .. (result["evidence"] as JsonArray ?? []).Select(e => Json.Str(e?["content"]))];

    /// Asks every question and reports hit rate, abstention accuracy,
    /// attribution, faithfulness and latency. With BENCHMARK_STRICT=1 any
    /// miss is an error, so it can gate a change.
    public static async Task<JsonObject> Run(HttpClient http, string apiUrl, string? apiKey, string? casesFile = null)
    {
        var url = apiUrl.TrimEnd('/');
        var projectId = Env.Get("BENCHMARK_PROJECT_ID") ?? "benchmark-suite";
        var caseText = casesFile is null ? BundledQuestions() : await File.ReadAllTextAsync(casesFile);
        var format = Env.Get("BENCHMARK_CASES_FORMAT");
        var cases = ParseCases(caseText, format);
        var judge = new FaithfulnessJudge();
        var cost = Env.Number("MEMORY_RECALL_COST_USD", 0);
        var measurements = new List<JsonObject>();

        foreach (var testCase in cases)
        {
            var clock = System.Diagnostics.Stopwatch.StartNew();
            var body = await Task.Run(() => Post(http, url, apiKey, "/v1/recall", new JsonObject { ["query"] = testCase.Query, ["projectId"] = projectId, ["limit"] = 10 }, out _));
            var elapsed = clock.Elapsed.TotalMilliseconds;
            var results = (body["results"] as JsonArray ?? []).OfType<JsonObject>().ToList();
            var text = string.Join(' ', results.Select(r => Json.Str(r["content"]))).ToLowerInvariant();
            var hit = testCase.ExpectedContains.All(term => text.Contains(term.ToLowerInvariant(), StringComparison.Ordinal));
            bool Sourced(JsonObject r) => (r["sourceEventIds"] as JsonArray)?.Count > 0 || (r["sourceChunkIds"] as JsonArray)?.Count > 0;
            var attributed = !testCase.RequiresProvenance || results.Any(Sourced);
            var evidence = results.SelectMany(EvidenceTexts).ToList();
            var lexical = !testCase.RequiresProvenance || FaithfulnessJudge.Lexical(text, [string.Join(' ', evidence).ToLowerInvariant()], testCase.ExpectedContains);
            var external = testCase.RequiresProvenance && judge.Enabled ? await judge.Judge(testCase.Query, text, evidence) : null;
            var methods = results.SelectMany(r => (string[])[Json.Str(r["retrievalMethod"]), .. Json.Strings(r["retrievalMethods"])]).Where(m => m.Length > 0).Distinct().ToList();
            var abstain = Json.Flag(body["abstain"]);
            measurements.Add(new JsonObject
            {
                ["id"] = testCase.Id, ["category"] = testCase.Category, ["elapsedMs"] = Math.Round(elapsed, 2), ["hit"] = hit, ["attributed"] = attributed,
                ["faithfulness"] = external?.Faithful ?? lexical,
                ["reasoningSupport"] = !testCase.ReasoningIntensive || (methods.Contains("graph") && results.Any(r => r["sourceEventIds"] is JsonArray || r["sourceChunkIds"] is JsonArray)),
                ["retrievalMethods"] = new JsonArray([.. methods.Select(m => (JsonNode)m)]),
                ["faithfulnessJudge"] = external?.JudgedBy ?? "lexical-fallback", ["abstentionCorrect"] = abstain == testCase.ShouldAbstain,
                ["estimatedCostUsd"] = cost, ["resultCount"] = results.Count, ["abstain"] = abstain,
            });
        }

        var byId = cases.ToDictionary(c => c.Id, c => c);
        var latencies = measurements.Select(m => Json.Num(m["elapsedMs"])).Order().ToList();
        double Share(IEnumerable<JsonObject> among, string key) => among.Any() ? (double)among.Count(m => Json.Flag(m[key])) / among.Count() : 1;
        var provenance = measurements.Where(m => byId.TryGetValue(Json.Str(m["id"]), out var c) && c.RequiresProvenance).ToList();
        var reasoning = measurements.Where(m => byId.TryGetValue(Json.Str(m["id"]), out var c) && c.ReasoningIntensive).ToList();
        var report = new JsonObject
        {
            ["apiUrl"] = url,
            ["dataset"] = new JsonObject { ["source"] = casesFile ?? "bundled questions.json", ["sha256"] = Sha256(caseText), ["format"] = format ?? "json" },
            ["evaluation"] = new JsonObject
            {
                ["retrievalMethods"] = "service default", ["faithfulnessJudge"] = judge.Enabled ? "external" : "lexical-fallback", ["projectId"] = projectId,
                ["model"] = Env.Get("BENCHMARK_MODEL_ID") ?? "not specified", ["embedding"] = Env.Get("BENCHMARK_EMBEDDING_ID") ?? "not specified", ["reranker"] = Env.Get("BENCHMARK_RERANKER_ID") ?? "not specified",
            },
            ["cases"] = measurements.Count,
            ["hitRate"] = Share(measurements, "hit"),
            ["abstentionAccuracy"] = Share(measurements, "abstentionCorrect"),
            ["provenanceCases"] = provenance.Count,
            ["attributionRate"] = Share(provenance, "attributed"),
            ["faithfulnessRate"] = Share(provenance, "faithfulness"),
            ["reasoningSupportRate"] = Share(reasoning, "reasoningSupport"),
            ["estimatedCostUsd"] = measurements.Sum(m => Json.Num(m["estimatedCostUsd"])),
            ["meanLatencyMs"] = latencies.Average(),
            ["p95LatencyMs"] = P95(latencies),
            ["measurements"] = new JsonArray([.. measurements]),
        };

        // Compare with an earlier report, number by number.
        if (Env.Get("BENCHMARK_BASELINE_FILE") is { } baselineFile && File.Exists(baselineFile)
            && JsonNode.Parse(await File.ReadAllTextAsync(baselineFile)) is JsonObject baseline)
        {
            var (kept, delta) = (new JsonObject(), new JsonObject());
            foreach (var key in (string[])["hitRate", "abstentionAccuracy", "attributionRate", "faithfulnessRate", "reasoningSupportRate", "meanLatencyMs", "p95LatencyMs", "estimatedCostUsd"])
                if (baseline[key]?.GetValueKind() == JsonValueKind.Number)
                    (kept[key], delta[key]) = (Json.Num(baseline[key]), Json.Num(report[key]) - Json.Num(baseline[key]));
            (report["baseline"], report["delta"]) = (kept, delta);
        }
        if (Env.Get("BENCHMARK_OUTPUT_FILE") is { } output) await File.WriteAllTextAsync(output, report.ToJsonString(Pretty));

        if (Env.Get("BENCHMARK_STRICT") == "1")
        {
            var maxCost = Env.Get("BENCHMARK_MAX_COST_USD") is null ? double.PositiveInfinity : Env.Number("BENCHMARK_MAX_COST_USD", double.PositiveInfinity);
            if (new[] { "hitRate", "abstentionAccuracy", "attributionRate", "faithfulnessRate", "reasoningSupportRate" }.Any(key => Json.Num(report[key]) < 1) || Json.Num(report["estimatedCostUsd"]) > maxCost)
                throw new InvalidOperationException("benchmark strict gate failed");
            if (Env.Get("FAITHFULNESS_REQUIRE_JUDGE") == "1" && !judge.Enabled)
                throw new InvalidOperationException("benchmark strict gate requires FAITHFULNESS_JUDGE_BASE_URL");
        }
        return report;
    }

    /// Runs the same questions with each way of retrieving on its own, to
    /// show what each one contributes. Modes: lexical, vector, graph, hybrid.
    public static async Task<JsonObject> Ablate(HttpClient http, string apiUrl, string? apiKey, string? casesFile = null)
    {
        var url = apiUrl.TrimEnd('/');
        var projectId = Env.Get("BENCHMARK_PROJECT_ID") ?? "benchmark-suite";
        var caseText = casesFile is null ? BundledQuestions() : await File.ReadAllTextAsync(casesFile);
        var cases = ParseCases(caseText, Env.Get("BENCHMARK_CASES_FORMAT"));
        var methods = new Dictionary<string, string[]> { ["lexical"] = ["lexical"], ["vector"] = ["vector"], ["graph"] = ["graph"], ["hybrid"] = ["lexical", "vector", "graph"] };
        var modes = (Env.Get("ABLATION_MODES") ?? "lexical,graph,hybrid").Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);
        var reports = new JsonArray();
        foreach (var mode in modes)
        {
            if (!methods.TryGetValue(mode, out var used)) throw new InvalidOperationException($"unknown ablation mode: {mode}");
            var measurements = new List<(bool Hit, bool Abstention, double Elapsed, int Count)>();
            foreach (var testCase in cases)
            {
                var clock = System.Diagnostics.Stopwatch.StartNew();
                var body = await Task.Run(() => Post(http, url, apiKey, "/v1/recall", new JsonObject
                {
                    ["query"] = testCase.Query, ["projectId"] = projectId, ["limit"] = 10, ["retrievalMethods"] = new JsonArray([.. used.Select(m => (JsonNode)m)]),
                }, out _));
                var results = (body["results"] as JsonArray ?? []).OfType<JsonObject>().ToList();
                var text = string.Join(' ', results.Select(r => Json.Str(r["content"]))).ToLowerInvariant();
                measurements.Add((testCase.ExpectedContains.All(t => text.Contains(t.ToLowerInvariant(), StringComparison.Ordinal)),
                    Json.Flag(body["abstain"]) == testCase.ShouldAbstain, Math.Round(clock.Elapsed.TotalMilliseconds, 2), results.Count));
            }
            var latencies = measurements.Select(m => m.Elapsed).Order().ToList();
            reports.Add(new JsonObject
            {
                ["mode"] = mode, ["methods"] = new JsonArray([.. used.Select(m => (JsonNode)m)]), ["cases"] = measurements.Count,
                ["hitRate"] = (double)measurements.Count(m => m.Hit) / measurements.Count,
                ["abstentionAccuracy"] = (double)measurements.Count(m => m.Abstention) / measurements.Count,
                ["meanLatencyMs"] = measurements.Average(m => m.Elapsed), ["p95LatencyMs"] = P95(latencies),
            });
        }
        var report = new JsonObject
        {
            ["apiUrl"] = url, ["projectId"] = projectId,
            ["dataset"] = new JsonObject { ["source"] = casesFile ?? "bundled questions.json", ["sha256"] = Sha256(caseText) },
            ["reports"] = reports,
        };
        if (Env.Get("ABLATION_OUTPUT_FILE") is { } output) await File.WriteAllTextAsync(output, report.ToJsonString(Pretty));
        return report;
    }

    /// Writes the small fixed world the bundled questions ask about.
    public static async Task Seed(HttpClient http, string apiUrl, string? apiKey)
    {
        var url = apiUrl.TrimEnd('/');
        const string project = "benchmark-suite";
        async Task<JsonObject> Call(string path, JsonObject body) => await Task.Run(() => Post(http, url, apiKey, path, body, out _));
        JsonObject Event(string id, string kind, string content, string? session = null, bool failed = false) => new()
        {
            ["id"] = id, ["kind"] = kind, ["content"] = content, ["source"] = "benchmark", ["projectId"] = project, ["sessionId"] = session,
            ["metadata"] = failed ? new JsonObject { ["benchmark"] = true, ["status"] = "failed" } : new JsonObject { ["benchmark"] = true },
        };
        JsonArray Ids(params string[] ids) => new([.. ids.Select(id => (JsonNode)id)]);
        var first = await Call("/v1/events", Event("benchmark-event-embedding-decision", "decision", "The benchmark project decided to use Qwen3 embeddings for local semantic retrieval."));
        var later = await Call("/v1/events", Event("benchmark-event-later-session", "user_message", "In a later session, the benchmark project confirmed that Qwen3 remains the embedding provider.", "benchmark-session-2"));
        var reflection = await Call("/v1/events", Event("benchmark-event-reflection", "decision", "Reflection: retrieval quality improved when graph evidence and source citations were combined.", "benchmark-session-2"));
        var failureEvent = await Call("/v1/events", Event("benchmark-event-failure", "error", "Failure lesson: an invalid model endpoint prevents the memory worker from consolidating events.", "benchmark-session-2", failed: true));
        var entity = await Call("/v1/entities", new JsonObject { ["id"] = "benchmark-project", ["name"] = "Benchmark Project", ["type"] = "project", ["projectId"] = project });
        var document = await Call("/v1/documents", new JsonObject { ["id"] = "benchmark-document", ["title"] = "Benchmark source", ["source"] = "benchmark-fixture", ["content"] = "The benchmark source supports the Qwen3 embedding decision.", ["projectId"] = project });
        var chunk = await Call("/v1/chunks", new JsonObject
        {
            ["id"] = "benchmark-chunk", ["documentId"] = Json.Str(document["id"]), ["content"] = "Benchmark Project uses Qwen3 embeddings for local retrieval.",
            ["ordinal"] = 0, ["entityIds"] = Ids(Json.Str(entity["id"])), ["projectId"] = project,
        });
        await Call("/v1/claims", new JsonObject
        {
            ["id"] = "benchmark-claim", ["statement"] = "Benchmark Project uses Qwen3 embeddings.", ["predicate"] = "USES", ["subjectEntityId"] = Json.Str(entity["id"]),
            ["sourceChunkIds"] = Ids(Json.Str(chunk["id"])), ["projectId"] = project,
        });
        JsonObject Memory(string id, string content, string category, string? subject, string? predicate, string? obj, string[] sources, string? from = null, string? supersedes = null, string? session = null) => new()
        {
            ["id"] = id, ["content"] = content, ["category"] = category, ["subject"] = subject, ["predicate"] = predicate, ["object"] = obj, ["projectId"] = project,
            ["sessionId"] = session, ["validFrom"] = from, ["supersedesMemoryId"] = supersedes, ["sourceEventIds"] = Ids(sources),
        };
        var (e1, e2, e3) = (Json.Str(first["id"]), Json.Str(later["id"]), Json.Str(reflection["id"]));
        await Call("/v1/memories", Memory("benchmark-memory-fact", "The benchmark project uses Qwen3 embeddings for local semantic retrieval.", "decision", "Benchmark Project", "USES", "Qwen3 embeddings", [e1]));
        await Call("/v1/memories", Memory("benchmark-memory-multisession", "Across multiple sessions, the benchmark project confirmed that Qwen3 remains the embedding provider.", "semantic", "Benchmark Project", "CONFIRMED_PROVIDER", "Qwen3", [e1, e2]));
        var old = await Call("/v1/memories", Memory("benchmark-memory-old", "Before Qwen3, the benchmark project used a hosted embedding API.", "fact", "Benchmark Project", "EMBEDDING_PROVIDER", "hosted embedding API", [e1], "2023-01-01T00:00:00.000Z"));
        await Call("/v1/memories", Memory("benchmark-memory-current", "The current benchmark embedding provider is Qwen3.", "fact", "Benchmark Project", "EMBEDDING_PROVIDER", "Qwen3", [e1], "2024-01-01T00:00:00.000Z", Json.Str(old["id"])));
        await Call("/v1/procedures", new JsonObject
        {
            ["id"] = "benchmark-procedure", ["title"] = "Recover the memory worker", ["goal"] = "Restore the worker after a failed run.",
            ["steps"] = new JsonArray("Inspect failed jobs", "Restart the worker", "Verify the queue drains"), ["preconditions"] = new JsonArray("Neo4j is reachable"),
            ["knownFailures"] = new JsonArray("Invalid model endpoint"), ["projectId"] = project, ["sourceEventIds"] = Ids(e1),
        });
        await Call("/v1/memories", Memory("benchmark-memory-reflection", "Reflection: retrieval quality improved when graph evidence and source citations were combined.", "reflection", null, null, null, [e3], session: "benchmark-session-2"));
        await Call("/v1/memories", Memory("benchmark-memory-belief", "The benchmark project believes durable source-linked memories should outrank unsupported recollections.", "belief", null, null, null, [e1]));
        var failure = Memory("benchmark-memory-failure", "Failure lesson: an invalid model endpoint prevents the memory worker from consolidating events.", "failure", null, null, null, [Json.Str(failureEvent["id"])]);
        failure["metadata"] = new JsonObject { ["memoryKind"] = "reflexion_failure" };
        await Call("/v1/memories", failure);
        await Call("/v1/procedures", new JsonObject
        {
            ["id"] = "benchmark-executable-skill", ["title"] = "Inspect memory health", ["goal"] = "Check the memory API before retrieval.",
            ["steps"] = new JsonArray("Call the health endpoint", "Verify Neo4j connectivity"), ["executable"] = true, ["command"] = "GET /health",
            ["parameters"] = new JsonObject { ["timeoutMs"] = 3000 }, ["safety"] = "read-only", ["projectId"] = project, ["sourceEventIds"] = Ids(e1),
        });
        // An archived memory must stay out of recall: one question checks that it does.
        var archived = Memory("benchmark-memory-archived", "Archived benchmark-only secret: retired provider token alpha-991.", "working", null, null, null, [e1]);
        archived["status"] = "archived";
        await Call("/v1/memories", archived);
    }
}

/// LongMemEval: long chat histories with one question each, used to check
/// that recall finds the session that holds the answer. This adapter measures
/// retrieval only; the benchmark's own answer judge is not run.
public static class LongMemEval
{
    public sealed record Turn(string Role, string Content, bool HasAnswer);

    public sealed record Event(string Id, string Kind, string Content, string ProjectId, string SessionId, string UserId, string OccurredAt, string QuestionId, string DatasetVersion, int Turn, bool HasAnswer);

    public sealed record Case(string QuestionId, string QuestionType, string Question, string Answer, string DatasetVersion, string UserId, string ProjectId,
        IReadOnlyList<string> AnswerSessionIds, IReadOnlyList<Event> Events);

    public sealed record SessionRecall(IReadOnlyList<string> RetrievedSessions, IReadOnlyList<string> GoldSessions, bool RecallAny, bool RecallAll, int OverlapCount);

    private static string StableId(string value) => Simulator.StableHex(value, 8);

    private static string NormalizeDate(string? value, string fallback) =>
        value is not null && DateTimeOffset.TryParse(value.Replace('/', '-'), CultureInfo.InvariantCulture, DateTimeStyles.AssumeUniversal, out var at)
            ? at.UtcDateTime.ToString("yyyy-MM-ddTHH:mm:ss.fffZ", CultureInfo.InvariantCulture)
            : fallback;

    public static List<Case> Parse(string text, int limit = int.MaxValue)
    {
        if (JsonNode.Parse(text) is not JsonArray array) throw new FormatException("LongMemEval file must contain a JSON array");
        var cases = new List<Case>();
        for (var index = 0; index < Math.Min(limit, array.Count); index++)
        {
            var item = array[index] as JsonObject;
            if (item is null || item["question_id"] is null || Json.Str(item["question"]).Length == 0
                || item["haystack_sessions"] is not JsonArray sessions || item["haystack_session_ids"] is not JsonArray sessionIds || item["haystack_dates"] is not JsonArray dates)
                throw new FormatException($"invalid LongMemEval item at index {index}");
            var questionId = Json.Str(item["question_id"]);
            if (sessions.Count != sessionIds.Count || sessions.Count != dates.Count)
                throw new FormatException($"LongMemEval session arrays differ in length for {questionId}");
            var version = $"longmemeval:{StableId($"{questionId}:{Json.Str(item["question_type"], "unknown")}")}";
            var (userId, projectId) = ($"longmemeval-user-{StableId(questionId)}", $"longmemeval-project-{StableId(questionId)}");
            var events = new List<Event>();
            for (var s = 0; s < sessions.Count; s++)
            {
                if (sessions[s] is not JsonArray turns) throw new FormatException($"invalid session {s} for {questionId}");
                for (var t = 0; t < turns.Count; t++)
                {
                    var content = Json.Str(turns[t]?["content"]);
                    if (content.Trim().Length == 0) continue;
                    events.Add(new Event($"lme-{StableId($"{questionId}:{s}:{t}")}", Json.Str(turns[t]!["role"]) == "user" ? "user_message" : "agent_message", content, projectId,
                        Json.Str(sessionIds[s]), userId, NormalizeDate(Json.Str(dates[s]), $"2020-01-01T00:00:{s:00}Z"), questionId, version, t, Json.Flag(turns[t]!["has_answer"])));
                }
            }
            cases.Add(new Case(questionId, Json.Str(item["question_type"], "unknown"), Json.Str(item["question"]), Json.Str(item["answer"]), version, userId, projectId,
                Json.Strings(item["answer_session_ids"]), events));
        }
        return cases;
    }

    /// Which of the sessions that hold the answer did recall reach?
    public static SessionRecall Recall(Case instance, IEnumerable<string> resultIds)
    {
        var sessionOf = instance.Events.ToDictionary(e => e.Id, e => e.SessionId);
        var retrieved = resultIds.Where(sessionOf.ContainsKey).Select(id => sessionOf[id]).Distinct().ToList();
        var gold = instance.AnswerSessionIds.Distinct().ToList();
        var overlap = gold.Count(retrieved.Contains);
        return new SessionRecall(retrieved, gold, gold.Count == 0 || overlap > 0, gold.Count == 0 || overlap == gold.Count, overlap);
    }

    /// The share of the expected answer's words that the retrieved text contains.
    public static double AnswerTermCoverage(string answer, string expected)
    {
        var terms = Regex.Matches(expected.ToLowerInvariant(), "[a-z0-9][a-z0-9_-]{2,}").Select(m => m.Value).Distinct().ToList();
        var text = answer.ToLowerInvariant();
        return terms.Count > 0 ? (double)terms.Count(text.Contains) / terms.Count : 1;
    }

    public static async Task<JsonObject> Run(HttpClient http, string apiUrl, string? apiKey, string file)
    {
        var url = apiUrl.TrimEnd('/');
        var limit = Env.Get("LONGMEMEVAL_LIMIT") is null ? int.MaxValue : (int)Env.Number("LONGMEMEVAL_LIMIT", int.MaxValue);
        var recallLimit = (int)Env.Number("LONGMEMEVAL_RECALL_LIMIT", 20);
        var text = await File.ReadAllTextAsync(file);
        var cost = Env.Number("MEMORY_RECALL_COST_USD", 0);

        async Task<JsonObject> Call(string path, JsonNode body)
        {
            using var request = new HttpRequestMessage(HttpMethod.Post, $"{url}{path}") { Content = JsonContent.Create(body, options: Json.Options) };
            if (apiKey is not null) request.Headers.Authorization = new("Bearer", apiKey);
            using var response = await http.SendAsync(request);
            var reply = await response.Content.ReadAsStringAsync();
            return response.IsSuccessStatusCode ? JsonNode.Parse(reply) as JsonObject ?? [] : throw new InvalidOperationException($"{path} returned {(int)response.StatusCode}: {reply}");
        }

        var measurements = new List<JsonObject>();
        foreach (var instance in Parse(text, limit))
        {
            for (var offset = 0; offset < instance.Events.Count; offset += 100)
                await Call("/v1/events/batch", new JsonObject
                {
                    ["events"] = new JsonArray([.. instance.Events.Skip(offset).Take(100).Select(e => (JsonNode)new JsonObject
                    {
                        ["id"] = e.Id, ["kind"] = e.Kind, ["content"] = e.Content, ["source"] = "longmemeval", ["projectId"] = e.ProjectId, ["sessionId"] = e.SessionId,
                        ["userId"] = e.UserId, ["occurredAt"] = e.OccurredAt,
                        ["metadata"] = new JsonObject { ["datasetVersion"] = e.DatasetVersion, ["questionId"] = e.QuestionId, ["turn"] = e.Turn, ["hasAnswer"] = e.HasAnswer },
                    })]),
                });
            var clock = System.Diagnostics.Stopwatch.StartNew();
            var body = await Call("/v1/recall", new JsonObject
            {
                ["query"] = instance.Question, ["userId"] = instance.UserId, ["projectId"] = instance.ProjectId, ["limit"] = recallLimit, ["includeTestData"] = true,
            });
            var elapsed = clock.Elapsed.TotalMilliseconds;
            var results = (body["results"] as JsonArray ?? []).OfType<JsonObject>().ToList();
            var recall = Recall(instance, results.Select(r => Json.Str(r["id"])));
            measurements.Add(new JsonObject
            {
                ["questionId"] = instance.QuestionId, ["questionType"] = instance.QuestionType, ["datasetVersion"] = instance.DatasetVersion,
                ["recallAny"] = recall.RecallAny, ["recallAll"] = recall.RecallAll,
                ["answerTermCoverage"] = AnswerTermCoverage(string.Join(' ', results.Select(r => Json.Str(r["content"]))), instance.Answer),
                ["abstention"] = Json.Flag(body["abstain"]), ["elapsedMs"] = Math.Round(elapsed, 2), ["estimatedCostUsd"] = cost,
                ["goldSessions"] = new JsonArray([.. recall.GoldSessions.Select(s => (JsonNode)s)]), ["retrievedSessions"] = new JsonArray([.. recall.RetrievedSessions.Select(s => (JsonNode)s)]),
            });
        }
        double Mean(string key) => measurements.Average(m => Json.Num(m[key]));
        double Rate(string key) => (double)measurements.Count(m => Json.Flag(m[key])) / measurements.Count;
        var report = new JsonObject
        {
            ["dataset"] = "LongMemEval", ["protocol"] = "retrieval-only adapter; upstream end-to-end judge not run", ["source"] = file, ["sha256"] = Benchmark.Sha256(text),
            ["recallLimit"] = recallLimit, ["cases"] = measurements.Count,
            ["datasetVersions"] = new JsonArray([.. measurements.Select(m => Json.Str(m["datasetVersion"])).Distinct().Select(v => (JsonNode)v)]),
            ["recallAnyRate"] = Rate("recallAny"), ["recallAllRate"] = Rate("recallAll"), ["meanAnswerTermCoverage"] = Mean("answerTermCoverage"),
            ["meanLatencyMs"] = Mean("elapsedMs"), ["estimatedCostUsd"] = measurements.Sum(m => Json.Num(m["estimatedCostUsd"])), ["measurements"] = new JsonArray([.. measurements]),
        };
        if (Env.Get("LONGMEMEVAL_OUTPUT_FILE") is { } output) await File.WriteAllTextAsync(output, report.ToJsonString(new JsonSerializerOptions(Json.Options) { WriteIndented = true }));
        return report;
    }
}
