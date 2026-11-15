using System.Diagnostics;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Harness;

public sealed record AgentOptions(int MaxTurns = 20, bool AllowScripts = false, int MaxDepth = 1);

public sealed record RunResult(string Final, IReadOnlyList<Message> Transcript, int Turns, bool Completed);

/// <summary>Agent = frozen model + harness. The loop only calls the model, runs the tools it asks for, and repeats.</summary>
public sealed class Agent(IModel model, HarnessFiles harness, string workspace, AgentOptions? options = null)
{
    private readonly AgentOptions _o = options ?? new();
    private readonly string _root = Path.GetFullPath(workspace);

    public Task<RunResult> RunAsync(string prompt, CancellationToken ct = default) =>
        RunAsync(harness.SystemPrompt(), prompt, 0, ct);

    private async Task<RunResult> RunAsync(string system, string prompt, int depth, CancellationToken ct)
    {
        var history = new List<Message> { new("user", [new TextBlock(prompt)]) };
        var tools = Tools(depth);
        for (var turn = 1; turn <= _o.MaxTurns; turn++)
        {
            var reply = await model.CompleteAsync(system, history, tools, ct);
            history.Add(reply);
            var calls = reply.Content.OfType<ToolUse>().ToList();
            if (calls.Count == 0)
                return new(reply.Text, history, turn, true);

            var results = new List<Block>();
            foreach (var call in calls)
            {
                var (output, error) = await ExecuteAsync(call, depth, ct);
                results.Add(new ToolResult(call.Id, output, error));
            }
            history.Add(new("user", results));
        }
        return new(history.LastOrDefault(m => m.Role == "assistant")?.Text ?? "", history, _o.MaxTurns, false);
    }

    private async Task<(string, bool)> ExecuteAsync(ToolUse call, int depth, CancellationToken ct)
    {
        // C8: plugins sit between the model and the tools. Here: declarative deny rules.
        foreach (var rule in harness.Get("plugins/deny.txt").Split('\n', StringSplitOptions.RemoveEmptyEntries))
        {
            var pattern = rule.Trim();
            if (pattern.Length == 0 || pattern.StartsWith('#'))
                continue;
            if (Denies(pattern, call.Name + " " + call.Input.ToJsonString()))
                return ($"Blocked by plugin rule: {pattern}", true);
        }
        try
        {
            return (await DispatchAsync(call, depth, ct), false);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ($"{e.GetType().Name}: {e.Message}", true);
        }
    }

    /// <summary>A rule that is not a valid regex still denies, as a literal substring.</summary>
    public static bool Denies(string pattern, string text)
    {
        try
        {
            return Regex.IsMatch(text, pattern, RegexOptions.None, TimeSpan.FromSeconds(1));
        }
        catch (ArgumentException)
        {
            return text.Contains(pattern, StringComparison.Ordinal);
        }
        catch (RegexMatchTimeoutException)
        {
            return true;
        }
    }

    private async Task<string> DispatchAsync(ToolUse call, int depth, CancellationToken ct)
    {
        string S(string key) =>
            call.Input[key]?.GetValue<string>() ?? throw new ArgumentException($"Missing '{key}'.");

        switch (call.Name)
        {
            case "list_dir":
            {
                var dir = Resolve(call.Input["path"]?.GetValue<string>() ?? ".");
                return string.Join(
                    "\n",
                    Directory
                        .EnumerateFileSystemEntries(dir)
                        .Select(p => Path.GetRelativePath(_root, p) + (Directory.Exists(p) ? "/" : ""))
                        .Order()
                );
            }
            case "read_file":
                return Trim(await File.ReadAllTextAsync(Resolve(S("path")), ct));
            case "write_file":
            {
                var path = Resolve(S("path"));
                Directory.CreateDirectory(Path.GetDirectoryName(path)!);
                await File.WriteAllTextAsync(path, S("content"), ct);
                return $"Wrote {Path.GetRelativePath(_root, path)}";
            }
            case "load_skill":
            {
                var p = harness.Path_("skills", S("name"));
                return p.Length > 0 ? harness.Get(p) : throw new ArgumentException("No such skill.");
            }
            case "delegate" when depth < _o.MaxDepth:
            {
                var p = harness.Path_("subagents", S("name"));
                if (p.Length == 0)
                    throw new ArgumentException("No such subagent.");
                var sub = await RunAsync(harness.Get(p), S("task"), depth + 1, ct);
                return Trim(sub.Final);
            }
            case "run_script" when _o.AllowScripts:
            {
                var p = harness.Path_("scripts", S("name"));
                if (p.Length == 0)
                    throw new ArgumentException("No such script.");
                return await RunScriptAsync(p, call.Input["args"]?.GetValue<string>() ?? "", ct);
            }
            default:
                throw new ArgumentException($"Unknown tool '{call.Name}'.");
        }
    }

    private async Task<string> RunScriptAsync(string scriptPath, string args, CancellationToken ct)
    {
        var tmp = Path.Combine(Path.GetTempPath(), Path.GetRandomFileName() + ".sh");
        await File.WriteAllTextAsync(tmp, harness.Get(scriptPath), ct);
        try
        {
            var psi = new ProcessStartInfo("bash")
            {
                WorkingDirectory = _root,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
            };
            psi.ArgumentList.Add(tmp);
            foreach (var a in args.Split(' ', StringSplitOptions.RemoveEmptyEntries))
                psi.ArgumentList.Add(a);
            using var proc = Process.Start(psi)!;
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(ct);
            timeout.CancelAfter(TimeSpan.FromSeconds(30));
            var stdout = proc.StandardOutput.ReadToEndAsync(timeout.Token);
            var stderr = proc.StandardError.ReadToEndAsync(timeout.Token);
            try
            {
                await proc.WaitForExitAsync(timeout.Token);
            }
            catch (OperationCanceledException)
            {
                proc.Kill(true);
                throw new TimeoutException("Script exceeded 30 seconds.");
            }
            return Trim($"exit {proc.ExitCode}\n{await stdout}{await stderr}");
        }
        finally
        {
            File.Delete(tmp);
        }
    }

    private string Resolve(string rel)
    {
        var full = Path.GetFullPath(Path.Combine(_root, rel));
        if (full != _root && !full.StartsWith(_root + Path.DirectorySeparatorChar))
            throw new UnauthorizedAccessException("Path escapes the workspace.");
        return full;
    }

    private static string Trim(string s) => s.Length <= 20_000 ? s : s[..20_000] + "\n…(truncated)";

    private List<ToolSpec> Tools(int depth)
    {
        static JsonObject Schema(params string[] props) =>
            new()
            {
                ["type"] = "object",
                ["properties"] = new JsonObject(
                    props.Select(p => KeyValuePair.Create<string, JsonNode?>(p, new JsonObject { ["type"] = "string" }))
                ),
            };

        var tools = new List<ToolSpec>
        {
            new("list_dir", "List a workspace directory.", Schema("path")),
            new("read_file", "Read a workspace file.", Schema("path")),
            new("write_file", "Write a workspace file.", Schema("path", "content")),
        };
        if (harness.Names("skills").Any())
            tools.Add(new("load_skill", "Read a skill by name.", Schema("name")));
        if (depth < _o.MaxDepth && harness.Names("subagents").Any())
            tools.Add(new("delegate", "Hand a task to a named subagent.", Schema("name", "task")));
        if (_o.AllowScripts && harness.Names("scripts").Any())
            tools.Add(new("run_script", "Run a harness script in the workspace.", Schema("name", "args")));
        return tools;
    }
}
