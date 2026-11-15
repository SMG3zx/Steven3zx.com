using System.Text.Json.Nodes;

namespace Harness.Tests;

/// <summary>Replays scripted replies. A reply may be a function of the call number.</summary>
sealed class ScriptedModel(params Func<string, Message>[] replies) : IModel
{
    public int Calls;
    public List<string> Systems = [];

    public Task<Message> CompleteAsync(
        string system,
        IReadOnlyList<Message> h,
        IReadOnlyList<ToolSpec> t,
        CancellationToken ct
    )
    {
        Systems.Add(system);
        var r = replies[Math.Min(Calls++, replies.Length - 1)];
        return Task.FromResult(r(system));
    }
}

public class HarnessTests
{
    static Message Text(string s) => new("assistant", [new TextBlock(s)]);

    static Message Finish(string s) => Call("finish", new() { ["summary"] = s });

    static Message Call(string name, JsonObject input) =>
        new("assistant", [new ToolUse("id" + name, name, input)]);

    static string Temp()
    {
        var d = Path.Combine(Path.GetTempPath(), "harness-test-" + Path.GetRandomFileName());
        Directory.CreateDirectory(d);
        return d;
    }

    [Fact]
    public async Task Loop_runs_tool_then_finishes()
    {
        var ws = Temp();
        var model = new ScriptedModel(
            _ => Call("write_file", new() { ["path"] = "a.txt", ["content"] = "hi" }),
            _ => Finish("done")
        );
        var run = await new Agent(model, new(), ws).RunAsync("go");
        Assert.True(run.Completed);
        Assert.Equal("hi", File.ReadAllText(Path.Combine(ws, "a.txt")));
        Assert.Equal(2, run.Turns);
    }

    [Fact]
    public async Task Workspace_escape_is_an_error_result_not_a_write()
    {
        var ws = Temp();
        var model = new ScriptedModel(
            _ => Call("write_file", new() { ["path"] = "../evil.txt", ["content"] = "x" }),
            _ => Finish("done")
        );
        var run = await new Agent(model, new(), ws).RunAsync("go");
        var result = run.Transcript.SelectMany(m => m.Content).OfType<ToolResult>().Single();
        Assert.True(result.IsError);
        Assert.False(File.Exists(Path.Combine(Path.GetDirectoryName(ws)!, "evil.txt")));
    }

    [Fact]
    public async Task Plugin_deny_rule_blocks_calls()
    {
        var h = new HarnessFiles().Apply(
            new Dictionary<string, string?> { ["plugins/deny.txt"] = "write_file" }
        );
        var model = new ScriptedModel(
            _ => Call("write_file", new() { ["path"] = "a.txt", ["content"] = "x" }),
            _ => Finish("done")
        );
        var ws = Temp();
        var run = await new Agent(model, h, ws).RunAsync("go");
        Assert.StartsWith(
            "Blocked",
            run.Transcript.SelectMany(m => m.Content).OfType<ToolResult>().Single().Content
        );
        Assert.False(File.Exists(Path.Combine(ws, "a.txt")));
    }

    [Fact]
    public void Invalid_deny_regex_falls_back_to_literal_match()
    {
        Assert.True(Agent.Denies("^[^\\]\\./backup$", "x ^[^\\]\\./backup$ y"));
        Assert.False(Agent.Denies("[unclosed", "harmless"));
        Assert.True(Agent.Denies("write_file", "write_file {}"));
    }

    [Fact]
    public async Task Announcing_without_a_tool_call_is_nudged_not_accepted()
    {
        var ws = Temp();
        var model = new ScriptedModel(
            _ => Text("I will write a.txt now."),
            _ => Call("write_file", new() { ["path"] = "a.txt", ["content"] = "x" }),
            _ => Finish("done")
        );
        var run = await new Agent(model, new(), ws).RunAsync("go");
        Assert.True(run.Completed);
        Assert.Equal("x", File.ReadAllText(Path.Combine(ws, "a.txt")));
        Assert.Contains(run.Transcript, m => m.Role == "user" && m.Text == Agent.Nudge);
    }

    [Fact]
    public async Task A_model_that_only_talks_is_stopped_after_the_nudge_limit_and_flagged()
    {
        var model = new ScriptedModel(_ => Text("thinking out loud"));
        var run = await new Agent(model, new(), Temp(), new(MaxNudges: 2)).RunAsync("go");
        Assert.False(run.Completed);
        Assert.Equal(3, model.Calls);
    }

