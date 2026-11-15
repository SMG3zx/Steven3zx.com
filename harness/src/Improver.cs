using System.Text;
using System.Text.Json.Nodes;

namespace Harness;

public sealed record Attempt(string Label, HarnessFiles Harness, RunResult Run, Evidence Evidence);

public sealed record Improvement(
    HarnessFiles Final,
    IReadOnlyList<Attempt> Record,
    IReadOnlyList<string> Notes
);

/// <summary>
/// The Harness² loop. Level 1 refines the harness for one task: reference, propose k candidates,
/// probe them, compose a successor, run the final. Level 2 reflects on the record and writes
/// EXPERIENCE.md into the library, the only thing that survives to the next task.
/// </summary>
public sealed class Improver(IModel model, AgentOptions agentOptions, int samples = 3)
{
    public async Task<Improvement> ImproveAsync(
        HarnessFiles h0,
        HarnessTask task,
        string libraryDir,
        int k,
        CancellationToken ct = default
    )
    {
        var experience = ReadExperience(libraryDir);
        var record = new List<Attempt>();
        var notes = new List<string>();

        // 1. Reference: run the base harness.
        var reference = await RunAsync("reference", h0, task, ct);
        record.Add(reference);

        // 2. Propose: k candidates, each seeing the base harness, evidence and library lessons.
        var proposals = await Task.WhenAll(
            Enumerable.Range(1, k).Select(i => ProposeAsync(i, h0, reference, task, experience, ct))
        );

        // 3. Probe: execute each candidate to collect evidence.
        var probed = new List<Attempt>();
        foreach (
            var (label, candidate, _) in proposals
                .Where(p => p.Harness is not null)
                .Select(p => (p.Label, p.Harness!, p.Why))
        )
            probed.Add(await RunAsync(label, candidate, task, ct));
        record.AddRange(probed);
        notes.AddRange(proposals.Select(p => $"{p.Label}: {p.Why}"));

        // 4. Compose: a successor built from all candidates and their evidence.
        var successor = await ComposeAsync(h0, reference, probed, task, ct) ?? h0;

        // 5. Final: execute the successor.
        var final = await RunAsync("final", successor, task, ct);
        record.Add(final);

        // Never ship a regression: keep the best-scoring harness, preferring the base on ties.
        var best = record
            .OrderByDescending(a => a.Evidence.Score)
            .ThenBy(a => a.Label == "reference" ? 0 : 1)
            .First();

        // 6-7. Reflect and update the library.
        await ReflectAsync(libraryDir, task, record, best, ct);
        new HarnessFiles(best.Harness.Files).Save(Path.Combine(libraryDir, "harnesses", task.Id));
        return new(best.Harness, record, notes);
    }

    /// <summary>Runs the harness several times: the score is the mean, the transcript is the worst run so the proposer sees a failure.</summary>
    private async Task<Attempt> RunAsync(
        string label,
        HarnessFiles h,
        HarnessTask task,
        CancellationToken ct
    )
    {
        var runs = new List<(RunResult Run, Evidence Evidence)>();
        for (var i = 0; i < samples; i++)
        {
            var ws = task.NewWorkspace();
            try
            {
                var run = await new Agent(
                    model,
                    h,
                    ws,
                    agentOptions with
                    {
                        TestCommand = task.TestCommand,
                    }
                ).RunAsync(task.Prompt, ct);
                runs.Add((run, task.Verify(ws)));
            }
            finally
            {
                Directory.Delete(ws, true);
            }
        }
        var worst = runs.OrderBy(r => r.Evidence.Score).First();
        return new(
            label,
            h,
            worst.Run,
            new(runs.Average(r => r.Evidence.Score), worst.Evidence.Notes)
        );
    }

    private static readonly string[] Angles =
    [
        "what the agent should read or check BEFORE it acts",
        "how the agent verifies its work and when it is allowed to call finish",
        "what the agent should do when its attempts keep failing or repeat",
        "how the agent should plan and break the task down",
        "which skill or subagent would help, written as a new skills/*.md or subagents/*.md file",
    ];

