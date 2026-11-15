using System.Text;
using System.Text.Json.Nodes;

namespace Harness;

public sealed record Attempt(string Label, HarnessFiles Harness, RunResult Run, Evidence Evidence);

public sealed record Improvement(HarnessFiles Final, IReadOnlyList<Attempt> Record);

/// <summary>
/// The Harness² loop. Level 1 refines the harness for one task: reference, propose k candidates,
/// probe them, compose a successor, run the final. Level 2 reflects on the record and writes
/// EXPERIENCE.md into the library, the only thing that survives to the next task.
/// </summary>
public sealed class Improver(IModel model, AgentOptions agentOptions)
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

        // 1. Reference: run the base harness.
        var reference = await RunAsync("reference", h0, task, ct);
        record.Add(reference);

        // 2. Propose: k candidates, each seeing the base harness, evidence and library lessons.
        var proposals = await Task.WhenAll(
            Enumerable.Range(1, k).Select(i => ProposeAsync(i, h0, reference, task, experience, ct))
        );

        // 3. Probe: execute each candidate to collect evidence.
        var probed = new List<Attempt>();
        foreach (var (label, candidate, _) in proposals.Where(p => p.Harness is not null).Select(p => (p.Label, p.Harness!, p.Why)))
            probed.Add(await RunAsync(label, candidate, task, ct));
        record.AddRange(probed);

        // 4. Compose: a successor built from all candidates and their evidence.
        var successor = await ComposeAsync(h0, reference, probed, task, ct) ?? h0;

        // 5. Final: execute the successor.
        var final = await RunAsync("final", successor, task, ct);
        record.Add(final);

        // Never ship a regression: keep the best-scoring harness, preferring the base on ties.
        var best = record.OrderByDescending(a => a.Evidence.Score).ThenBy(a => a.Label == "reference" ? 0 : 1).First();

        // 6-7. Reflect and update the library.
        await ReflectAsync(libraryDir, task, record, best, ct);
        new HarnessFiles(best.Harness.Files).Save(Path.Combine(libraryDir, "harnesses", task.Id));
        return new(best.Harness, record);
    }

    private async Task<Attempt> RunAsync(string label, HarnessFiles h, HarnessTask task, CancellationToken ct)
    {
        var ws = task.NewWorkspace();
        try
        {
            var run = await new Agent(model, h, ws, agentOptions).RunAsync(task.Prompt, ct);
            return new(label, h, run, task.Verify(ws));
        }
        finally
        {
            Directory.Delete(ws, true);
        }
    }

    private async Task<(string Label, HarnessFiles? Harness, string Why)> ProposeAsync(
        int i,
        HarnessFiles h0,
        Attempt reference,
        HarnessTask task,
        string experience,
        CancellationToken ct
    )
    {
        var prompt = new StringBuilder()
            .AppendLine($"Task:\n{task.Prompt}")
            .AppendLine($"\nLibrary lessons from earlier tasks:\n{experience}")
            .AppendLine($"\nBase harness:\n{Dump(h0)}")
            .AppendLine($"\nReference run (score {reference.Evidence.Score:0.00}):\n{reference.Evidence.Notes}")
            .AppendLine($"\nTranscript:\n{Summarize(reference.Run)}")
            .AppendLine($"\nCandidate {i} of several. Make a different, focused change from the others.")
            .AppendLine(EditFormat)
            .ToString();
        var (edits, why) = await AskForEditsAsync(prompt, ct);
        try
        {
            return ($"candidate-{i}", h0.Apply(edits), why);
        }
        catch (InvalidOperationException)
        {
            return ($"candidate-{i}", null, why);
        }
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
            sb.AppendLine($"\n## {c.Label} score {c.Evidence.Score:0.00}\nChanged files:\n{Diff(h0, c.Harness)}\nEvidence:\n{c.Evidence.Notes}");
        sb.AppendLine("\nCompose one successor from the changes that helped. Drop the ones that did not.")
            .AppendLine(EditFormat);
        var (edits, _) = await AskForEditsAsync(sb.ToString(), ct);
        try
        {
            return h0.Apply(edits);
        }
        catch (InvalidOperationException)
        {
            return null;
        }
    }

    private async Task ReflectAsync(string libraryDir, HarnessTask task, List<Attempt> record, Attempt best, CancellationToken ct)
    {
        var sb = new StringBuilder($"Task {task.Id}\n");
        foreach (var a in record)
            sb.AppendLine($"- {a.Label}: score {a.Evidence.Score:0.00}, {a.Run.Turns} turns\n{Diff(record[0].Harness, a.Harness)}");
        sb.AppendLine($"Winner: {best.Label}.")
            .AppendLine("Write 3 to 6 short bullet lessons for future tasks. Mark each [general] if it should help any task, or [specific] if it only helps this kind of task. Bullets only.");
        var reply = await model.CompleteAsync("You distill lessons from harness experiments.", [new("user", [new TextBlock(sb.ToString())])], [], ct);
        Directory.CreateDirectory(libraryDir);
        File.AppendAllText(Path.Combine(libraryDir, "EXPERIENCE.md"), $"\n## {task.Id}\n{reply.Text.Trim()}\n");
    }

    private async Task<(Dictionary<string, string?> Edits, string Why)> AskForEditsAsync(string prompt, CancellationToken ct)
    {
        var reply = await model.CompleteAsync(
            "You improve an agent harness by editing its files. Reply with JSON only.",
            [new("user", [new TextBlock(prompt)])],
            [],
            ct
        );
        return ParseEdits(reply.Text);
    }

    public static (Dictionary<string, string?> Edits, string Why) ParseEdits(string text)
    {
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
        catch (Exception e) when (e is System.Text.Json.JsonException or InvalidOperationException or FormatException)
        {
            return (new(), "unparseable reply");
        }
    }

    private const string EditFormat =
        """
        Reply with JSON only: {"rationale": "...", "edits": {"<path>": "<full new content, or null to delete>"}}.
        Editable paths: systemprompt.md, guardrails.md, memory.md, tool-guidance.md, subagents/*.md, skills/*.md, scripts/*, plugins/deny.txt.
        """;

    private static string ReadExperience(string dir)
    {
        var path = Path.Combine(dir, "EXPERIENCE.md");
        return File.Exists(path) ? File.ReadAllText(path) : "(none yet)";
    }

    private static string Dump(HarnessFiles h) =>
        h.Files.Count == 0 ? "(empty)" : string.Join("\n", h.Files.Select(f => $"--- {f.Key}\n{f.Value}"));

    private static string Diff(HarnessFiles a, HarnessFiles b)
    {
        var paths = a.Files.Keys.Union(b.Files.Keys).Order();
        var changed = paths.Where(p => a.Get(p) != b.Get(p)).Select(p => b.Files.ContainsKey(p) ? $"--- {p}\n{b.Get(p)}" : $"--- {p} (deleted)");
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
