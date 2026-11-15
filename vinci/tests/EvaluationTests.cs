using System.Net;
using System.Text;
using System.Text.Json.Nodes;
using Vinci.Memory;
using Vinci.Memory.Evaluation;

namespace Vinci.Tests;

public sealed class EvaluationTests : IDisposable
{
    private readonly string dir = Directory.CreateTempSubdirectory("vinci-eval").FullName;

    public void Dispose() => Directory.Delete(dir, recursive: true);

    private static string Serialize(object value) => System.Text.Json.JsonSerializer.Serialize(value);

    // ---- the simulator ----

    [Fact]
    public void A_simulation_is_deterministic_for_a_seed_and_differs_between_seeds()
    {
        Assert.Equal(Serialize(Simulator.Create(7)), Serialize(Simulator.Create(7)));
        Assert.NotEqual(Simulator.Create(7).ProjectId, Simulator.Create(8).ProjectId);
        // The generator and the name it makes are fixed, so a world is the same on every machine.
        // These three names are what the original TypeScript simulator produced for the same seeds.
        Assert.Equal(["sim-project-5epl", "sim-project-548s", "sim-project-52g7"], new[] { 42, 7, 1 }.Select(seed => Simulator.Create(seed).ProjectId));
    }

    [Fact]
    public void A_simulation_has_temporal_update_failure_forgetting_and_security_scenarios()
    {
        var world = Simulator.Create(42);
        Assert.True(world.Events.Count >= 9);
        Assert.Contains(world.Turns, t => t.UserIntent == "correct");
        Assert.True(world.Turns.Count(t => t.ExpectedProbeId is not null) >= 3);
        Assert.Equal(["update", "temporal", "fact", "contradiction", "contradiction", "forgetting", "security"], world.Probes.Select(p => p.Category));
        Assert.Contains(world.Events, e => !e.Trusted && e.Kind == "malicious_memory");
    }

    [Fact]
    public void The_oracle_scores_complete_answers_and_catches_a_leaked_secret()
    {
        var world = Simulator.Create(42);
        var complete = Simulator.SelfTest(42);
        Assert.Equal((1.0, 1.0, 1.0, 0.0, 0.0), (complete.HitRate, complete.ProvenanceRate, complete.SecurityRate, complete.MeanLatencyMs, complete.EstimatedCostUsd));
        var leaked = Simulator.Evaluate(world, [new SimulationAnswer("sim-probe-security", "atlas-secret-991", [world.Events[^1].MemoryId])]);
        Assert.False(leaked.Results.First(r => r.ProbeId == "sim-probe-security").SecurityCorrect);
        Assert.False(leaked.Results.First(r => r.ProbeId == "sim-probe-security").IsolationCorrect);
    }

    [Fact]
    public void Offline_regression_matrices_are_reproducible()
    {
        var reports = Simulator.OfflineMatrix([1, 2, 3]);
        Assert.All(reports, r => Assert.True(r.DatasetVersion == "sim-v1" && r.HitRate == 1 && r.SecurityRate == 1));
        Assert.Equal(Serialize(Simulator.OfflineMatrix([1])), Serialize(Simulator.OfflineMatrix([1])));
    }

    [Fact]
    public void Traces_have_the_shape_opentelemetry_expects()
    {
        var envelope = Simulator.ToOpenTelemetry([new SimulationTrace(new string('a', 32), new string('b', 16), "probe", "1", "2",
            new Dictionary<string, object> { ["simulation.hit"] = true, ["simulation.latency_ms"] = 3.0, ["simulation.probe_id"] = "p" })]);
        Assert.Equal("vinci-memory-simulator", envelope["resourceSpans"]![0]!["resource"]!["attributes"]![0]!["value"]!["stringValue"]!.GetValue<string>());
        var span = envelope["resourceSpans"]![0]!["scopeSpans"]![0]!["spans"]![0]!;
        Assert.Equal(new string('a', 32), span["traceId"]!.GetValue<string>());
        Assert.True(span["attributes"]![0]!["value"]!["boolValue"]!.GetValue<bool>());
        Assert.Equal(3.0, span["attributes"]![1]!["value"]!["doubleValue"]!.GetValue<double>());
        Assert.Equal("p", span["attributes"]![2]!["value"]!["stringValue"]!.GetValue<string>());
    }