    private async Task<(string Label, HarnessFiles? Harness, string Why)> ProposeAsync(
        int i,
        HarnessFiles h0,
        Attempt reference,
        HarnessTask task,
        string experience,
        CancellationToken ct
    )
    {
        var angle = Angles[(i - 1) % Angles.Length];
        var prompt = new StringBuilder()
            .AppendLine(
                "You are NOT solving the task below. A separate AI agent attempted it and you are improving the written instructions that agent works under, its harness, so that it does better next time."
            )
            .AppendLine(
                "Do not write the task's solution. Do not edit the task's files. Only edit the harness files listed at the end."
            )
            .AppendLine($"\nThe task the agent was given:\n{task.Prompt}")
            .AppendLine($"\nLessons from earlier tasks:\n{experience}")
            .AppendLine($"\nThe agent's current harness:\n{Dump(h0)}")
            .AppendLine(
                $"\nThe agent's attempt scored {reference.Evidence.Score:0.00}. Result:\n{reference.Evidence.Notes}"
            )
            .AppendLine($"\nWhat the agent did:\n{Summarize(reference.Run)}")
            .AppendLine($"\nFocus your change on this one angle: {angle}.")
            .AppendLine(
                "Write short, general rules that would help on any task like this, not rules that only fit this exact task."
            )
            .AppendLine(EditFormat)
            .ToString();
        var (edits, why) = await AskForEditsAsync(prompt, h0, ct);
        if (edits.Count == 0)
            return ($"candidate-{i}", null, $"no usable edit ({why})");
        var candidate = KeepDenyRules(h0, h0.ApplyValid(edits, out var rejected));
        var note =
            rejected.Count == 0
                ? why
                : $"{why} [dropped non-harness paths: {string.Join(", ", rejected)}]";
        return candidate.Files.SequenceEqual(h0.Files)
            ? ($"candidate-{i}", null, $"no change to the harness ({note})")
            : ($"candidate-{i}", candidate, note);
    }

    private async Task<HarnessFiles?> ComposeAsync(
        HarnessFiles h0,
        Attempt reference,
        List<Attempt> candidates,
        HarnessTask task,
        CancellationToken ct
    )
    {
        var sb = new StringBuilder()
            .AppendLine($"Task:\n{task.Prompt}")
            .AppendLine($"\nBase harness (score {reference.Evidence.Score:0.00}):\n{Dump(h0)}");
        foreach (var c in candidates)
            sb.AppendLine(
                $"\n## {c.Label} score {c.Evidence.Score:0.00}\nChanged files:\n{Diff(h0, c.Harness)}\nEvidence:\n{c.Evidence.Notes}"
            );
        sb.AppendLine(
                "\nCompose one successor from the changes that helped. Drop the ones that did not."
            )
            .AppendLine(EditFormat);
        var (edits, _) = await AskForEditsAsync(sb.ToString(), h0, ct);
        return edits.Count == 0 ? null : KeepDenyRules(h0, h0.ApplyValid(edits, out _));
    }

    private async Task ReflectAsync(
        string libraryDir,
        HarnessTask task,
        List<Attempt> record,
        Attempt best,
        CancellationToken ct
    )
    {
        var sb = new StringBuilder($"Task {task.Id}\n");
        foreach (var a in record)
            sb.AppendLine(
                $"- {a.Label}: score {a.Evidence.Score:0.00}, {a.Run.Turns} turns\n{Diff(record[0].Harness, a.Harness)}"
            );
        sb.AppendLine($"Winner: {best.Label}.")
            .AppendLine(
                "Write 3 to 6 short bullet lessons for future tasks. Mark each [general] if it should help any task, or [specific] if it only helps this kind of task. Bullets only."
            );
        var reply = await model.CompleteAsync(
            "You distill lessons from harness experiments.",
            [new("user", [new TextBlock(sb.ToString())])],
            [],
            ct
        );
        Directory.CreateDirectory(libraryDir);
        File.AppendAllText(
            Path.Combine(libraryDir, "EXPERIENCE.md"),
            $"\n## {task.Id}\n{reply.Text.Trim()}\n"
        );
    }

    private async Task<(Dictionary<string, string?> Edits, string Why)> AskForEditsAsync(
        string prompt,
        HarnessFiles baseline,
        CancellationToken ct
    )
    {
        var reply = await model.CompleteAsync(
            "You improve an agent harness by editing its files. Reply with JSON only.",
            [new("user", [new TextBlock(prompt)])],
            [],
            ct
        );
        if (Environment.GetEnvironmentVariable("HARNESS_DEBUG") is { Length: > 0 })
            Console.Error.WriteLine($"--- model reply ---\n{reply.Text}\n---");
        return ParseEdits(reply.Text, baseline);
    }

    private static readonly System.Text.RegularExpressions.Regex Block = new(
        @"^=== (?<mode>delete |replace )?(?<path>\S+) ===[ \t]*\r?\n(?<body>.*?)(?:\r?\n)?^=== end ===",
        System.Text.RegularExpressions.RegexOptions.Multiline
            | System.Text.RegularExpressions.RegexOptions.Singleline
    );

