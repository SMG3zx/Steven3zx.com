namespace Harness;

/// <summary>
/// The editable half of an agent. A harness is nothing but files, so it can be diffed, versioned,
/// cloned and rewritten by a model. The eight components follow Harness²:
/// C1 systemprompt.md, C2 guardrails.md, C3 memory.md, C4 tool-guidance.md,
/// C5 subagents/*.md, C6 skills/*.md, C7 scripts/*, C8 plugins/deny.txt.
/// </summary>
public sealed class HarnessFiles
{
    public static readonly string[] EditableRoots =
    [
        "systemprompt.md",
        "guardrails.md",
        "memory.md",
        "tool-guidance.md",
        "subagents/",
        "skills/",
        "scripts/",
        "plugins/",
    ];

    private readonly SortedDictionary<string, string> _files;

    public HarnessFiles(IEnumerable<KeyValuePair<string, string>>? files = null) =>
        _files = new(files?.ToDictionary(p => p.Key, p => p.Value) ?? []);

    public IReadOnlyDictionary<string, string> Files => _files;

    public string Get(string path) => _files.GetValueOrDefault(path, "");

    public IEnumerable<string> Names(string folder) =>
        _files
            .Keys.Where(k => k.StartsWith(folder + "/"))
            .Select(k => Path.GetFileNameWithoutExtension(k))
            .Order();

    public string Path_(string folder, string name) =>
        _files.Keys.FirstOrDefault(k =>
            k.StartsWith(folder + "/")
            && Path.GetFileNameWithoutExtension(k).Equals(name, StringComparison.Ordinal)
        ) ?? "";

    public HarnessFiles Clone() => new(_files);

    public static bool IsEditable(string path) =>
        !path.Contains("..")
        && !path.StartsWith('/')
        && EditableRoots.Any(r => r.EndsWith('/') ? path.StartsWith(r) : path == r);

    /// <summary>Applies path -> content edits (null content deletes). Paths outside the eight components are rejected.</summary>
    public HarnessFiles Apply(IReadOnlyDictionary<string, string?> edits)
    {
        var next = Clone();
        foreach (var (path, content) in edits)
        {
            if (!IsEditable(path))
                throw new InvalidOperationException(
                    $"'{path}' is not an editable harness component."
                );
            if (content is null)
                next._files.Remove(path);
            else
                next._files[path] = content;
        }
        return next;
    }

    /// <summary>Applies the edits that target editable components and reports the ones that do not.</summary>
    public HarnessFiles ApplyValid(
        IReadOnlyDictionary<string, string?> edits,
        out List<string> rejected
    )
    {
        rejected = [.. edits.Keys.Where(k => !IsEditable(k))];
        return Apply(edits.Where(e => IsEditable(e.Key)).ToDictionary(e => e.Key, e => e.Value));
    }

    public static HarnessFiles Load(string dir)
    {
        var files = new Dictionary<string, string>();
        if (Directory.Exists(dir))
            foreach (var f in Directory.EnumerateFiles(dir, "*", SearchOption.AllDirectories))
            {
                var rel = System.IO.Path.GetRelativePath(dir, f).Replace('\\', '/');
                if (IsEditable(rel))
                    files[rel] = File.ReadAllText(f);
            }
        return new(files);
    }

    public void Save(string dir)
    {
        if (Directory.Exists(dir))
            Directory.Delete(dir, true);
        foreach (var (rel, content) in _files)
        {
            var target = System.IO.Path.Combine(dir, rel);
            Directory.CreateDirectory(System.IO.Path.GetDirectoryName(target)!);
            File.WriteAllText(target, content);
        }
    }

    /// <summary>C1-C4 and the skill index become the system prompt; skill bodies load on demand.</summary>
    public string SystemPrompt()
    {
        var parts = new List<string>();
        void Add(string title, string path)
        {
            var text = Get(path).Trim();
            if (text.Length > 0)
                parts.Add($"# {title}\n{text}");
        }
        Add("Instructions", "systemprompt.md");
        Add("Guardrails", "guardrails.md");
        Add("Memory", "memory.md");
        Add("Tool guidance", "tool-guidance.md");
        var skills = Names("skills").ToList();
        if (skills.Count > 0)
            parts.Add("# Skills\nCall load_skill to read one: " + string.Join(", ", skills));
        var agents = Names("subagents").ToList();
        if (agents.Count > 0)
            parts.Add(
                "# Subagents\nCall delegate to hand off a task: " + string.Join(", ", agents)
            );
        var scripts = Names("scripts").ToList();
        if (scripts.Count > 0)
            parts.Add("# Scripts\nCall run_script to run one: " + string.Join(", ", scripts));
        return parts.Count == 0 ? "You are a careful assistant." : string.Join("\n\n", parts);
    }
}
