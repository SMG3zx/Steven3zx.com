using Harness;

static string? Flag(string[] args, string name) =>
    Array.IndexOf(args, name) is var i && i >= 0 && i + 1 < args.Length ? args[i + 1] : null;

const string usage = """
    harness run     <task.json> [--harness DIR] [--allow-scripts] [--trace]
    harness improve <task.json> [--harness DIR] [--library DIR] [--k N] [--allow-scripts]

    Uses the local Ollama server (OLLAMA_HOST, default 127.0.0.1:11434) with HARNESS_MODEL (default qwen3:8b)
    and HARNESS_NUM_CTX (default 16384). Set HARNESS_PROVIDER=anthropic plus ANTHROPIC_API_KEY to use Claude instead.
    Scripts run as bash on this machine, so --allow-scripts is off unless you pass it.
    """;

if (args.Length < 2 || args[0] is not ("run" or "improve"))
{
    Console.Error.WriteLine(usage);
    return 2;
}

var task = HarnessTask.Load(args[1]);
var harness = HarnessFiles.Load(Flag(args, "--harness") ?? "examples/default-harness");
var options = new AgentOptions(AllowScripts: args.Contains("--allow-scripts"));
IModel model =
    Environment.GetEnvironmentVariable("HARNESS_PROVIDER") == "anthropic"
        ? AnthropicModel.FromEnvironment()
        : OllamaModel.FromEnvironment();

if (args[0] == "run")
{
    var ws = task.NewWorkspace();
    var run = await new Agent(model, harness, ws, options).RunAsync(task.Prompt);
    var ev = task.Verify(ws);
    if (args.Contains("--trace"))
        foreach (var m in run.Transcript)
        foreach (var b in m.Content)
            Console.WriteLine(
                b switch
                {
                    TextBlock t => $"[{m.Role}] {t.Text}",
                    ToolUse u => $"[call] {u.Name} {u.Input.ToJsonString()}",
                    ToolResult r => $"[result{(r.IsError ? " error" : "")}] {r.Content}",
                    _ => "",
                }
            );
    Console.WriteLine(run.Final);
    Console.WriteLine($"\nscore {ev.Score:0.00} in {run.Turns} turns\n{ev.Notes}\nworkspace: {ws}");
    return ev.Score >= 1 ? 0 : 1;
}

var library = Flag(args, "--library") ?? "library";
var k = int.TryParse(Flag(args, "--k"), out var kk) ? Math.Clamp(kk, 1, 5) : 3;
var result = await new Improver(model, options).ImproveAsync(harness, task, library, k);
foreach (var a in result.Record)
    Console.WriteLine($"{a.Label, -12} score {a.Evidence.Score:0.00}  turns {a.Run.Turns}");
result.Final.Save(Flag(args, "--out") ?? Path.Combine(library, "harnesses", task.Id));
Console.WriteLine($"best harness saved under {library}/harnesses/{task.Id}");
return 0;