    /// <summary>
    /// Reads the proposer's reply. A file block ADDS its text to the end of that file, because a small model
    /// asked to rewrite a whole file tends to drop the rules already in it; "replace" rewrites, "delete" removes.
    /// JSON edits are accepted as a fallback and always replace.
    /// </summary>
    public static (Dictionary<string, string?> Edits, string Why) ParseEdits(
        string text,
        HarnessFiles? baseline = null
    )
    {
        var blocks = new Dictionary<string, string?>();
        foreach (System.Text.RegularExpressions.Match m in Block.Matches(text))
        {
            var path = m.Groups["path"].Value;
            var body = m.Groups["body"].Value.TrimEnd() + "\n";
            var mode = m.Groups["mode"].Value.Trim();
            var existing = baseline?.Get(path) ?? "";
            blocks[path] =
                mode == "delete" ? null
                : mode == "replace" || existing.Length == 0 ? body
                : existing.TrimEnd() + "\n" + body;
        }
        if (blocks.Count > 0)
        {
            var why = text[..text.IndexOf("===", StringComparison.Ordinal)].Trim();
            return (blocks, why.Length > 0 ? why : "file blocks");
        }
        var edits = new Dictionary<string, string?>();
        var start = text.IndexOf('{');
        var end = text.LastIndexOf('}');
        if (start < 0 || end < start)
            return (edits, "unparseable reply");
        try
        {
            var json = JsonNode.Parse(text[start..(end + 1)])!.AsObject();
            foreach (var (path, content) in json["edits"]?.AsObject() ?? [])
                edits[path] = content?.GetValue<string>();
            return (edits, json["rationale"]?.GetValue<string>() ?? "");
        }
        catch (Exception e)
            when (e
                    is System.Text.Json.JsonException
                        or InvalidOperationException
                        or FormatException
            )
        {
            return (new(), "unparseable reply");
        }
    }

    private const string EditFormat = """
        Reply with one sentence of reasoning, then one block per harness file you want to change, in this shape:

        === guardrails.md ===
        - <one short rule you want to add>
        === end ===

        A block ADDS its lines to the end of that file, or creates the file if it does not exist, so do not repeat rules that are already there.
        To rewrite a whole file instead, write "=== replace NAME ===". To remove a file, write "=== delete NAME ===" then "=== end ===".
        The only files you may use: systemprompt.md, guardrails.md, memory.md, tool-guidance.md, subagents/NAME.md, skills/NAME.md, scripts/NAME, plugins/deny.txt.
        """;

    private static string ReadExperience(string dir)
    {
        var path = Path.Combine(dir, "EXPERIENCE.md");
        return File.Exists(path) ? File.ReadAllText(path) : "(none yet)";
    }

    /// <summary>Deny rules are append-only: a candidate may add rules but never drop the base harness's.</summary>
    public static HarnessFiles KeepDenyRules(HarnessFiles baseline, HarnessFiles candidate)
    {
        const string path = "plugins/deny.txt";
        var have = candidate.Get(path).Split('\n').Select(l => l.Trim()).ToHashSet();
        var missing = baseline
            .Get(path)
            .Split('\n')
            .Select(l => l.Trim())
            .Where(l => l.Length > 0 && !have.Contains(l))
            .ToList();
        if (missing.Count == 0)
            return candidate;
        var text = candidate.Get(path).TrimEnd();
        return candidate.Apply(
            new Dictionary<string, string?>
            {
                [path] = (text.Length > 0 ? text + "\n" : "") + string.Join("\n", missing) + "\n",
            }
        );
    }

    private static string Dump(HarnessFiles h) =>
        h.Files.Count == 0
            ? "(empty)"
            : string.Join("\n", h.Files.Select(f => $"--- {f.Key}\n{f.Value}"));

    private static string Diff(HarnessFiles a, HarnessFiles b)
    {
        var paths = a.Files.Keys.Union(b.Files.Keys).Order();
        var changed = paths
            .Where(p => a.Get(p) != b.Get(p))
            .Select(p => b.Files.ContainsKey(p) ? $"--- {p}\n{b.Get(p)}" : $"--- {p} (deleted)");
        var text = string.Join("\n", changed);
        return text.Length == 0 ? "(no change)" : text;
    }

    private static string Summarize(RunResult run)
    {
        var sb = new StringBuilder();
        foreach (var m in run.Transcript)
        foreach (var b in m.Content)
            sb.AppendLine(
                b switch
                {
                    TextBlock t => $"{m.Role}: {t.Text}",
                    ToolUse u => $"call {u.Name} {u.Input.ToJsonString()}",
                    ToolResult r => $"result{(r.IsError ? " (error)" : "")}: {r.Content}",
                    _ => "",
                }
            );
        var s = sb.ToString();
        return s.Length <= 6000 ? s : s[..6000] + "\n…(truncated)";
    }
}
