using System.Diagnostics;
using System.Text.Json;
using System.Text.Json.Nodes;
using Harness;

static string? Flag(string[] args, string name) =>
    Array.IndexOf(args, name) is var i && i >= 0 && i + 1 < args.Length ? args[i + 1] : null;

static int IntFlag(string[] args, string name, int fallback) =>
    int.TryParse(Flag(args, name), out var v) ? v : fallback;

const string usage = """
    harness run     <task.json> [--harness DIR] [--allow-scripts] [--trace]
    harness improve <task.json> [--harness DIR] [--library DIR] [--k N] [--allow-scripts]
    harness import-exercism <track-checkout> <out-dir>
    harness scan    <tasks-dir> [--harness DIR] [--limit N] [--samples S] [--seed N] [--turns N] [--out FILE]

    Uses the local Ollama server (OLLAMA_HOST, default 127.0.0.1:11434) with HARNESS_MODEL (default qwen3:8b)
    and HARNESS_NUM_CTX (default 16384). Set HARNESS_PROVIDER=anthropic plus ANTHROPIC_API_KEY to use Claude instead.
    Scripts run as bash on this machine, so --allow-scripts is off unless you pass it.
    Test commands run model-written code, and only run inside bubblewrap (no network, read-only filesystem).
    """;

if (args.Length < 2 || args[0] is not ("run" or "improve" or "import-exercism" or "scan"))
{
    Console.Error.WriteLine(usage);
    return 2;
}

if (args[0] == "import-exercism")
{
    var n = Exercism.Import(args[1], args[2], "python3 -m unittest -q {test}");
    Console.WriteLine($"wrote {n} tasks to {args[2]}");
    return 0;
}

IModel model =
    Environment.GetEnvironmentVariable("HARNESS_PROVIDER") == "anthropic"
        ? AnthropicModel.FromEnvironment()
        : OllamaModel.FromEnvironment();
var harness = HarnessFiles.Load(Flag(args, "--harness") ?? "examples/default-harness");
var options = new AgentOptions(
    MaxTurns: IntFlag(args, "--turns", 20),
    AllowScripts: args.Contains("--allow-scripts")
);

if (args[0] == "scan")
{
    // Headroom scan: how often does the current harness pass each task? Tasks that pass sometimes, but not
    // always, are the ones a recursive harness can learn from.
    var files = Directory.EnumerateFiles(args[1], "*.json").Order().ToList();
    var rng = new Random(IntFlag(args, "--seed", 1));
    files = [.. files.OrderBy(_ => rng.Next()).Take(IntFlag(args, "--limit", files.Count))];
    var samples = IntFlag(args, "--samples", 3);
    var outFile = Flag(args, "--out") ?? "scan.jsonl";
    File.WriteAllText(outFile, "");
    var rates = new List<double>();
    foreach (var f in files)
    {
        var task = HarnessTask.Load(f);
        var scores = new List<double>();
        var turns = new List<int>();
        var clock = Stopwatch.StartNew();
        for (var i = 0; i < samples; i++)
        {
            var ws = task.NewWorkspace();
            try
            {
                var run = await new Agent(
                    model,
                    harness,
                    ws,
                    options with
                    {
                        TestCommand = task.TestCommand,
                    }
                ).RunAsync(task.Prompt);
                scores.Add(task.Verify(ws).Score);
                turns.Add(run.Turns);
            }
            catch (Exception e) when (e is HttpRequestException or TaskCanceledException)
            {
                scores.Add(0);
                turns.Add(0);
            }
            finally
            {
                Directory.Delete(ws, true);
            }
        }
        var rate = scores.Average();
        rates.Add(rate);
        File.AppendAllText(
            outFile,
            new JsonObject
            {
                ["task"] = task.Id,
                ["rate"] = rate,
                ["scores"] = JsonSerializer.SerializeToNode(scores),
                ["turns"] = JsonSerializer.SerializeToNode(turns),
            }.ToJsonString() + "\n"
        );
        Console.WriteLine(
            $"{task.Id, -34} {string.Join(" ", scores.Select(s => s >= 1 ? "P" : "."))}  turns {string.Join("/", turns)}  {clock.Elapsed.TotalSeconds:0}s"
        );
    }
    var always = rates.Count(r => r >= 1);
    var never = rates.Count(r => r <= 0);
    Console.WriteLine(
        $"\n{rates.Count} tasks x {samples} samples: mean pass {rates.Average():P0}, always {always}, never {never}, sometimes {rates.Count - always - never} (learnable)"
    );
    return 0;
}

var task0 = HarnessTask.Load(args[1]);
var taskOptions = options with { TestCommand = task0.TestCommand };

if (args[0] == "run")
{
    var ws = task0.NewWorkspace();
    var run = await new Agent(model, harness, ws, taskOptions).RunAsync(task0.Prompt);
    var ev = task0.Verify(ws);
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
var k = Math.Clamp(IntFlag(args, "--k", 3), 1, 5);
var result = await new Improver(model, options).ImproveAsync(harness, task0, library, k);
foreach (var a in result.Record)
    Console.WriteLine($"{a.Label, -12} score {a.Evidence.Score:0.00}  turns {a.Run.Turns}");
result.Final.Save(Flag(args, "--out") ?? Path.Combine(library, "harnesses", task0.Id));
Console.WriteLine($"best harness saved under {library}/harnesses/{task0.Id}");
return 0;
