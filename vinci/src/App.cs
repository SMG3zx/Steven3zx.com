using System.Globalization;

namespace Vinci;

/// A mistake in what was typed, as opposed to a bug. Shown without a stack trace.
public sealed class UserError(string message) : Exception(message);

public static class Text
{
    public static string Span(TimeSpan t) =>
        t.TotalDays >= 1 ? $"{(int)t.TotalDays}d{t.Hours}h"
        : t.TotalHours >= 1 ? $"{(int)t.TotalHours}h{t.Minutes}m"
        : $"{(int)t.TotalMinutes}m";

    public static string Clip(string s, int max) => s.Length <= max ? s : s[..(max - 1)] + "…";

    public static string Day(DateTimeOffset at) => at.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);

    public static string Num(double v) => v.ToString("0.##", CultureInfo.InvariantCulture);
}

public static class App
{
    /// Window time is credited before the command runs, so every command
    /// sees current totals and nobody has to start or stop a timer.
    public static int Run(string[] args, Journal journal, TextReader input, TextWriter output, TextWriter errors, DateTimeOffset now,
        ActivitySource? activity = null, CommitSource? commits = null)
    {
        try
        {
            var tracked = Tracker.Sync(journal, activity, commits, now);
            new Session(journal, input, output, now, tracked).Dispatch(args);
            return 0;
        }
        catch (UserError e)
        {
            errors.WriteLine($"vinci: {e.Message}");
            return 1;
        }
    }
}

/// One command: read the journal, check the request, append at most a few facts.
public sealed class Session(Journal journal, TextReader input, TextWriter o, DateTimeOffset now, IReadOnlyList<string> tracked)
{
    private readonly State s = State.From(journal.Read());

    public void Dispatch(string[] args)
    {
        if (args.Length == 0)
        {
            if (s.Ideas.Count == 0) o.Write(Help); else Pipeline();
            return;
        }
        var a = args[1..];
        switch (args[0])
        {
            case "idea": NewIdea(Arg(a, 0, "title")); break;
            case "for": For(IdeaArg(a), Arg(a, 1, "who will use it")); break;
            case "goal": Goal(IdeaArg(a), a); break;
            case "req": Req(IdeaArg(a), Arg(a, 1, "requirement"), Arg(a, 2, "who asked, by name"), Arg(a, 3, "why")); break;
            case "cut": Cut(Arg(a, 0, "requirement id"), Arg(a, 1, "why")); break;
            case "advance": Advance(IdeaArg(a), a.Length > 1 ? Number(a[1], "days") : 1); break;
            case "adopt": Adopt(Arg(a, 0, "folder")); break;
            case "home": Home(IdeaArg(a), Arg(a, 1, "folder")); break;
            case "watch": Watch(IdeaArg(a), Arg(a, 1, "text in its window titles"), a.ElementAtOrDefault(2)); break;
            case "unwatch": Unwatch(IdeaArg(a), Arg(a, 1, "watched text")); break;
            case "watches": Watches(); break;
            case "sync": foreach (var line in tracked) o.WriteLine(line); break;
            case "spent": Spent(IdeaArg(a), Number(Arg(a, 1, "minutes"), "minutes")); break;
            case "ship": Ship(IdeaArg(a), Arg(a, 1, "what shipped")); break;
            case "used": Used(IdeaArg(a), a.Length > 1 ? Arg(a, 1, "how it went") : null); break;
            case "tend": Tend(IdeaArg(a), Arg(a, 1, "what you did")); break;
            case "retire": Retire(IdeaArg(a), Arg(a, 1, "why")); break;
            case "ideas": Ideas(); break;
            case "pipeline": Pipeline(); break;
            case "next": Next(); break;
            case "source": NewSource(Arg(a, 0, "citation"), a.ElementAtOrDefault(1)); break;
            case "note": NewNote(Arg(a, 0, "text"), a[1..]); break;
            case "link": Link(Arg(a, 0, "note id"), Arg(a, 1, "note or idea id")); break;
            case "notes": Notes(a.ElementAtOrDefault(0)); break;
            case "tags": Tags(); break;
            case "sources": Sources(); break;
            case "show": Show(Arg(a, 0, "id")); break;
            case "predict": Predict(Arg(a, 0, "what you expect"), Number(Arg(a, 1, "confidence percent"), "confidence percent"), a.ElementAtOrDefault(2)); break;
            case "result": Result(Arg(a, 0, "prediction id"), Arg(a, 1, "right or wrong"), Arg(a, 2, "what happened")); break;
            case "predictions": Predictions(); break;
            case "log": Log(Arg(a, 0, "metric"), Number(Arg(a, 1, "value"), "value"), a.Length > 2 ? Arg(a, 2, "comment") : null); break;
            case "spec": Spec(Arg(a, 0, "metric"), Number(Arg(a, 1, "low"), "low"), Number(Arg(a, 2, "high"), "high")); break;
            case "stats": if (a.Length == 0) Metrics(); else MetricDetail(a[0]); break;
            case "issue": NewIssue(Arg(a, 0, "what went wrong"), a[1..]); break;
            case "diagnose": Diagnose(Arg(a, 0, "issue id"), Arg(a, 1, "root cause")); break;
            case "principle": NewPrinciple(Arg(a, 0, "principle"), a.ElementAtOrDefault(1)); break;
            case "issues": Issues(); break;
            case "principles": Principles(); break;
            case "commit": Commit(Arg(a, 0, "what you will get done"), a.ElementAtOrDefault(1)); break;
            case "kept": Kept(); break;
            case "missed": Missed(Arg(a, 0, "why")); break;
            case "review": RunReview(); break;
            case "path": o.WriteLine(journal.Path); break;
            case "help" or "--help" or "-h": o.Write(Help); break;
            default: throw new UserError($"unknown command '{args[0]}'. Run 'vinci help'.");
        }
    }