    [Fact]
    public void Deny_rules_look_at_targets_not_free_text()
    {
        var write = new ToolUse(
            "i",
            "write_file",
            new() { ["path"] = "r.txt", ["content"] = "cannot copy to ../x" }
        );
        Assert.False(Agent.Denies("\\.\\./", Agent.RuleTarget(write)));
        var escape = new ToolUse(
            "i",
            "write_file",
            new() { ["path"] = "../r.txt", ["content"] = "hi" }
        );
        Assert.True(Agent.Denies("\\.\\./", Agent.RuleTarget(escape)));
    }

    [Fact]
    public async Task Identical_failing_calls_get_a_change_approach_hint()
    {
        var model = new ScriptedModel(_ => Call("read_file", new() { ["path"] = "missing" }));
        var run = await new Agent(model, new(), Temp(), new(MaxTurns: 4)).RunAsync("go");
        var last = run.Transcript.SelectMany(m => m.Content).OfType<ToolResult>().Last();
        Assert.Contains("identical call", last.Content);
    }

    [Fact]
    public async Task Turn_limit_stops_a_looping_model()
    {
        var model = new ScriptedModel(_ => Call("list_dir", new()));
        var run = await new Agent(model, new(), Temp(), new(MaxTurns: 3)).RunAsync("go");
        Assert.False(run.Completed);
        Assert.Equal(3, model.Calls);
    }

    [Fact]
    public async Task Subagent_runs_with_its_own_prompt()
    {
        var h = new HarnessFiles().Apply(
            new Dictionary<string, string?> { ["subagents/reviewer.md"] = "You are the reviewer." }
        );
        var model = new ScriptedModel(
            _ => Call("delegate", new() { ["name"] = "reviewer", ["task"] = "check" }),
            _ => Finish("looks fine"),
            _ => Finish("all done")
        );
        var run = await new Agent(model, h, Temp()).RunAsync("go");
        Assert.Contains("You are the reviewer.", model.Systems[1]);
        Assert.Equal("all done", run.Final);
    }

    [Fact]
    public void Edits_outside_the_eight_components_are_rejected()
    {
        var h = new HarnessFiles();
        Assert.Throws<InvalidOperationException>(() =>
            h.Apply(new Dictionary<string, string?> { ["../x"] = "y" })
        );
        Assert.Throws<InvalidOperationException>(() =>
            h.Apply(new Dictionary<string, string?> { ["src/Program.cs"] = "y" })
        );
    }

    [Fact]
    public void Save_and_load_round_trip()
    {
        var dir = Temp();
        var h = new HarnessFiles().Apply(
            new Dictionary<string, string?> { ["systemprompt.md"] = "a", ["skills/x.md"] = "b" }
        );
        h.Save(dir);
        Assert.Equal(h.Files, HarnessFiles.Load(dir).Files);
    }

    [Fact]
    public void Deny_rules_cannot_be_removed_only_added()
    {
        var baseline = new HarnessFiles().Apply(
            new Dictionary<string, string?> { ["plugins/deny.txt"] = "a\nb\n" }
        );
        var dropped = baseline.Apply(
            new Dictionary<string, string?> { ["plugins/deny.txt"] = null }
        );
        Assert.Equal(
            ["a", "b"],
            Improver
                .KeepDenyRules(baseline, dropped)
                .Get("plugins/deny.txt")
                .Split('\n', StringSplitOptions.RemoveEmptyEntries)
        );
        var added = baseline.Apply(
            new Dictionary<string, string?> { ["plugins/deny.txt"] = "a\nb\nc\n" }
        );
        Assert.Equal("a\nb\nc\n", Improver.KeepDenyRules(baseline, added).Get("plugins/deny.txt"));
    }

    [Fact]
    public void ParseEdits_reads_file_blocks_and_deletes()
    {
        var (edits, why) = Improver.ParseEdits(
            "Read first.\n\n=== guardrails.md ===\n- one\n- two\n=== end ===\n=== delete skills/old.md ===\n=== end ===\n"
        );
        Assert.Equal("Read first.", why);
        Assert.Equal("- one\n- two\n", edits["guardrails.md"]);
        var baseline = new HarnessFiles().Apply(
            new Dictionary<string, string?> { ["guardrails.md"] = "- old\n" }
        );
        Assert.Equal(
            "- old\n- one\n- two\n",
            Improver
                .ParseEdits("x\n=== guardrails.md ===\n- one\n- two\n=== end ===", baseline)
                .Edits["guardrails.md"]
        );
        Assert.Equal(
            "- new\n",
            Improver
                .ParseEdits("x\n=== replace guardrails.md ===\n- new\n=== end ===", baseline)
                .Edits["guardrails.md"]
        );
        Assert.Null(edits["skills/old.md"]);
    }

