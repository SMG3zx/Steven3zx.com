namespace Vinci;

/// The life of an idea. It moves forward one stage at a time and each move
/// has a gate (see Session.Advance). Tend has no exit: a grown idea is kept
/// up, or retired on purpose with a reason. It is never just dropped.
public enum Stage { Seed, Prepare, Incubate, Build, Tend, Retired }

public sealed class Idea(string id, string title, DateTimeOffset born)
{
    public string Id => id;
    public string Title => title;
    public DateTimeOffset Born { get; } = born;
    public Stage Stage { get; set; } = Stage.Seed;
    public string? Goal { get; set; }
    public DateTimeOffset LastTouched { get; set; } = born;
    public DateTimeOffset? IncubateUntil { get; set; }
    /// Time actually spent working on the idea.
    public TimeSpan Touch { get; set; }
    public List<(DateTimeOffset At, string What)> Ships { get; } = [];
    public string? RetiredWhy { get; set; }
}

public sealed class Requirement(string id, string ideaId, string text, string who, string why)
{
    public string Id => id;
    public string IdeaId => ideaId;
    public string Text => text;
    /// The person who asked for it. A name, never a department.
    public string Who => who;
    public string Why => why;
    public string? CutWhy { get; set; }
    public bool Live => CutWhy is null;
}

public sealed record Note(string Id, DateTimeOffset At, string Text, List<string> Refs);

public sealed record Source(string Id, DateTimeOffset At, string Citation, string? Url);

public sealed class Issue(string id, DateTimeOffset at, string text, string? ideaId)
{
    public string Id => id;
    public DateTimeOffset At => at;
    public string Text => text;
    public string? IdeaId => ideaId;
    public string? RootCause { get; set; }
    public string? PrincipleId { get; set; }
}

public sealed record Principle(string Id, DateTimeOffset At, string Text, string? IssueId);

public sealed class Metric(string name)
{
    public string Name => name;
    public List<(DateTimeOffset At, double Value)> Points { get; } = [];
    public (double Lo, double Hi)? Spec { get; set; }
}

/// Everything vinci knows right now, rebuilt from the journal on every run.
public sealed class State
{
    /// The metric vinci keeps about itself: findings per review.
    public const string FindingsMetric = "vinci.findings";

    public List<Idea> Ideas { get; } = [];
    public List<Requirement> Requirements { get; } = [];
    public List<Note> Notes { get; } = [];
    public List<Source> Sources { get; } = [];
    public List<Issue> Issues { get; } = [];
    public List<Principle> Principles { get; } = [];
    public List<Metric> Metrics { get; } = [];
    public (string IdeaId, DateTimeOffset Since)? Running { get; private set; }

    public static State From(IEnumerable<Event> events, DateTimeOffset now)
    {
        var state = new State();
        foreach (var e in events) state.Apply(e);
        // Count the running timer so far, so totals are current while working.
        if (state.Running is var (ideaId, since)) state.Idea(ideaId)!.Touch += now - since;
        return state;
    }

    public Idea? Idea(string id) => Ideas.Find(i => i.Id == id);
    public Requirement? Requirement(string id) => Requirements.Find(r => r.Id == id);
    public Note? Note(string id) => Notes.Find(n => n.Id == id);
    public Source? Source(string id) => Sources.Find(s => s.Id == id);
    public Issue? Issue(string id) => Issues.Find(x => x.Id == id);
    public Metric? Metric(string name) => Metrics.Find(m => m.Name == name);

    public bool Has(string id) =>
        Idea(id) is not null || Note(id) is not null || Source(id) is not null
        || Issue(id) is not null || Requirement(id) is not null || Principles.Exists(p => p.Id == id);

    public string NextId(char prefix)
    {
        var count = prefix switch
        {
            'i' => Ideas.Count,
            'r' => Requirements.Count,
            'n' => Notes.Count,
            's' => Sources.Count,
            'x' => Issues.Count,
            'p' => Principles.Count,
            _ => throw new ArgumentOutOfRangeException(nameof(prefix)),
        };
        return $"{prefix}{count + 1}";
    }