    // ---- ideas: inception to grown ----

    private void NewIdea(string title)
    {
        var id = s.NextId('i');
        Record(new(now, "idea", id, title));
        o.WriteLine($"{id} seeded. Next: vinci for {id} \"who will use it\", then vinci goal {id} ...");
    }

    private void For(Idea idea, string who)
    {
        Record(new(now, "for", idea.Id, who));
        o.WriteLine($"{idea.Id} is for {who}.");
    }

    /// goal <idea> "text" <metric> at-least|at-most <n>, or between <low> <high>
    private void Goal(Idea idea, string[] a)
    {
        const string usage = "goal <idea> \"goal\" <metric> at-least <n> | at-most <n> | between <low> <high>";
        var text = Arg(a, 1, "goal");
        if (a.Length < 5) throw new UserError($"a goal needs a number that says when it is met: {usage}");
        var metric = a[2];
        double? min = null, max = null;
        switch (a[3])
        {
            case "at-least": min = Number(a[4], "target"); break;
            case "at-most": max = Number(a[4], "target"); break;
            case "between":
                min = Number(a[4], "low");
                max = Number(Arg(a, 5, "high"), "high");
                if (min > max) throw new UserError("low must not be above high");
                break;
            default: throw new UserError($"'{a[3]}' is not at-least, at-most or between: {usage}");
        }
        Record(new(now, "goal", idea.Id, text, Value: min, Hi: max, Metric: metric));
        o.WriteLine($"{idea.Id} goal set. Record progress with: vinci log {metric} <value>");
    }

    private void Req(Idea idea, string text, string who, string why)
    {
        var id = s.NextId('r');
        Record(new(now, "req", id, text, [idea.Id], Who: who, Why: why));
        o.WriteLine($"{id} added to {idea.Id}. Can it be deleted? vinci cut {id} \"why\"");
    }

    private void Cut(string id, string why)
    {
        var req = s.Requirement(id) ?? throw new UserError($"no requirement {id}");
        if (!req.Live) throw new UserError($"{id} is already cut");
        Record(new(now, "cut", id, why));
        var all = s.Requirements.Where(r => r.IdeaId == req.IdeaId).ToList();
        o.WriteLine($"{id} cut. {req.IdeaId} has deleted {all.Count(r => !r.Live) + 1} of {all.Count} requirements.");
    }

    /// The gates. Each one is a step of someone's method that is easy to skip.
    private void Advance(Idea idea, double days)
    {
        switch (idea.Stage)
        {
            case Stage.Seed:
                if (idea.For is null)
                    throw new UserError($"{idea.Id} is for nobody yet. Name who will use it: vinci for {idea.Id} \"...\"");
                if (idea.Goal is null)
                    throw new UserError($"{idea.Id} has no goal. Set one with a number: vinci goal {idea.Id} \"...\" <metric> at-least <n>");
                MoveTo(idea, Stage.Prepare);
                o.WriteLine($"Next: gather notes (vinci note \"...\" {idea.Id}), question every requirement (vinci req), and say what you expect (vinci predict).");
                break;

            case Stage.Prepare:
                if (!s.NotesAbout(idea.Id).Any())
                    throw new UserError($"{idea.Id} has no notes. Preparation is gathering what is known: vinci note \"...\" <source> {idea.Id}");
                if (!s.Requirements.Exists(r => r.IdeaId == idea.Id && r.Live))
                    throw new UserError($"{idea.Id} has no requirements. State what it must do, who says so, and why: vinci req {idea.Id} \"...\" \"name\" \"why\"");
                if (!s.Predictions.Exists(h => h.IdeaId == idea.Id))
                    throw new UserError($"{idea.Id} has no prediction. Write down what you expect before you test it: vinci predict \"...\" <percent> {idea.Id}");
                if (days < 0) throw new UserError("days cannot be negative");
                Record(new(now, "stage", idea.Id, nameof(Stage.Incubate), Value: days));
                o.WriteLine($"{idea.Id} is incubating until {Text.Day(now.AddDays(days))}. Work on something else.");
                break;

            case Stage.Incubate:
                if (now < idea.IncubateUntil)
                    throw new UserError($"{idea.Id} is incubating until {Text.Day(idea.IncubateUntil.Value)}. Leave it alone.");
                var building = s.Ideas.Where(i => i.Stage == Stage.Build).Select(i => i.Id).ToList();
                if (building.Count >= Review.BuildLimit)
                    throw new UserError($"{Review.BuildLimit} ideas are already in Build ({string.Join(", ", building)}). Ship or retire one first.");
                MoveTo(idea, Stage.Build);
                o.WriteLine(s.Watched(idea)
                    ? "Next: ship the smallest thing that works."
                    : $"Next: vinci home {idea.Id} <folder>, then ship the smallest thing that works.");
                break;

            case Stage.Build:
                throw new UserError($"Build ends by shipping: vinci ship {idea.Id} \"what shipped\"");
            case Stage.Tend:
                throw new UserError($"{idea.Id} is grown. It is tended, not finished: vinci ship or vinci tend");
            default:
                throw new UserError($"{idea.Id} is retired: {idea.RetiredWhy}");
        }
    }

    private void MoveTo(Idea idea, Stage stage)
    {
        Record(new(now, "stage", idea.Id, stage.ToString()));
        o.WriteLine($"{idea.Id} is now in {stage}.");
        Remind(stage);
    }

    /// A principle is shown again at the stage where the issue behind it happened.
    private void Remind(Stage stage)
    {
        foreach (var p in s.Principles.Where(p => p.Stage == stage))
            o.WriteLine($"Remember {p.Id}: {p.Text}");
    }