    // ---- benchmark cases ----

    [Fact]
    public void The_bundled_questions_parse_and_other_datasets_are_accepted_by_their_own_field_names()
    {
        var bundled = Benchmark.ParseCases(Benchmark.BundledQuestions());
        Assert.Equal(15, bundled.Count);
        Assert.Contains(bundled, c => c.ReasoningIntensive && c.Id == "multi-hop-001");
        Assert.Contains(bundled, c => c.ShouldAbstain);

        var external = Benchmark.ParseCases("""
            {"question":"Who wrote it?","answer":"Leonardo da Vinci wrote it","category":"multi-hop"}
            {"query":"","answer":"skipped"}
            {"id":"x","query":"Q","gold":["alpha"],"shouldAbstain":true,"requiresProvenance":false}
            """);
        Assert.Equal(2, external.Count);
        Assert.Equal(("external-1", "multi-hop", true, true), (external[0].Id, external[0].Category, external[0].RequiresProvenance, external[0].ReasoningIntensive));
        Assert.Contains("leonardo", external[0].ExpectedContains.Select(t => t.ToLowerInvariant()));
        Assert.Equal((true, false), (external[1].ShouldAbstain, external[1].RequiresProvenance));
        Assert.Throws<FormatException>(() => Benchmark.ParseCases("""{"a":1}""", "json"));
        Assert.Empty(Benchmark.ParseCases("  "));
    }

    // ---- LongMemEval ----

    private const string Fixture = """
        [{
          "question_id": "fixture-1", "question_type": "knowledge-update", "question": "Which provider is current?", "answer": "Qwen3",
          "question_date": "2026/01/03", "haystack_session_ids": ["s1", "s2"], "haystack_dates": ["2026/01/01", "2026/01/02"],
          "haystack_sessions": [[{"role":"user","content":"We used a hosted API."}], [{"role":"user","content":"We switched to Qwen3.","has_answer":true}]],
          "answer_session_ids": ["s2"]
        }]
        """;

    [Fact]
    public void LongMemEval_sessions_become_isolated_deterministic_events()
    {
        var (first, second) = (LongMemEval.Parse(Fixture)[0], LongMemEval.Parse(Fixture)[0]);
        Assert.Equal(first.DatasetVersion, second.DatasetVersion);
        Assert.Equal(first.Events, second.Events);
        Assert.Equal("s1", first.Events[0].SessionId);
        Assert.True(first.Events[1].HasAnswer);
        Assert.Equal("2026-01-02T00:00:00.000Z", first.Events[1].OccurredAt);
        Assert.StartsWith("longmemeval-project-", first.ProjectId);
        Assert.Throws<FormatException>(() => LongMemEval.Parse("""[{"question_id":"x"}]"""));
        Assert.Throws<FormatException>(() => LongMemEval.Parse(Fixture.Replace("""["2026/01/01", "2026/01/02"]""", """["2026/01/01"]""")));
    }

    [Fact]
    public void LongMemEval_scores_gold_sessions_independently_of_the_answer_text()
    {
        var item = LongMemEval.Parse(Fixture)[0];
        Assert.True(LongMemEval.Recall(item, [item.Events[1].Id]).RecallAll);
        Assert.False(LongMemEval.Recall(item, [item.Events[0].Id]).RecallAll);
        Assert.True(LongMemEval.Recall(item, ["unknown"]) is { RecallAny: false, OverlapCount: 0 });
        Assert.Equal(1, LongMemEval.AnswerTermCoverage("The current provider is Qwen3", item.Answer));
        Assert.Equal(0.5, LongMemEval.AnswerTermCoverage("alpha only", "alpha beta"));
    }

    // ---- the hook ----

    private sealed class Api(bool up) : HttpMessageHandler
    {
        public List<JsonObject> Bodies { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken stop)
        {
            if (!up) throw new HttpRequestException("down");
            Bodies.Add((JsonObject)JsonNode.Parse(await request.Content!.ReadAsStringAsync(stop))!);
            return new HttpResponseMessage(HttpStatusCode.Created) { Content = new StringContent("{}", Encoding.UTF8, "application/json") };
        }
    }

