using System.Text.Json.Nodes;

namespace Harness.Tests;

/// <summary>Replays scripted replies. A reply may be a function of the call number.</summary>
sealed class ScriptedModel(params Func<string, Message>[] replies) : IModel
{
    public int Calls;
    public List<string> Systems = [];

    public Task<Message> CompleteAsync(string system, IReadOnlyList<Message> h, IReadOnlyList<ToolSpec> t, CancellationToken ct)
    {
        Systems.Add(system);
        var r = replies[Math.Min(Calls++, replies.Length - 1)];
        return Task.FromResult(r(system));
    }
}

public class HarnessTests
{
    static Message Text(string s) => new("assistant", [new TextBlock(s)]);

    static Message Call(string name, JsonObject input) => new("assistant", [new ToolUse("id" + name, name, input)]);

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
            _ => Text("done")
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
            _ => Text("done")
        );
        var run = await new Agent(model, new(), ws).RunAsync("go");
        var result = run.Transcript.SelectMany(m => m.Content).OfType<ToolResult>().Single();
        Assert.True(result.IsError);
        Assert.False(File.Exists(Path.Combine(Path.GetDirectoryName(ws)!, "evil.txt")));
    }

    [Fact]
    public async Task Plugin_deny_rule_blocks_calls()
    {
        var h = new HarnessFiles().Apply(new Dictionary<string, string?> { ["plugins/deny.txt"] = "write_file" });
        var model = new ScriptedModel(
            _ => Call("write_file", new() { ["path"] = "a.txt", ["content"] = "x" }),
            _ => Text("done")
        );
        var ws = Temp();
        var run = await new Agent(model, h, ws).RunAsync("go");
        Assert.StartsWith("Blocked", run.Transcript.SelectMany(m => m.Content).OfType<ToolResult>().Single().Content);
        Assert.False(File.Exists(Path.Combine(ws, "a.txt")));
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
        var h = new HarnessFiles().Apply(new Dictionary<string, string?> { ["subagents/reviewer.md"] = "You are the reviewer." });
        var model = new ScriptedModel(
            _ => Call("delegate", new() { ["name"] = "reviewer", ["task"] = "check" }),
            _ => Text("looks fine"),
            _ => Text("all done")
        );
        var run = await new Agent(model, h, Temp()).RunAsync("go");
        Assert.Contains("You are the reviewer.", model.Systems[1]);
        Assert.Equal("all done", run.Final);
    }

    [Fact]
    public void Edits_outside_the_eight_components_are_rejected()
    {
        var h = new HarnessFiles();
        Assert.Throws<InvalidOperationException>(() => h.Apply(new Dictionary<string, string?> { ["../x"] = "y" }));
        Assert.Throws<InvalidOperationException>(() => h.Apply(new Dictionary<string, string?> { ["src/Program.cs"] = "y" }));
    }

    [Fact]
    public void Save_and_load_round_trip()
    {
        var dir = Temp();
        var h = new HarnessFiles().Apply(new Dictionary<string, string?> { ["systemprompt.md"] = "a", ["skills/x.md"] = "b" });
        h.Save(dir);
        Assert.Equal(h.Files, HarnessFiles.Load(dir).Files);
    }

    [Fact]
    public void ParseEdits_tolerates_prose_and_garbage()
    {
        var (edits, why) = Improver.ParseEdits("Sure!\n{\"rationale\":\"r\",\"edits\":{\"memory.md\":\"m\",\"skills/a.md\":null}}\nbye");
        Assert.Equal("r", why);
        Assert.Equal("m", edits["memory.md"]);
        Assert.Null(edits["skills/a.md"]);
        Assert.Empty(Improver.ParseEdits("no json").Edits);
    }

    [Fact]
    public async Task Improver_keeps_the_best_harness_and_writes_experience()
    {
        // The base harness cannot pass; any system prompt containing GOOD makes the agent write the file.
        var model = new ScriptedModel(
            sys =>
                sys.Contains("GOOD") ? Call("write_file", new() { ["path"] = "out.txt", ["content"] = "ok" })
                : sys.StartsWith("You improve") ? Text("{\"rationale\":\"r\",\"edits\":{\"systemprompt.md\":\"GOOD\"}}")
                : sys.StartsWith("You distill") ? Text("- [general] say GOOD")
                : Text("giving up")
        );
        var lib = Temp();
        var task = new HarnessTask("t", "make out.txt", null, [new("out.txt", "ok")]);
        // After the write the model sees GOOD again; make the second call finish.
        var finishing = new FinishAfterWrite(model);
        var result = await new Improver(finishing, new(MaxTurns: 4)).ImproveAsync(new HarnessFiles(), task, lib, 2);
        Assert.Equal("GOOD", result.Final.Get("systemprompt.md"));
        Assert.Equal(1.0, result.Record.Max(a => a.Evidence.Score));
        Assert.Equal(0.0, result.Record[0].Evidence.Score);
        Assert.Contains("say GOOD", File.ReadAllText(Path.Combine(lib, "EXPERIENCE.md")));
        Assert.True(File.Exists(Path.Combine(lib, "harnesses", "t", "systemprompt.md")));
    }

    sealed class FinishAfterWrite(IModel inner) : IModel
    {
        public async Task<Message> CompleteAsync(string s, IReadOnlyList<Message> h, IReadOnlyList<ToolSpec> t, CancellationToken ct) =>
            h.Any(m => m.Content.OfType<ToolResult>().Any()) ? Text("done") : await inner.CompleteAsync(s, h, t, ct);
    }
}