    private void Ship(Idea idea, string what)
    {
        if (idea.Stage is not (Stage.Build or Stage.Tend))
            throw new UserError($"{idea.Id} is in {idea.Stage}. It ships from Build or Tend: vinci advance {idea.Id}");
        var first = idea.Ships.Count == 0;
        var cycle = now - (first ? idea.Born : idea.Ships[^1].At);
        Record(new(now, "ship", idea.Id, what));
        o.WriteLine($"{idea.Id} shipped (#{idea.Ships.Count + 1}). Cycle time {Text.Span(cycle)} since {(first ? "inception" : "the last ship")}; touch time {Text.Span(idea.Touch)} in total.");
        o.WriteLine($"Goal: {GoalStatus(idea)}.");
        var open = s.Predictions.Where(h => h.IdeaId == idea.Id && h.Right is null).Select(h => h.Id).ToList();
        if (open.Count > 0) o.WriteLine($"Predictions to settle: {string.Join(", ", open)}");
        if (first)
        {
            o.WriteLine($"{idea.Id} is now in Tend. Use it yourself: vinci used {idea.Id}");
            Remind(Stage.Tend);
        }
    }

    private void Used(Idea idea, string? how)
    {
        if (idea.Ships.Count == 0) throw new UserError($"{idea.Id} has not shipped, so there is nothing to use");
        if (idea.Stage == Stage.Retired) throw new UserError($"{idea.Id} is retired");
        Record(new(now, "used", idea.Id, how));
        o.WriteLine($"{idea.Id} used {idea.Uses.Count + 1} times. Anything wrong with it? vinci issue \"...\" {idea.Id}");
    }

    private void Tend(Idea idea, string what)
    {
        if (idea.Stage == Stage.Retired) throw new UserError($"{idea.Id} is retired");
        Record(new(now, "tend", idea.Id, what));
        o.WriteLine($"{idea.Id} tended.");
    }

    private void Retire(Idea idea, string why)
    {
        if (idea.Stage == Stage.Retired) throw new UserError($"{idea.Id} is already retired");
        Record(new(now, "retire", idea.Id, why));
        o.WriteLine($"{idea.Id} retired on purpose. Its notes stay.");
    }

    private string GoalStatus(Idea i)
    {
        if (i.Goal is null) return "no goal";
        var latest = s.Metric(i.GoalMetric!)?.Points is { Count: > 0 } points ? Text.Num(points[^1].Value) : null;
        return s.GoalMet(i) switch
        {
            null => $"{i.GoalMetric} not measured yet, needs {i.GoalTarget}",
            true => $"met, {i.GoalMetric} {latest} is {i.GoalTarget}. Raise it?",
            false => $"not met, {i.GoalMetric} {latest} needs {i.GoalTarget}",
        };
    }

    private void Ideas()
    {
        if (s.Ideas.Count == 0) { o.WriteLine("No ideas yet. vinci idea \"title\""); return; }
        foreach (var i in s.Ideas)
        {
            o.WriteLine($"{i.Id,-4} {i.Stage,-8} touch {Text.Span(i.Touch),-7} age {Text.Span(now - i.Born),-7} flow {Flow(i),-6} {i.Title}");
        }
    }

    /// Touch time over elapsed time: how much of the idea's life was work
    /// rather than waiting. The gap is where the improvement is.
    private string Flow(Idea i)
    {
        var age = now - i.Born;
        return age <= TimeSpan.Zero ? "-" : (i.Touch / age).ToString("0.0%", CultureInfo.InvariantCulture);
    }

    /// Every live idea by stage, where ideas get stuck, and the one thing to do now.
    private void Pipeline()
    {
        foreach (var stage in new[] { Stage.Seed, Stage.Prepare, Stage.Incubate, Stage.Build, Stage.Tend })
        {
            var here = s.Ideas.Where(i => i.Stage == stage).ToList();
            var label = stage == Stage.Build ? $"Build {here.Count}/{Review.BuildLimit}" : stage.ToString();
            if (here.Count == 0) o.WriteLine($"{label,-10} -");
            foreach (var i in here)
            {
                var detail = stage switch
                {
                    Stage.Incubate => $"until {Text.Day(i.IncubateUntil!.Value)}",
                    Stage.Build => $"{Text.Span(i.TimeIn(stage, now))} here, touch {Text.Span(i.Touch)}; goal {GoalStatus(i)}",
                    Stage.Tend => $"shipped {i.Ships.Count}, used {i.Uses.Count}; goal {GoalStatus(i)}",
                    _ => $"{Text.Span(i.TimeIn(stage, now))} here",
                };
                o.WriteLine($"{(i == here[0] ? label : ""),-10} {i.Id,-4} {i.Title}  ({detail})");
            }
        }

        if (Review.SlowestStage(s, now) is var (slow, average) && average >= TimeSpan.FromHours(1))
            o.WriteLine($"Slowest stage: {slow}, {Text.Span(average)} on average. Speed that one up first.");
        Next();
    }

    private void Next()
    {
        var (what, how) = Review.Next(s, now);
        o.WriteLine($"Next: {what}");
        o.WriteLine($"      {how}");
    }

    // ---- time ----

    /// An existing folder becomes an idea in one step, named after the folder.
    /// Adopting it again does nothing, so a whole directory can be adopted in a loop.
    private void Adopt(string folder)
    {
        var path = FolderArg(folder);
        if (s.Ideas.Find(i => i.Home == path) is { } owner)
        {
            o.WriteLine($"{owner.Id} already lives in {path}.");
            return;
        }
        var id = s.NextId('i');
        Record(new(now, "idea", id, Path.GetFileName(path)));
        Record(new(now, "home", id, path));
        o.WriteLine($"{id} adopted from {path}. Its windows and commits now count as work.");
    }

