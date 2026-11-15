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
    public Stage Stage { get; private set; } = Stage.Seed;
    /// Who will use it. The person whose experience decides whether it is good.
    public string? For { get; set; }
    public string? Goal { get; set; }
    /// The goal is met when the latest measurement of this metric is in range.
    public string? GoalMetric { get; set; }
    public double? GoalMin { get; set; }
    public double? GoalMax { get; set; }
    public DateTimeOffset LastTouched { get; set; } = born;
    public DateTimeOffset? IncubateUntil { get; set; }
    /// Time actually spent working on the idea.
    public TimeSpan Touch { get; private set; }
    public Dictionary<Stage, TimeSpan> TouchIn { get; } = [];
    public List<(Stage Stage, DateTimeOffset At)> StageLog { get; } = [(Stage.Seed, born)];
    public List<(DateTimeOffset At, string What)> Ships { get; } = [];
    public List<DateTimeOffset> Uses { get; } = [];
    /// The folder the idea lives in. Its window titles and its commits count as work.
    public string? Home { get; set; }
    /// Commits under Home: proof that work happened, read from git.
    public List<(DateTimeOffset At, string What)> Work { get; } = [];
    public string? RetiredWhy { get; set; }

    public string GoalTarget =>
        GoalMin is { } lo && GoalMax is { } hi ? $"{Text.Num(lo)}..{Text.Num(hi)}"
        : GoalMin is { } min ? $"≥ {Text.Num(min)}"
        : $"≤ {Text.Num(GoalMax!.Value)}";

    public void Enter(Stage stage, DateTimeOffset at)
    {
        if (stage == Stage) return;
        Stage = stage;
        StageLog.Add((stage, at));
    }

    /// Work is counted against the stage the idea is in when the work is recorded.
    public void AddTouch(TimeSpan t)
    {
        Touch += t;
        TouchIn[Stage] = TouchIn.GetValueOrDefault(Stage) + t;
    }

    public bool Entered(Stage stage) => StageLog.Exists(l => l.Stage == stage);

    /// How long the idea has sat in a stage, working or waiting.
    public TimeSpan TimeIn(Stage stage, DateTimeOffset now)
    {
        var total = TimeSpan.Zero;
        for (var i = 0; i < StageLog.Count; i++)
        {
            if (StageLog[i].Stage != stage) continue;
            total += (i + 1 < StageLog.Count ? StageLog[i + 1].At : now) - StageLog[i].At;
        }
        return total;
    }
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

/// Where is the place inside the source: a page, a chapter, a timestamp.
public sealed record Note(string Id, DateTimeOffset At, string Text, List<string> Refs, List<string> Tags, string? Where);

public sealed record Source(string Id, DateTimeOffset At, string Citation, string? Url);

public sealed class Issue(string id, DateTimeOffset at, string text, string? ideaId, Stage? stage, bool big, string? brokeId)
{
    public string Id => id;
    public DateTimeOffset At => at;
    public string Text => text;
    public string? IdeaId => ideaId;
    /// The stage the idea was in when this went wrong.
    public Stage? Stage => stage;
    public bool Big => big;
    /// A principle that was supposed to prevent this and did not.
    public string? BrokeId => brokeId;
    public string? RootCause { get; set; }
    public string? PrincipleId { get; set; }
}

/// Stage is where the issue behind it happened, so the principle can be
/// shown again when another idea reaches that stage.
public sealed record Principle(string Id, DateTimeOffset At, string Text, string? IssueId, Stage? Stage);

/// A claim about what will happen, with a confidence, written down before
/// the test. Right stays null until the result is in.
public sealed class Prediction(string id, DateTimeOffset at, string text, double confidence, string? ideaId)
{
    public string Id => id;
    public DateTimeOffset At => at;
    public string Text => text;
    public double Confidence => confidence;
    public string? IdeaId => ideaId;
    public bool? Right { get; set; }
    public string? Outcome { get; set; }
}