    [Fact]
    public void Non_harness_paths_are_dropped_but_valid_edits_still_apply()
    {
        var h = new HarnessFiles().ApplyValid(
            new Dictionary<string, string?> { ["acronym.py"] = "x", ["guardrails.md"] = "g" },
            out var rejected
        );
        Assert.Equal(["acronym.py"], rejected);
        Assert.Equal("g", h.Get("guardrails.md"));
        Assert.False(h.Files.ContainsKey("acronym.py"));
    }

    [Fact]
    public void ParseEdits_tolerates_prose_and_garbage()
    {
        var (edits, why) = Improver.ParseEdits(
            "Sure!\n{\"rationale\":\"r\",\"edits\":{\"memory.md\":\"m\",\"skills/a.md\":null}}\nbye"
        );
        Assert.Equal("r", why);
        Assert.Equal("m", edits["memory.md"]);
        Assert.Null(edits["skills/a.md"]);
        Assert.Empty(Improver.ParseEdits("no json").Edits);
    }

    [Fact]
    public async Task Improver_keeps_the_best_harness_and_writes_experience()
    {
        // The base harness cannot pass; any system prompt containing GOOD makes the agent write the file.
        var model = new ScriptedModel(sys =>
            sys.Contains("GOOD")
                ? Call("write_file", new() { ["path"] = "out.txt", ["content"] = "ok" })
            : sys.StartsWith("You improve")
                ? Text("{\"rationale\":\"r\",\"edits\":{\"systemprompt.md\":\"GOOD\"}}")
            : sys.StartsWith("You distill") ? Text("- [general] say GOOD")
            : Text("giving up")
        );
        var lib = Temp();
        var task = new HarnessTask("t", "make out.txt", null, [new("out.txt", "ok")]);
        // After the write the model sees GOOD again; make the second call finish.
        var finishing = new FinishAfterWrite(model);
        var result = await new Improver(finishing, new(MaxTurns: 4)).ImproveAsync(
            new HarnessFiles(),
            task,
            lib,
            2
        );
        Assert.Equal("GOOD", result.Final.Get("systemprompt.md"));
        Assert.Equal(1.0, result.Record.Max(a => a.Evidence.Score));
        Assert.Equal(0.0, result.Record[0].Evidence.Score);
        Assert.Contains("say GOOD", File.ReadAllText(Path.Combine(lib, "EXPERIENCE.md")));
        Assert.True(File.Exists(Path.Combine(lib, "harnesses", "t", "systemprompt.md")));
    }

    sealed class FinishAfterWrite(IModel inner) : IModel
    {
        public async Task<Message> CompleteAsync(
            string s,
            IReadOnlyList<Message> h,
            IReadOnlyList<ToolSpec> t,
            CancellationToken ct
        ) =>
            h.Any(m => m.Content.OfType<ToolResult>().Any())
                ? Finish("done")
                : await inner.CompleteAsync(s, h, t, ct);
    }
}

public class OllamaModelTests
{
    sealed class Capture(string reply) : HttpMessageHandler
    {
        public string Body = "";

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage r,
            CancellationToken ct
        )
        {
            Body = await r.Content!.ReadAsStringAsync(ct);
            return new(System.Net.HttpStatusCode.OK) { Content = new StringContent(reply) };
        }
    }

    [Fact]
    public async Task Maps_tool_calls_and_results_to_the_native_api()
    {
        var h = new Capture(
            """{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"read_file","arguments":{"path":"a"}}}]}}"""
        );
        var model = new OllamaModel(
            new HttpClient(h) { BaseAddress = new("http://x/") },
            "qwen3:8b"
        );
        var history = new List<Message>
        {
            new("user", [new TextBlock("go")]),
            new("assistant", [new ToolUse("c1", "list_dir", new())]),
            new("user", [new ToolResult("c1", "a.txt", false)]),
        };
        var reply = await model.CompleteAsync(
            "sys",
            history,
            [new("read_file", "d", new() { ["type"] = "object" })],
            default
        );

        var call = Assert.IsType<ToolUse>(Assert.Single(reply.Content));
        Assert.Equal("read_file", call.Name);
        Assert.Equal("a", call.Input["path"]!.GetValue<string>());

        var sent = System.Text.Json.Nodes.JsonNode.Parse(h.Body)!;
        Assert.False(sent["think"]!.GetValue<bool>());
        Assert.Equal("list_dir", sent["messages"]![3]!["tool_name"]!.GetValue<string>());
        Assert.Equal("tool", sent["messages"]![3]!["role"]!.GetValue<string>());
    }
}