    private void Home(Idea idea, string folder)
    {
        if (idea.Stage == Stage.Retired) throw new UserError($"{idea.Id} is retired");
        var path = FolderArg(folder);
        if (s.Ideas.Find(i => i.Home == path) is { } owner) throw new UserError($"{owner.Id} already lives in {path}");
        Record(new(now, "home", idea.Id, path));
        o.WriteLine($"{idea.Id} lives in {path}. Time in windows titled \"{State.HomePattern(path)}\" and commits there now count as work.");
    }

    private static string FolderArg(string folder)
    {
        var path = Path.TrimEndingDirectorySeparator(Path.GetFullPath(folder));
        return Directory.Exists(path) ? path : throw new UserError($"{path} is not a folder");
    }

    private void Watch(Idea idea, string pattern, string? kind)
    {
        if (idea.Stage == Stage.Retired) throw new UserError($"{idea.Id} is retired");
        if (kind is not (null or "use")) throw new UserError($"after the text comes \"use\" or nothing, got '{kind}'");
        if (s.AllWatches.FirstOrDefault(w => w.Pattern.Equals(pattern, StringComparison.OrdinalIgnoreCase)) is { } taken)
            throw new UserError($"\"{pattern}\" is already watched for {taken.IdeaId}");
        Record(new(now, "watch", idea.Id, pattern, Value: kind is null ? null : 1));
        o.WriteLine(kind is null
            ? $"Time in a window whose app or title contains \"{pattern}\" now counts for {idea.Id}."
            : $"A window whose app or title contains \"{pattern}\" now counts as using {idea.Id}.");
    }

    private void Unwatch(Idea idea, string pattern)
    {
        if (!s.Watches.Exists(w => w.IdeaId == idea.Id && w.Pattern.Equals(pattern, StringComparison.OrdinalIgnoreCase)))
            throw new UserError($"{idea.Id} does not watch \"{pattern}\"");
        Record(new(now, "unwatch", idea.Id, pattern));
        o.WriteLine($"{idea.Id} no longer watches \"{pattern}\".");
    }

    private void Watches()
    {
        var watches = s.AllWatches.ToList();
        if (watches.Count == 0) { o.WriteLine(Tracker.NothingWatched); return; }
        foreach (var w in watches) o.WriteLine($"{w.IdeaId,-4} {(w.Use ? "use " : "work")} \"{w.Pattern}\"  {s.Idea(w.IdeaId)!.Title}");
    }

    private void Spent(Idea idea, double minutes)
    {
        if (minutes <= 0) throw new UserError("minutes must be positive");
        Record(new(now, "spent", idea.Id, Value: minutes));
        o.WriteLine($"{Text.Span(TimeSpan.FromMinutes(minutes))} added to {idea.Id}.");
    }

    // ---- knowledge and where it came from ----

    private void NewSource(string citation, string? url)
    {
        var id = s.NextId('s');
        Record(new(now, "source", id, citation, Url: url));
        o.WriteLine($"{id} recorded. Cite it with the place: vinci note \"one idea, in your own words\" {id}:p.12");
    }

    /// After the text come, in any order: ids to link to, a source with an
    /// optional place (s1:p.42), and tags (+flight).
    private void NewNote(string text, string[] rest)
    {
        var refs = new List<string>();
        var tags = new List<string>();
        string? where = null;
        foreach (var token in rest)
        {
            if (token.StartsWith('+') && token.Length > 1) { tags.Add(token[1..].ToLowerInvariant()); continue; }
            var (id, place) = token.Split(':', 2) is [var head, var tail] ? (head, tail) : (token, null);
            if (!Linkable(id)) throw new UserError($"'{token}' is not a note, idea, source or prediction that exists, nor a +tag");
            if (place is not null)
            {
                if (id[0] != 's') throw new UserError($"only a source has a place inside it, and {id} is not a source");
                where = place;
            }
            refs.Add(id);
        }
        var noteId = s.NextId('n');
        Record(new(now, "note", noteId, text, refs.Count > 0 ? [.. refs] : null, Tags: tags.Count > 0 ? [.. tags] : null, Where: where));
        var origin = refs.Exists(r => r[0] == 's') ? "" : " No source given, so it is recorded as your own thought.";
        o.WriteLine($"{noteId} written.{origin}");
    }

    private bool Linkable(string id) => id.Length > 0 && id[0] is 'n' or 'i' or 's' or 'h' && s.Has(id);

    private void Link(string from, string to)
    {
        var note = s.Note(from) ?? throw new UserError($"no note {from}");
        if (!Linkable(to)) throw new UserError($"'{to}' is not a note, idea, source or prediction that exists");
        if (from == to) throw new UserError("a note cannot link to itself");
        if (note.Refs.Contains(to)) throw new UserError($"{from} already links to {to}");
        Record(new(now, "link", from, Refs: [to]));
        o.WriteLine($"{from} → {to}");
    }

    private void Notes(string? query)
    {
        var found = s.Notes.Where(n => query is null
            || (query.StartsWith('+')
                ? n.Tags.Contains(query[1..].ToLowerInvariant())
                : n.Text.Contains(query, StringComparison.OrdinalIgnoreCase))).ToList();
        if (found.Count == 0) { o.WriteLine("No notes."); return; }
        foreach (var n in found) o.WriteLine(NoteLine(n));
    }

