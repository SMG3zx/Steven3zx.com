using Harness;

static string? Flag(string[] args, string name) =>
    Array.IndexOf(args, name) is var i && i >= 0 && i + 1 < args.Length ? args[i + 1] : null;

const string usage =
    """
    harness run     <task.json> [--harness DIR] [--allow-scripts]
    harness improve <task.json> [--harness DIR] [--library DIR] [--k N] [--allow-scripts]

    Needs ANTHROPIC_API_KEY. HARNESS_MODEL overrides the model (default claude-sonnet-5-5).
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
var model = AnthropicModel.FromEnvironment();

if (args[0] == "run")
{
    var ws = task.NewWorkspace();
    var run = await new Agent(model, harness, ws, options).RunAsync(task.Prompt);
    var ev = task.Verify(ws);
    Console.WriteLine(run.Final);
    Console.WriteLine($"\nscore {ev.Score:0.00} in {run.Turns} turns\n{ev.Notes}\nworkspace: {ws}");
    return ev.Score >= 1 ? 0 : 1;
}

var library = Flag(args, "--library") ?? "library";
var k = int.TryParse(Flag(args, "--k"), out var kk) ? Math.Clamp(kk, 1, 5) : 3;
var result = await new Improver(model, options).ImproveAsync(harness, task, library, k);
foreach (var a in result.Record)
    Console.WriteLine($"{a.Label,-12} score {a.Evidence.Score:0.00}  turns {a.Run.Turns}");
result.Final.Save(Flag(args, "--out") ?? Path.Combine(library, "harnesses", task.Id));
Console.WriteLine($"best harness saved under {library}/harnesses/{task.Id}");
return 0;