    public IEnumerable<Note> NotesAbout(string id) => Notes.Where(n => n.Refs.Contains(id));

    private Metric MetricOrNew(string name)
    {
        var metric = Metric(name);
        if (metric is null) Metrics.Add(metric = new Metric(name));
        return metric;
    }

    private void Apply(Event e)
    {
        var refs = e.Refs ?? [];
        switch (e.Kind)
        {
            case "idea":
                Ideas.Add(new Idea(e.Id!, e.Text!, e.At));
                break;
            case "goal":
                Idea(e.Id!)!.Goal = e.Text;
                break;
            case "req":
                Requirements.Add(new Requirement(e.Id!, refs[0], e.Text!, e.Who!, e.Why!));
                break;
            case "cut":
                Requirement(e.Id!)!.CutWhy = e.Text;
                break;
            case "stage":
                var staged = Idea(e.Id!)!;
                staged.Stage = Enum.Parse<Stage>(e.Text!);
                if (staged.Stage == Stage.Incubate) staged.IncubateUntil = e.At.AddDays(e.Value ?? 1);
                break;
            case "start":
                StopTimer(e.At, null);
                Running = (e.Id!, e.At);
                break;
            case "stop":
                StopTimer(e.At, e.Value);
                break;
            case "spent":
                Idea(e.Id!)!.Touch += TimeSpan.FromMinutes(e.Value!.Value);
                break;
            case "ship":
                var shipped = Idea(e.Id!)!;
                shipped.Ships.Add((e.At, e.Text!));
                shipped.Stage = Stage.Tend;
                break;
            case "retire":
                var retired = Idea(e.Id!)!;
                retired.Stage = Stage.Retired;
                retired.RetiredWhy = e.Text;
                break;
            case "source":
                Sources.Add(new Source(e.Id!, e.At, e.Text!, e.Url));
                break;
            case "note":
                Notes.Add(new Note(e.Id!, e.At, e.Text!, [.. refs]));
                break;
            case "link":
                Note(e.Id!)!.Refs.AddRange(refs);
                break;
            case "log":
                MetricOrNew(e.Id!).Points.Add((e.At, e.Value!.Value));
                break;
            case "spec":
                MetricOrNew(e.Id!).Spec = (e.Value!.Value, e.Hi!.Value);
                break;
            case "issue":
                Issues.Add(new Issue(e.Id!, e.At, e.Text!, refs.FirstOrDefault()));
                break;
            case "diagnose":
                Issue(e.Id!)!.RootCause = e.Text;
                break;
            case "principle":
                var issueId = refs.FirstOrDefault();
                Principles.Add(new Principle(e.Id!, e.At, e.Text!, issueId));
                if (issueId is not null) Issue(issueId)!.PrincipleId = e.Id;
                break;
            case "review":
                MetricOrNew(FindingsMetric).Points.Add((e.At, e.Value!.Value));
                break;
            // "tend" changes nothing but when the idea was last touched.
            // Unknown kinds are skipped so an older build can read a newer journal.
        }

        // Metric names are free text, so they must not be read as ids.
        if (e.Kind is "log" or "spec") return;
        Touched(e.Id, e.At);
        foreach (var r in refs) Touched(r, e.At);
    }

    /// A stop with minutes replaces the measured time: the fix for a timer left running.
    private void StopTimer(DateTimeOffset at, double? minutes)
    {
        if (Running is not var (ideaId, since)) return;
        Idea(ideaId)!.Touch += minutes is { } m ? TimeSpan.FromMinutes(m) : at - since;
        Running = null;
    }

    private void Touched(string? id, DateTimeOffset at)
    {
        if (id is null) return;
        var idea = Idea(id) ?? (Requirement(id) is { } r ? Idea(r.IdeaId) : null);
        if (idea is not null) idea.LastTouched = at;
    }
}