    /// The index: every subject and how many notes sit under it.
    private void Tags()
    {
        var index = s.Notes.SelectMany(n => n.Tags).GroupBy(t => t).OrderByDescending(g => g.Count()).ThenBy(g => g.Key).ToList();
        if (index.Count == 0) { o.WriteLine("No tags yet. Add one to a note: vinci note \"...\" +subject"); return; }
        foreach (var tag in index) o.WriteLine($"+{tag.Key,-20} {tag.Count()}");
    }

    private void Sources()
    {
        if (s.Sources.Count == 0) { o.WriteLine("No sources yet. vinci source \"citation\" [url]"); return; }
        foreach (var src in s.Sources)
            o.WriteLine($"{src.Id,-4} {src.Citation}{(src.Url is null ? "" : $" <{src.Url}>")}  ({s.NotesAbout(src.Id).Count()} notes)");
    }

    private static string NoteLine(Note n)
    {
        var refs = n.Refs.Select(r => r[0] == 's' && n.Where is not null ? $"{r}:{n.Where}" : r).Concat(n.Tags.Select(t => $"+{t}"));
        return $"{n.Id,-4} {n.Text}{(n.Refs.Count + n.Tags.Count > 0 ? $"  [{string.Join(' ', refs)}]" : "")}";
    }

    private void Show(string id)
    {
        if (s.Idea(id) is { } idea) ShowIdea(idea);
        else if (s.Note(id) is { } note) ShowNote(note);
        else if (s.Source(id) is { } source)
        {
            o.WriteLine($"{source.Id}  {source.Citation}");
            if (source.Url is not null) o.WriteLine($"url       {source.Url}");
            o.WriteLine($"recorded  {Text.Day(source.At)}");
            Section("notes drawn from it", s.NotesAbout(id).Select(NoteLine));
        }
        else if (s.Issue(id) is { } issue)
        {
            o.WriteLine($"{issue.Id}  {issue.Text}{(issue.Big ? "  (big)" : "")}");
            o.WriteLine($"raised      {Text.Day(issue.At)}{(issue.IdeaId is null ? "" : $" on {issue.IdeaId} in {issue.Stage}")}");
            if (issue.BrokeId is not null) o.WriteLine($"broke       {issue.BrokeId} {s.Principle(issue.BrokeId)!.Text}");
            o.WriteLine($"root cause  {issue.RootCause ?? "(not diagnosed)"}");
            var principle = issue.PrincipleId is null ? null : s.Principle(issue.PrincipleId);
            o.WriteLine($"principle   {(principle is null ? "(none yet)" : $"{principle.Id} {principle.Text}")}");
        }
        else if (s.Prediction(id) is { } h)
        {
            o.WriteLine(PredictionLine(h));
            o.WriteLine($"made     {Text.Day(h.At)}{(h.IdeaId is null ? "" : $" about {h.IdeaId}")}");
            if (h.Outcome is not null) o.WriteLine($"outcome  {h.Outcome}");
            Section("notes about it", s.NotesAbout(id).Select(NoteLine));
        }
        else throw new UserError($"nothing has the id '{id}'");
    }

    private void ShowIdea(Idea i)
    {
        o.WriteLine($"{i.Id}  {i.Title}");
        o.WriteLine($"stage   {i.Stage}{(i.Stage == Stage.Incubate ? $" until {Text.Day(i.IncubateUntil!.Value)}" : "")}{(i.RetiredWhy is null ? "" : $": {i.RetiredWhy}")}");
        o.WriteLine($"for     {i.For ?? "(nobody yet)"}");
        o.WriteLine($"goal    {i.Goal ?? "(none)"}{(i.Goal is null ? "" : $"  [{GoalStatus(i)}]")}");
        o.WriteLine($"time    born {Text.Day(i.Born)}, age {Text.Span(now - i.Born)}, touch {Text.Span(i.Touch)}, flow {Flow(i)}");
        if (i.Home is not null) o.WriteLine($"home    {i.Home}{(i.Work.Count > 0 ? $"  ({i.Work.Count} commit{(i.Work.Count == 1 ? "" : "s")}, last {Text.Day(i.Work[^1].At)})" : "")}");
        var watches = s.AllWatches.Where(w => w.IdeaId == i.Id).Select(w => $"\"{w.Pattern}\"{(w.Use ? " (use)" : "")}").ToList();
        if (watches.Count > 0) o.WriteLine($"watch   {string.Join(", ", watches)}");
        var stages = Enum.GetValues<Stage>().Where(st => st != Stage.Retired && i.Entered(st)).Select(st =>
            $"{st} {Text.Span(i.TimeIn(st, now))}{(i.TouchIn.TryGetValue(st, out var t) ? $" (touch {Text.Span(t)})" : "")}");
        o.WriteLine($"stages  {string.Join(", ", stages)}");
        if (i.Ships.Count > 0) o.WriteLine($"used    {i.Uses.Count} times{(i.Uses.Count > 0 ? $", last {Text.Day(i.Uses[^1])}" : "")}");
        Section("requirements", s.Requirements.Where(r => r.IdeaId == i.Id).Select(r =>
            r.Live ? $"{r.Id,-4} {r.Text}  ({r.Who}: {r.Why})" : $"{r.Id,-4} CUT {r.Text}  ({r.CutWhy})"));
        Section("predictions", s.Predictions.Where(h => h.IdeaId == i.Id).Select(PredictionLine));
        Section("notes", s.NotesAbout(i.Id).Select(NoteLine));
        Section("credits", s.NotesAbout(i.Id).SelectMany(n => n.Refs).Where(r => r[0] == 's').Distinct()
            .Select(r => s.Source(r)!).Select(src => $"{src.Id,-4} {src.Citation}"));
        Section("ships", i.Ships.Select(ship => $"{Text.Day(ship.At)}  {ship.What}"));
        Section("issues", s.Issues.Where(x => x.IdeaId == i.Id).Select(IssueLine));
    }