/// What you said you would do this week. Kept stays null until you answer for it.
public sealed class Commitment(DateTimeOffset at, string text, string? ideaId)
{
    public DateTimeOffset At => at;
    public string Text => text;
    public string? IdeaId => ideaId;
    public bool? Kept { get; set; }
}

/// A link worth keeping. Why is what it is for.
public sealed record Bookmark(string Id, DateTimeOffset At, string Title, string Url, string? Why, List<string> Tags);

/// Time in a window whose app or title contains Pattern is work on the idea,
/// or, when Use is set, is the idea being used: the shipped thing on screen.
public sealed record Watch(string IdeaId, string Pattern, bool Use = false);

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
    public List<Prediction> Predictions { get; } = [];
    public List<Commitment> Commitments { get; } = [];
    public List<Bookmark> Bookmarks { get; } = [];
    /// Bookmarks ever made, kept or not, so an id is never used twice.
    private int bookmarksMade;
    public List<Watch> Watches { get; } = [];
    /// When the first watch was set: where window tracking begins.
    public DateTimeOffset? WatchingSince { get; private set; }
    /// Window activity up to here has already been credited.
    public DateTimeOffset? SyncedTo { get; private set; }

    public static State From(IEnumerable<Event> events)
    {
        var state = new State();
        foreach (var e in events) state.Apply(e);
        return state;
    }

    /// Commits already recorded as work on an idea, so none is counted twice.
    /// One commit can be work on several ideas.
    public HashSet<(string IdeaId, string Hash)> Commits { get; } = [];

    /// True when work on the idea is counted without anyone recording it.
    public bool Watched(Idea idea) => idea.Home is not null || Watches.Exists(w => w.IdeaId == idea.Id && !w.Use);

    /// What a home folder watches for: its name as an editor shows it,
    /// "App.cs - vinci - Visual Studio Code".
    public static string HomePattern(string home) => $"- {Path.GetFileName(home)} -";

    /// Every watch in force: the ones recorded, and one implied by each home.
    public IEnumerable<Watch> AllWatches =>
        Watches.Concat(Ideas.Where(i => i.Home is not null).Select(i => new Watch(i.Id, HomePattern(i.Home!))))
            .Where(w => Idea(w.IdeaId)!.Stage != Stage.Retired);

    public Idea? Idea(string id) => Ideas.Find(i => i.Id == id);
    public Requirement? Requirement(string id) => Requirements.Find(r => r.Id == id);
    public Note? Note(string id) => Notes.Find(n => n.Id == id);
    public Source? Source(string id) => Sources.Find(s => s.Id == id);
    public Issue? Issue(string id) => Issues.Find(x => x.Id == id);
    public Metric? Metric(string name) => Metrics.Find(m => m.Name == name);
    public Prediction? Prediction(string id) => Predictions.Find(h => h.Id == id);
    public Principle? Principle(string id) => Principles.Find(p => p.Id == id);
    public Commitment? OpenCommitment => Commitments.Find(c => c.Kept is null);

    /// Null when the goal's metric has never been measured.
    public bool? GoalMet(Idea idea)
    {
        if (idea.GoalMetric is null || Metric(idea.GoalMetric) is not { Points.Count: > 0 } metric) return null;
        var latest = metric.Points[^1].Value;
        return (idea.GoalMin is not { } min || latest >= min) && (idea.GoalMax is not { } max || latest <= max);
    }

    public int Breaks(Principle p) => Issues.Count(x => x.BrokeId == p.Id);

    public bool Has(string id) =>
        Idea(id) is not null || Note(id) is not null || Source(id) is not null
        || Issue(id) is not null || Requirement(id) is not null || Principle(id) is not null
        || Prediction(id) is not null;

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
            'h' => Predictions.Count,
            'b' => bookmarksMade,
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
            case "for":
                Idea(e.Id!)!.For = e.Text;
                break;
            case "goal":
                var aimed = Idea(e.Id!)!;
                (aimed.Goal, aimed.GoalMetric, aimed.GoalMin, aimed.GoalMax) = (e.Text, e.Metric, e.Value, e.Hi);
                break;
            case "req":
                Requirements.Add(new Requirement(e.Id!, refs[0], e.Text!, e.Who!, e.Why!));
                break;
            case "cut":
                Requirement(e.Id!)!.CutWhy = e.Text;
                break;
            case "stage":
                var staged = Idea(e.Id!)!;
                staged.Enter(Enum.Parse<Stage>(e.Text!), e.At);
                if (staged.Stage == Stage.Incubate) staged.IncubateUntil = e.At.AddDays(e.Value ?? 1);
                break;
            case "watch":
                WatchingSince ??= e.At;
                Watches.Add(new Watch(e.Id!, e.Text!, Use: e.Value == 1));
                break;
            case "unwatch":
                Watches.RemoveAll(w => w.IdeaId == e.Id && w.Pattern.Equals(e.Text, StringComparison.OrdinalIgnoreCase));
                break;
            case "bookmark":
                bookmarksMade++;
                Bookmarks.Add(new Bookmark(e.Id!, e.At, e.Text!, e.Url!, e.Why, [.. e.Tags ?? []]));
                break;
            case "unbookmark":
                Bookmarks.RemoveAll(b => b.Id == e.Id);
                break;
            case "home":
                WatchingSince ??= e.At;
                Idea(e.Id!)!.Home = e.Text;
                break;
            case "work":
                Idea(e.Id!)!.Work.Add((e.At, e.Text!));
                Commits.Add((e.Id!, e.Where!));
                break;
            case "sync":
                SyncedTo = e.At;
                break;
            case "spent":
                Idea(e.Id!)!.AddTouch(TimeSpan.FromMinutes(e.Value!.Value));
                break;
            case "ship":
                var shipped = Idea(e.Id!)!;
                shipped.Ships.Add((e.At, e.Text!));
                shipped.Enter(Stage.Tend, e.At);
                break;
            case "used":
                Idea(e.Id!)!.Uses.Add(e.At);
                break;
            case "retire":
                var retired = Idea(e.Id!)!;
                retired.Enter(Stage.Retired, e.At);
                retired.RetiredWhy = e.Text;
                break;
            case "source":
                Sources.Add(new Source(e.Id!, e.At, e.Text!, e.Url));
                break;
            case "note":
                Notes.Add(new Note(e.Id!, e.At, e.Text!, [.. refs], [.. e.Tags ?? []], e.Where));
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
                var about = refs.FirstOrDefault(r => r[0] == 'i');
                Issues.Add(new Issue(e.Id!, e.At, e.Text!, about, about is null ? null : Idea(about)!.Stage,
                    big: e.Value == 1, brokeId: refs.FirstOrDefault(r => r[0] == 'p')));
                break;
            case "diagnose":
                Issue(e.Id!)!.RootCause = e.Text;
                break;
            case "principle":
                var issue = refs.FirstOrDefault() is { } issueId ? Issue(issueId) : null;
                Principles.Add(new Principle(e.Id!, e.At, e.Text!, issue?.Id, issue?.Stage));
                if (issue is not null) issue.PrincipleId = e.Id;
                break;
            case "predict":
                Predictions.Add(new Prediction(e.Id!, e.At, e.Text!, e.Value!.Value, refs.FirstOrDefault()));
                break;
            case "result":
                var settled = Prediction(e.Id!)!;
                (settled.Right, settled.Outcome) = (e.Value == 1, e.Text);
                break;
            case "commit":
                Commitments.Add(new Commitment(e.At, e.Text!, refs.FirstOrDefault()));
                break;
            case "kept" or "missed":
                OpenCommitment!.Kept = e.Kind == "kept";
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

    private void Touched(string? id, DateTimeOffset at)
    {
        if (id is null) return;
        var idea = Idea(id) ?? (Requirement(id) is { } r ? Idea(r.IdeaId) : null);
        if (idea is not null) idea.LastTouched = at;
    }
}