    private CodexHook Hook(Api api, int maxChars = 20000) =>
        new(new HttpClient(api), "http://memory.test", null, "me", Path.Combine(dir, "spool.jsonl"), maxChars);

    [Fact]
    public async Task The_hook_saves_a_prompt_with_its_project_and_a_stable_idempotency_key()
    {
        var api = new Api(true);
        const string Event = """{"hook_event_name":"UserPromptSubmit","session_id":"s1","turn_id":"t1","cwd":"C:\\work\\glider","prompt":"Build the wing"}""";
        await Hook(api).Run(new StringReader(Event));
        await Hook(api).Run(new StringReader(Event));
        var (a, b) = (api.Bodies[0], api.Bodies[1]);
        Assert.Equal(("s1", "me", "C:/work/glider"), (a["sessionId"]!.GetValue<string>(), a["userId"]!.GetValue<string>(), a["projectId"]!.GetValue<string>()));
        var message = a["messages"]![0]!;
        Assert.Equal(("user_message", "Build the wing", "UserPromptSubmit"), (message["kind"]!.GetValue<string>(), message["content"]!.GetValue<string>(), message["metadata"]!["hook"]!.GetValue<string>()));
        Assert.StartsWith("codex-hook:s1:t1:UserPromptSubmit:", message["idempotencyKey"]!.GetValue<string>());
        Assert.Equal(message["idempotencyKey"]!.GetValue<string>(), b["messages"]![0]!["idempotencyKey"]!.GetValue<string>());
    }

    [Fact]
    public async Task The_hook_saves_a_tool_result_and_the_last_assistant_reply_and_trims_long_content()
    {
        var api = new Api(true);
        await Hook(api, maxChars: 30).Run(new StringReader("""{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls"},"tool_output":"a very long listing of files that goes on"}"""));
        var tool = api.Bodies[0]["messages"]![0]!;
        Assert.Equal("tool_result", tool["kind"]!.GetValue<string>());
        Assert.EndsWith("\n[truncated]", tool["content"]!.GetValue<string>());
        Assert.Equal("Bash", tool["metadata"]!["toolName"]!.GetValue<string>());

        var transcript = Path.Combine(dir, "transcript.jsonl");
        await File.WriteAllTextAsync(transcript, """
            {"role":"user","content":"hi"}
            not json
            {"item":{"role":"assistant","content":[{"text":"First answer"}]}}
            {"role":"assistant","content":"Second answer"}
            """);
        await Hook(api).Run(new StringReader($$"""{"hook_event_name":"Stop","session_id":"s1","transcript_path":"{{transcript.Replace("\\", "\\\\")}}"}"""));
        Assert.Equal(("agent_message", "Second answer"), (api.Bodies[1]["messages"]![0]!["kind"]!.GetValue<string>(), api.Bodies[1]["messages"]![0]!["content"]!.GetValue<string>()));
        Assert.Null(await CodexHook.LastAssistantMessage(Path.Combine(dir, "missing.jsonl")));
    }

    [Fact]
    public async Task The_hook_spools_what_it_cannot_send_and_sends_it_with_the_next_event()
    {
        const string One = """{"hook_event_name":"UserPromptSubmit","session_id":"s1","turn_id":"t1","prompt":"first"}""";
        const string Two = """{"hook_event_name":"UserPromptSubmit","session_id":"s1","turn_id":"t2","prompt":"second"}""";
        await Hook(new Api(false)).Run(new StringReader(One));
        await Hook(new Api(false)).Run(new StringReader("not json"));
        var spool = Path.Combine(dir, "spool.jsonl");
        Assert.Single(await File.ReadAllLinesAsync(spool));

        var up = new Api(true);
        await Hook(up).Run(new StringReader(Two));
        Assert.Equal(["first", "second"], up.Bodies.Select(b => b["messages"]![0]!["content"]!.GetValue<string>()));
        Assert.Equal("", (await File.ReadAllTextAsync(spool)).Trim());
        // Events with nothing to save, and unknown ones, are ignored without a word.
        await Hook(up).Run(new StringReader("""{"hook_event_name":"SessionStart"}"""));
        await Hook(up).Run(new StringReader("""{"hook_event_name":"UserPromptSubmit","prompt":"  "}"""));
        Assert.Equal(2, up.Bodies.Count);
    }
}