    private void ShowNote(Note n)
    {
        o.WriteLine($"{n.Id}  {n.Text}");
        o.WriteLine($"written  {Text.Day(n.At)}");
        if (n.Tags.Count > 0) o.WriteLine($"tags     {string.Join(' ', n.Tags.Select(t => $"+{t}"))}");
        var sources = n.Refs.Where(r => r[0] == 's').Select(r => s.Source(r)!).ToList();
        if (sources.Count == 0) o.WriteLine("origin   your own thought");
        foreach (var src in sources)
            o.WriteLine($"origin   {src.Id} {src.Citation}{(n.Where is null ? "" : $", {n.Where}")}{(src.Url is null ? "" : $" <{src.Url}>")}");
        Section("links to", n.Refs.Where(r => r[0] != 's').Select(Describe));
        Section("linked from", s.NotesAbout(n.Id).Select(NoteLine));
    }

    private string Describe(string id) =>
        s.Note(id) is { } note ? NoteLine(note)
        : s.Prediction(id) is { } h ? PredictionLine(h)
        : $"{id,-4} {s.Idea(id)!.Title}";

    private void Section(string title, IEnumerable<string> lines)
    {
        var list = lines.ToList();
        if (list.Count == 0) return;
        o.WriteLine(title);
        foreach (var line in list) o.WriteLine($"  {line}");
    }

    // ---- inquiry: say what you expect, then find out ----

    private void Predict(string text, double percent, string? ideaId)
    {
        if (percent is < 1 or > 99) throw new UserError("confidence is a percent from 1 to 99");
        if (ideaId is not null && s.Idea(ideaId) is null) throw new UserError($"no idea {ideaId}");
        var id = s.NextId('h');
        Record(new(now, "predict", id, text, ideaId is null ? null : [ideaId], Value: percent));
        o.WriteLine($"{id} predicted at {Text.Num(percent)}%. Test it, then: vinci result {id} right|wrong \"what happened\"");
    }

    private void Result(string id, string verdict, string outcome)
    {
        var h = s.Prediction(id) ?? throw new UserError($"no prediction {id}");
        if (h.Right is not null) throw new UserError($"{id} is already settled");
        if (verdict is not ("right" or "wrong")) throw new UserError($"say right or wrong, not '{verdict}'");
        Record(new(now, "result", id, outcome, Value: verdict == "right" ? 1 : 0));
        o.WriteLine($"{id} was {verdict}. Keep what you learned: vinci note \"...\" {id}");
    }

    private static string PredictionLine(Prediction h) =>
        $"{h.Id,-4} {(h.Right switch { true => "right", false => "wrong", null => "open " })} {Text.Num(h.Confidence),3}%  {h.Text}";

    /// Your record at knowing what will happen: how often you were right
    /// against how sure you said you were.
    private void Predictions()
    {
        if (s.Predictions.Count == 0) { o.WriteLine("No predictions yet. vinci predict \"what you expect\" <percent> [idea]"); return; }
        foreach (var h in s.Predictions) o.WriteLine(PredictionLine(h));

        var settled = s.Predictions.Where(h => h.Right is not null).ToList();
        if (settled.Count == 0) return;
        var right = 100.0 * settled.Count(h => h.Right == true) / settled.Count;
        var sure = settled.Average(h => h.Confidence);
        const int enough = 10;
        var verdict = settled.Count < enough ? $"Too few to judge ({settled.Count} of {enough})."
            : Math.Abs(sure - right) < 10 ? "Your confidence matches your record."
            : sure > right ? $"You are overconfident by {Text.Num(sure - right)} points."
            : $"You are underconfident by {Text.Num(right - sure)} points.";
        o.WriteLine($"Settled {settled.Count}: right {Text.Num(right)}%, average confidence {Text.Num(sure)}%. {verdict}");
    }

    // ---- measuring anything ----

    private void Log(string metric, double value, string? comment)
    {
        if (metric == State.FindingsMetric) throw new UserError($"{metric} is kept by vinci itself");
        Record(new(now, "log", metric, comment, Value: value));
        var n = (s.Metric(metric)?.Points.Count ?? 0) + 1;
        o.WriteLine($"{metric} = {Text.Num(value)} (measurement {n}).");
        foreach (var idea in s.Ideas.Where(i => i.GoalMetric == metric && i.Stage != Stage.Retired))
        {
            var met = (idea.GoalMin is not { } min || value >= min) && (idea.GoalMax is not { } max || value <= max);
            o.WriteLine($"{idea.Id} goal {(met ? "met" : "not met")}: needs {idea.GoalTarget}.");
        }
    }

    private void Spec(string metric, double lo, double hi)
    {
        if (lo > hi) throw new UserError("low must not be above high");
        Record(new(now, "spec", metric, Value: lo, Hi: hi));
        o.WriteLine($"{metric} is good between {Text.Num(lo)} and {Text.Num(hi)}.");
    }

    private void Metrics()
    {
        if (s.Metrics.Count == 0) { o.WriteLine("Nothing measured yet. vinci log <metric> <value>"); return; }
        foreach (var m in s.Metrics.Where(m => m.Points.Count > 0))
        {
            var values = m.Points.Select(p => p.Value).ToList();
            o.WriteLine($"{m.Name,-20} n {values.Count,-4} mean {Text.Num(Stats.Mean(values)),-8} last {Text.Num(values[^1])} on {Text.Day(m.Points[^1].At)}");
        }
    }

