using System.Text.Json;
using System.Text.Json.Serialization;

namespace Harness;

public sealed record Check(string File, string Contains);

/// <summary>A task is a prompt, an optional fixture directory copied into a fresh workspace, and file checks that score the result.</summary>
public sealed record HarnessTask(string Id, string Prompt, string? Fixture, IReadOnlyList<Check> Checks)
{
    public static HarnessTask Load(string path)
    {
        var t =
            JsonSerializer.Deserialize<HarnessTask>(
                File.ReadAllText(path),
                new JsonSerializerOptions(JsonSerializerDefaults.Web)
            ) ?? throw new InvalidDataException("Empty task file.");
        var fixture = t.Fixture is null
            ? null
            : Path.GetFullPath(t.Fixture, Path.GetDirectoryName(Path.GetFullPath(path))!);
        return t with { Fixture = fixture };
    }

    public string NewWorkspace()
    {
        var dir = Path.Combine(Path.GetTempPath(), "harness-" + Path.GetRandomFileName());
        Directory.CreateDirectory(dir);
        if (Fixture is not null)
            foreach (var f in Directory.EnumerateFiles(Fixture, "*", SearchOption.AllDirectories))
            {
                var target = Path.Combine(dir, Path.GetRelativePath(Fixture, f));
                Directory.CreateDirectory(Path.GetDirectoryName(target)!);
                File.Copy(f, target);
            }
        return dir;
    }

    public Evidence Verify(string workspace)
    {
        if (Checks.Count == 0)
            return new(0, "Task has no checks.");
        var notes = new List<string>();
        var passed = 0;
        foreach (var c in Checks)
        {
            var path = Path.GetFullPath(Path.Combine(workspace, c.File));
            var ok =
                path.StartsWith(Path.GetFullPath(workspace))
                && File.Exists(path)
                && File.ReadAllText(path).Contains(c.Contains, StringComparison.Ordinal);
            if (ok)
                passed++;
            notes.Add($"{(ok ? "PASS" : "FAIL")} {c.File} contains \"{c.Contains}\"");
        }
        return new((double)passed / Checks.Count, string.Join("\n", notes));
    }
}

public sealed record Evidence(double Score, string Notes);