    private void MetricDetail(string name)
    {
        var m = s.Metric(name);
        if (m is null || m.Points.Count == 0) throw new UserError($"nothing logged for '{name}'");
        var values = m.Points.Select(p => p.Value).ToList();
        o.WriteLine($"{name}: {values.Count} measurements, {Text.Day(m.Points[0].At)} to {Text.Day(m.Points[^1].At)}");
        o.WriteLine($"mean     {Text.Num(Stats.Mean(values))}   min {Text.Num(values.Min())}   max {Text.Num(values.Max())}   last {Text.Num(values[^1])}");

        if (Stats.ControlLimits(values) is { } limits)
        {
            o.WriteLine($"control  {Text.Num(limits.Lower)} .. {Text.Num(limits.Upper)}  (ordinary variation stays inside)");
            var outside = m.Points.Where(p => !limits.Holds(p.Value)).ToList();
            foreach (var p in outside) o.WriteLine($"  special cause: {Text.Num(p.Value)} on {Text.Day(p.At)}");
            if (outside.Count == 0) o.WriteLine("  stable: no point has a special cause. Changing the result means changing the process.");
        }

        if (m.Spec is var (lo, hi))
        {
            var defects = values.Count(v => v < lo || v > hi);
            var sigma = Stats.SigmaLevel(defects, values.Count);
            var level = values.Count < Stats.MinForSigmaLevel ? $"too few to give a sigma level ({values.Count} of {Stats.MinForSigmaLevel})"
                : double.IsPositiveInfinity(sigma) ? "no defects yet"
                : $"sigma level {sigma:0.0}";
            o.WriteLine($"spec     {Text.Num(lo)} .. {Text.Num(hi)}: {defects} of {values.Count} outside, {level}");
        }
        else o.WriteLine($"spec     none. Say what good looks like: vinci spec {name} <low> <high>");
    }

    // ---- getting better: issue, root cause, principle ----

    /// After the text come, in any order: the idea it happened on, the word
    /// "big", and a principle that should have prevented it.
    private void NewIssue(string text, string[] rest)
    {
        var refs = new List<string>();
        var big = false;
        foreach (var token in rest)
        {
            if (token == "big") big = true;
            else if (s.Idea(token) is not null || s.Principle(token) is not null) refs.Add(token);
            else throw new UserError($"'{token}' is not an idea, a principle, or the word big");
        }
        var id = s.NextId('x');
        Record(new(now, "issue", id, text, refs.Count > 0 ? [.. refs] : null, Value: big ? 1 : null));
        o.WriteLine($"{id} logged. Do not solve it yet. Find the root cause: vinci diagnose {id} \"...\"");
    }

    private void Diagnose(string id, string rootCause)
    {
        var issue = s.Issue(id) ?? throw new UserError($"no issue {id}");
        Record(new(now, "diagnose", issue.Id, rootCause));
        o.WriteLine($"{id} diagnosed. Now change the machine: vinci principle \"what I do from now on\" {id}");
    }

    private void NewPrinciple(string text, string? issueId)
    {
        if (issueId is not null)
        {
            var issue = s.Issue(issueId) ?? throw new UserError($"no issue {issueId}");
            if (issue.RootCause is null)
                throw new UserError($"{issueId} has no root cause. Diagnose before you design: vinci diagnose {issueId} \"...\"");
            if (issue.PrincipleId is not null) throw new UserError($"{issueId} is already closed by {issue.PrincipleId}");
        }
        var id = s.NextId('p');
        Record(new(now, "principle", id, text, issueId is null ? null : [issueId]));
        o.WriteLine($"{id} adopted.{(issueId is null ? "" : $" {issueId} is closed.")} If it fails: vinci issue \"...\" {id}");
    }

    private static string IssueLine(Issue x) =>
        $"{x.Id,-4} {(x.PrincipleId is not null ? "closed   " : x.RootCause is not null ? "diagnosed" : "open     ")} {(x.Big ? "BIG " : "")}{x.Text}";

    private void Issues()
    {
        if (s.Issues.Count == 0) { o.WriteLine("No issues logged. vinci issue \"what went wrong\""); return; }
        foreach (var x in s.Issues) o.WriteLine(IssueLine(x));

        // Where you usually fail, most common first.
        var byStage = s.Issues.Where(x => x.Stage is not null).GroupBy(x => x.Stage!.Value).OrderByDescending(g => g.Count()).ToList();
        if (byStage.Count > 0)
            o.WriteLine($"By stage: {string.Join(", ", byStage.Select(g => $"{g.Key} {g.Count()}"))}. Most go wrong in {byStage[0].Key}.");
    }

    private void Principles()
    {
        if (s.Principles.Count == 0) { o.WriteLine("No principles yet. They come from diagnosed issues."); return; }
        foreach (var p in s.Principles)
        {
            var from = p.IssueId is null ? "" : $"  (from {p.IssueId}{(p.Stage is null ? "" : $", {p.Stage}")})";
            var breaks = s.Breaks(p) is var n and > 0 ? $"  broken {n} times" : "";
            o.WriteLine($"{p.Id,-4} {p.Text}{from}{breaks}");
        }
    }

    // ---- the week ----

    private void Commit(string text, string? ideaId)
    {
        if (s.OpenCommitment is { } open)
            throw new UserError($"you already committed to \"{open.Text}\". Answer for it first: vinci kept, or vinci missed \"why\"");
        if (ideaId is not null && s.Idea(ideaId) is null) throw new UserError($"no idea {ideaId}");
        Record(new(now, "commit", Text: text, Refs: ideaId is null ? null : [ideaId]));
        o.WriteLine($"Committed. Due {Text.Day(now + Review.CommitmentLength)}.");
    }

    private void Kept()
    {
        if (s.OpenCommitment is null) throw new UserError("there is no open commitment");
        Record(new(now, "kept"));
        o.WriteLine($"Kept. {CommitmentRecord(1, 0)} What is next? vinci commit \"...\"");
    }

    /// A missed commitment is a problem like any other, so it becomes an issue.
    private void Missed(string why)
    {
        var open = s.OpenCommitment ?? throw new UserError("there is no open commitment");
        Record(new(now, "missed", Text: why));
        var id = s.NextId('x');
        Record(new(now, "issue", id, $"Missed commitment \"{open.Text}\": {why}", open.IdeaId is null ? null : [open.IdeaId]));
        o.WriteLine($"Missed. {CommitmentRecord(0, 1)} Logged as {id}: vinci diagnose {id} \"root cause\"");
    }

    private string CommitmentRecord(int kept, int missed)
    {
        kept += s.Commitments.Count(c => c.Kept == true);
        var total = kept + missed + s.Commitments.Count(c => c.Kept == false);
        return $"{kept} of {total} commitments kept.";
    }

    private void RunReview()
    {
        var findings = Review.Run(s, now);
        foreach (var f in findings)
        {
            o.WriteLine($"{f.Subject,-6} {f.Problem}");
            o.WriteLine($"       fix: {f.Fix}");
        }

        var before = s.Metric(State.FindingsMetric)?.Points;
        var trend = before is { Count: > 0 } ? $" Last review: {Text.Num(before[^1].Value)}." : "";
        o.WriteLine(findings.Count == 0 ? $"Nothing to fix.{trend}" : $"{findings.Count} to fix.{trend}");
        if (s.Commitments.Exists(c => c.Kept is not null)) o.WriteLine(CommitmentRecord(0, 0));
        Record(new(now, "review", Value: findings.Count));
    }

    // ---- plumbing ----

    private void Record(Event e) => journal.Append(e);

    /// A lone "-" means the text comes from standard input, so vinci can sit
    /// at the end of a pipe: git log -1 --format=%s | vinci ship i1 -
    private string Arg(string[] a, int index, string name)
    {
        if (index >= a.Length) throw new UserError($"missing <{name}>");
        var value = a[index] == "-" ? input.ReadToEnd() : a[index];
        value = string.Join(' ', value.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries));
        return value.Length > 0 ? value : throw new UserError($"<{name}> is empty");
    }

    private Idea IdeaArg(string[] a)
    {
        var id = Arg(a, 0, "idea id");
        return s.Idea(id) ?? throw new UserError($"no idea {id}");
    }

    private static double Number(string text, string name) =>
        double.TryParse(text, NumberStyles.Float, CultureInfo.InvariantCulture, out var n) && double.IsFinite(n)
            ? n
            : throw new UserError($"<{name}> must be a number, got '{text}'");

    private const string Help = """
        vinci: one journal for ideas, knowledge, time and measurements.
        Run it with no command to see the pipeline and the one thing to do next.

        An idea grows through five stages, and each move has a gate:
          Seed -> Prepare      needs who it is for, and a goal tied to a number
          Prepare -> Incubate  needs a note, a questioned requirement, a prediction
          Incubate -> Build    needs the wait to be over, and fewer than 3 in Build
          Build -> Tend        happens when you ship
          Tend                 never ends: ship again, use it, tend it, or retire it

        Ideas
          idea "title"                        capture an idea
          for <idea> "who"                    who will use it
          goal <idea> "goal" <metric> at-least <n> | at-most <n> | between <a> <b>
          req <idea> "text" "who" "why"       a requirement, who asked, the reason
          cut <req> "why"                     delete a requirement
          advance <idea> [days]               pass the next gate (days: incubation, default 1)
          ship <idea> "what"                  record something shipped
          used <idea> ["how it went"]         record that you used what you shipped
          tend <idea> "what"                  record upkeep on a grown idea
          retire <idea> "why"                 end an idea on purpose
          pipeline   next   ideas   show <id>

        Time
          adopt <folder>                      make an existing folder an idea, named after it
          home <idea> <folder>                where the idea lives: its editor windows and commits count as work
          watch <idea> "text" [use]           count time in windows whose app or title contains the text;
                                              with "use", such a window counts as using what you shipped
          unwatch <idea> "text"               stop counting it
          watches                             what is watched
          sync                                show what was just counted (every command counts it)
          spent <idea> <minutes>              add time spent away from the screen

        Knowledge
          source "citation" [url]             where knowledge came from
          note "text" [id...] [+tag...]       one thought. s1:p.42 cites a place in a source
          link <note> <id>                    connect a note to a note, idea or prediction
          notes [word | +tag]   tags   sources

        Inquiry
          predict "claim" <percent> [idea]    what you expect, before you test it
          result <h> right|wrong "what happened"
          predictions                         your record against your confidence

        Measurement
          log <metric> <value> ["comment"]    measure anything
          spec <metric> <low> <high>          what counts as good
          stats [metric]                      control limits, defects, sigma level

        Improvement
          issue "what went wrong" [idea] [big] [principle that failed]
          diagnose <issue> "root cause"       why it really happened
          principle "rule" [issue]            what you do from now on
          issues   principles
          commit "one thing" [idea]           what you will get done this week
          kept | missed "why"                 answer for it
          review                              what needs fixing now. Run it weekly.

        In a pipe
          Any quoted text can be "-" to read it from standard input:
            git log -1 --format=%s | vinci ship i1 -
          vinci path prints the journal file, one JSON object per line:
            jq -r 'select(.kind=="ship") | .text' "$(vinci path)"

        Data lives in $VINCI_HOME/journal.jsonl (default ~/.vinci).
        Window time comes from ActivityWatch at $VINCI_ACTIVITYWATCH (default http://127.0.0.1:5600).

        """;
}
