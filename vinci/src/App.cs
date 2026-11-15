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
}

public static class App
{
    public static int Run(string[] args, Journal journal, TextWriter output, TextWriter errors, DateTimeOffset now)
    {
        try
        {
            new Session(journal, output, now).Dispatch(args);
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
public sealed class Session(Journal journal, TextWriter o, DateTimeOffset now)
{
    private readonly State s = State.From(journal.Read(), now);

    public void Dispatch(string[] args)
    {
        if (args.Length == 0) { o.Write(Help); return; }
        var a = args[1..];
        switch (args[0])
        {
            case "idea": NewIdea(Arg(a, 0, "title")); break;
            case "goal": Goal(IdeaArg(a), Arg(a, 1, "measurable goal")); break;
            case "req": Req(IdeaArg(a), Arg(a, 1, "requirement"), Arg(a, 2, "who asked, by name"), Arg(a, 3, "why")); break;
            case "cut": Cut(Arg(a, 0, "requirement id"), Arg(a, 1, "why")); break;
            case "advance": Advance(IdeaArg(a), a.Length > 1 ? Number(a[1], "days") : 1); break;
            case "start": Start(IdeaArg(a)); break;
            case "stop": Stop(a.Length > 0 ? Number(a[0], "minutes") : null); break;
            case "spent": Spent(IdeaArg(a), Number(Arg(a, 1, "minutes"), "minutes")); break;
            case "ship": Ship(IdeaArg(a), Arg(a, 1, "what shipped")); break;
            case "tend": Tend(IdeaArg(a), Arg(a, 1, "what you did")); break;
            case "retire": Retire(IdeaArg(a), Arg(a, 1, "why")); break;
            case "ideas": Ideas(); break;
            case "source": NewSource(Arg(a, 0, "citation"), a.ElementAtOrDefault(1)); break;
            case "note": NewNote(Arg(a, 0, "text"), a[1..]); break;
            case "link": Link(Arg(a, 0, "note id"), Arg(a, 1, "note or idea id")); break;
            case "notes": Notes(a.ElementAtOrDefault(0)); break;
            case "sources": Sources(); break;
            case "show": Show(Arg(a, 0, "id")); break;
            case "log": Log(Arg(a, 0, "metric"), Number(Arg(a, 1, "value"), "value"), a.ElementAtOrDefault(2)); break;
            case "spec": Spec(Arg(a, 0, "metric"), Number(Arg(a, 1, "low"), "low"), Number(Arg(a, 2, "high"), "high")); break;
            case "stats": if (a.Length == 0) Metrics(); else MetricDetail(a[0]); break;
            case "issue": NewIssue(Arg(a, 0, "what went wrong"), a.ElementAtOrDefault(1)); break;
            case "diagnose": Diagnose(Arg(a, 0, "issue id"), Arg(a, 1, "root cause")); break;
            case "principle": NewPrinciple(Arg(a, 0, "principle"), a.ElementAtOrDefault(1)); break;
            case "issues": Issues(); break;
            case "principles": Principles(); break;
            case "review": RunReview(); break;
            case "help" or "--help" or "-h": o.Write(Help); break;
            default: throw new UserError($"unknown command '{args[0]}'. Run 'vinci help'.");
        }
    }

    // ---- ideas: inception to grown ----

    private void NewIdea(string title)
    {
        var id = s.NextId('i');
        Record(new(now, "idea", id, title));
        o.WriteLine($"{id} seeded. Next: vinci goal {id} \"<a goal you can measure>\"");
    }

    private void Goal(Idea idea, string goal)
    {
        Record(new(now, "goal", idea.Id, goal));
        o.WriteLine($"{idea.Id} goal set.");
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
                if (idea.Goal is null)
                    throw new UserError($"{idea.Id} has no goal. Set one you can measure: vinci goal {idea.Id} \"...\"");
                MoveTo(idea, Stage.Prepare);
                o.WriteLine($"Next: gather notes (vinci note \"...\" {idea.Id}) and question every requirement (vinci req).");
                break;

            case Stage.Prepare:
                if (!s.NotesAbout(idea.Id).Any())
                    throw new UserError($"{idea.Id} has no notes. Preparation is gathering what is known: vinci note \"...\" <source> {idea.Id}");
                if (!s.Requirements.Exists(r => r.IdeaId == idea.Id && r.Live))
                    throw new UserError($"{idea.Id} has no requirements. State what it must do, who says so, and why: vinci req {idea.Id} \"...\" \"name\" \"why\"");
                if (days < 0) throw new UserError("days cannot be negative");
                Record(new(now, "stage", idea.Id, nameof(Stage.Incubate), Value: days));
                o.WriteLine($"{idea.Id} is incubating until {Text.Day(now.AddDays(days))}. Work on something else.");
                break;

            case Stage.Incubate:
                if (now < idea.IncubateUntil)
                    throw new UserError($"{idea.Id} is incubating until {Text.Day(idea.IncubateUntil.Value)}. Leave it alone.");
                MoveTo(idea, Stage.Build);
                o.WriteLine($"Next: vinci start {idea.Id}, then ship the smallest thing that works.");
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
    }

    private void Ship(Idea idea, string what)
    {
        if (idea.Stage is not (Stage.Build or Stage.Tend))
            throw new UserError($"{idea.Id} is in {idea.Stage}. It ships from Build or Tend: vinci advance {idea.Id}");
        var first = idea.Ships.Count == 0;
        var cycle = now - (first ? idea.Born : idea.Ships[^1].At);
        Record(new(now, "ship", idea.Id, what));
        o.WriteLine($"{idea.Id} shipped (#{idea.Ships.Count + 1}). Cycle time {Text.Span(cycle)} since {(first ? "inception" : "the last ship")}; touch time {Text.Span(idea.Touch)} in total.");
        if (first) o.WriteLine($"{idea.Id} is now in Tend. Keep it alive.");
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
        if (s.Running?.IdeaId == idea.Id) Record(new(now, "stop"));
        Record(new(now, "retire", idea.Id, why));
        o.WriteLine($"{idea.Id} retired on purpose. Its notes stay.");
    }

    private void Ideas()
    {
        if (s.Ideas.Count == 0) { o.WriteLine("No ideas yet. vinci idea \"title\""); return; }
        foreach (var i in s.Ideas)
        {
            var running = s.Running?.IdeaId == i.Id ? " ●" : "";
            o.WriteLine($"{i.Id,-4} {i.Stage,-8} touch {Text.Span(i.Touch),-7} age {Text.Span(now - i.Born),-7} flow {Flow(i),-6} {i.Title}{running}");
        }
    }

    /// Touch time over elapsed time: how much of the idea's life was work
    /// rather than waiting. The gap is where the improvement is.
    private string Flow(Idea i)
    {
        var age = now - i.Born;
        return age <= TimeSpan.Zero ? "-" : (i.Touch / age).ToString("0.0%", CultureInfo.InvariantCulture);
    }

    // ---- time ----

    private void Start(Idea idea)
    {
        if (idea.Stage == Stage.Retired) throw new UserError($"{idea.Id} is retired");
        if (s.Running?.IdeaId == idea.Id) throw new UserError($"already timing {idea.Id}");
        if (s.Running is var (other, since)) o.WriteLine($"Stopped {other} after {Text.Span(now - since)}.");
        Record(new(now, "start", idea.Id));
        o.WriteLine($"Timing {idea.Id}: {idea.Title}");
    }

    private void Stop(double? minutes)
    {
        if (s.Running is not var (ideaId, since)) throw new UserError("no timer is running");
        if (minutes < 0) throw new UserError("minutes cannot be negative");
        Record(new(now, "stop", ideaId, Value: minutes));
        var spent = minutes is { } m ? TimeSpan.FromMinutes(m) : now - since;
        o.WriteLine($"Stopped {ideaId} after {Text.Span(spent)}.");
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
        o.WriteLine($"{id} recorded. Cite it: vinci note \"one idea, in your own words\" {id}");
    }

    private void NewNote(string text, string[] refs)
    {
        foreach (var r in refs)
        {
            if (r.Length == 0 || r[0] is not ('n' or 'i' or 's') || !s.Has(r))
                throw new UserError($"'{r}' is not a note, idea or source that exists");
        }
        var id = s.NextId('n');
        Record(new(now, "note", id, text, refs.Length > 0 ? refs : null));
        var origin = refs.Any(r => r[0] == 's') ? "" : " No source given, so it is recorded as your own thought.";
        o.WriteLine($"{id} written.{origin}");
    }

    private void Link(string from, string to)
    {
        var note = s.Note(from) ?? throw new UserError($"no note {from}");
        if (to.Length == 0 || to[0] is not ('n' or 'i' or 's') || !s.Has(to)) throw new UserError($"'{to}' is not a note, idea or source that exists");
        if (from == to) throw new UserError("a note cannot link to itself");
        if (note.Refs.Contains(to)) throw new UserError($"{from} already links to {to}");
        Record(new(now, "link", from, Refs: [to]));
        o.WriteLine($"{from} → {to}");
    }

    private void Notes(string? query)
    {
        var found = s.Notes.Where(n => query is null || n.Text.Contains(query, StringComparison.OrdinalIgnoreCase)).ToList();
        if (found.Count == 0) { o.WriteLine("No notes."); return; }
        foreach (var n in found) o.WriteLine(NoteLine(n));
    }

    private void Sources()
    {
        if (s.Sources.Count == 0) { o.WriteLine("No sources yet. vinci source \"citation\" [url]"); return; }
        foreach (var src in s.Sources)
            o.WriteLine($"{src.Id,-4} {src.Citation}{(src.Url is null ? "" : $" <{src.Url}>")}  ({s.NotesAbout(src.Id).Count()} notes)");
    }

    private string NoteLine(Note n) =>
        $"{n.Id,-4} {n.Text}{(n.Refs.Count > 0 ? $"  [{string.Join(' ', n.Refs)}]" : "")}";

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
            o.WriteLine($"{issue.Id}  {issue.Text}");
            o.WriteLine($"raised      {Text.Day(issue.At)}{(issue.IdeaId is null ? "" : $" on {issue.IdeaId}")}");
            o.WriteLine($"root cause  {issue.RootCause ?? "(not diagnosed)"}");
            var principle = s.Principles.Find(p => p.Id == issue.PrincipleId);
            o.WriteLine($"principle   {(principle is null ? "(none yet)" : $"{principle.Id} {principle.Text}")}");
        }
        else throw new UserError($"nothing has the id '{id}'");
    }

    private void ShowIdea(Idea i)
    {
        o.WriteLine($"{i.Id}  {i.Title}");
        o.WriteLine($"stage  {i.Stage}{(i.Stage == Stage.Incubate ? $" until {Text.Day(i.IncubateUntil!.Value)}" : "")}{(i.RetiredWhy is null ? "" : $": {i.RetiredWhy}")}");
        o.WriteLine($"goal   {i.Goal ?? "(none)"}");
        o.WriteLine($"time   born {Text.Day(i.Born)}, age {Text.Span(now - i.Born)}, touch {Text.Span(i.Touch)}, flow {Flow(i)}");
        Section("requirements", s.Requirements.Where(r => r.IdeaId == i.Id).Select(r =>
            r.Live ? $"{r.Id,-4} {r.Text}  ({r.Who}: {r.Why})" : $"{r.Id,-4} CUT {r.Text}  ({r.CutWhy})"));
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
        var sources = n.Refs.Where(r => r[0] == 's').Select(r => s.Source(r)!).ToList();
        if (sources.Count == 0) o.WriteLine("origin   your own thought");
        foreach (var src in sources) o.WriteLine($"origin   {src.Id} {src.Citation}{(src.Url is null ? "" : $" <{src.Url}>")}");
        Section("links to", n.Refs.Where(r => r[0] != 's').Select(Describe));
        Section("linked from", s.NotesAbout(n.Id).Select(NoteLine));
    }

    private string Describe(string id) =>
        s.Note(id) is { } note ? NoteLine(note) : $"{id,-4} {s.Idea(id)!.Title}";

    private void Section(string title, IEnumerable<string> lines)
    {
        var list = lines.ToList();
        if (list.Count == 0) return;
        o.WriteLine(title);
        foreach (var line in list) o.WriteLine($"  {line}");
    }

    // ---- measuring anything ----

    private void Log(string metric, double value, string? comment)
    {
        if (metric == State.FindingsMetric) throw new UserError($"{metric} is kept by vinci itself");
        Record(new(now, "log", metric, comment, Value: value));
        var n = (s.Metric(metric)?.Points.Count ?? 0) + 1;
        o.WriteLine($"{metric} = {Num(value)} (measurement {n}).");
    }

    private void Spec(string metric, double lo, double hi)
    {
        if (lo > hi) throw new UserError("low must not be above high");
        Record(new(now, "spec", metric, Value: lo, Hi: hi));
        o.WriteLine($"{metric} is good between {Num(lo)} and {Num(hi)}.");
    }

    private void Metrics()
    {
        if (s.Metrics.Count == 0) { o.WriteLine("Nothing measured yet. vinci log <metric> <value>"); return; }
        foreach (var m in s.Metrics.Where(m => m.Points.Count > 0))
        {
            var values = m.Points.Select(p => p.Value).ToList();
            o.WriteLine($"{m.Name,-20} n {values.Count,-4} mean {Num(Stats.Mean(values)),-8} last {Num(values[^1])} on {Text.Day(m.Points[^1].At)}");
        }
    }

    private void MetricDetail(string name)
    {
        var m = s.Metric(name);
        if (m is null || m.Points.Count == 0) throw new UserError($"nothing logged for '{name}'");
        var values = m.Points.Select(p => p.Value).ToList();
        o.WriteLine($"{name}: {values.Count} measurements, {Text.Day(m.Points[0].At)} to {Text.Day(m.Points[^1].At)}");
        o.WriteLine($"mean     {Num(Stats.Mean(values))}   min {Num(values.Min())}   max {Num(values.Max())}   last {Num(values[^1])}");

        if (Stats.ControlLimits(values) is { } limits)
        {
            o.WriteLine($"control  {Num(limits.Lower)} .. {Num(limits.Upper)}  (ordinary variation stays inside)");
            var outside = m.Points.Where(p => !limits.Holds(p.Value)).ToList();
            foreach (var p in outside) o.WriteLine($"  special cause: {Num(p.Value)} on {Text.Day(p.At)}");
            if (outside.Count == 0) o.WriteLine("  stable: no point has a special cause. Changing the result means changing the process.");
        }

        if (m.Spec is var (lo, hi))
        {
            var defects = values.Count(v => v < lo || v > hi);
            var sigma = Stats.SigmaLevel(defects, values.Count);
            var level = double.IsPositiveInfinity(sigma) ? $"no defects in {values.Count}; too few to rate" : $"sigma level {sigma:0.0}";
            o.WriteLine($"spec     {Num(lo)} .. {Num(hi)}: {defects} of {values.Count} outside, {Stats.Dpmo(defects, values.Count):0} per million, {level}");
        }
        else o.WriteLine($"spec     none. Say what good looks like: vinci spec {name} <low> <high>");
    }

    // ---- getting better: issue, root cause, principle ----

    private void NewIssue(string text, string? ideaId)
    {
        if (ideaId is not null && s.Idea(ideaId) is null) throw new UserError($"no idea {ideaId}");
        var id = s.NextId('x');
        Record(new(now, "issue", id, text, ideaId is null ? null : [ideaId]));
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
        o.WriteLine($"{id} adopted.{(issueId is null ? "" : $" {issueId} is closed.")}");
    }

    private string IssueLine(Issue x) =>
        $"{x.Id,-4} {(x.PrincipleId is not null ? "closed   " : x.RootCause is not null ? "diagnosed" : "open     ")} {x.Text}";

    private void Issues()
    {
        if (s.Issues.Count == 0) { o.WriteLine("No issues logged. vinci issue \"what went wrong\""); return; }
        foreach (var x in s.Issues) o.WriteLine(IssueLine(x));
    }

    private void Principles()
    {
        if (s.Principles.Count == 0) { o.WriteLine("No principles yet. They come from diagnosed issues."); return; }
        foreach (var p in s.Principles)
            o.WriteLine($"{p.Id,-4} {p.Text}{(p.IssueId is null ? "" : $"  (from {p.IssueId})")}");
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
        var trend = before is { Count: > 0 } ? $" Last review: {Num(before[^1].Value)}." : "";
        o.WriteLine(findings.Count == 0 ? $"Nothing to fix.{trend}" : $"{findings.Count} to fix.{trend}");
        Record(new(now, "review", Value: findings.Count));
    }

    // ---- plumbing ----

    private void Record(Event e) => journal.Append(e);

    private static string Arg(string[] a, int index, string name) =>
        index < a.Length && a[index].Trim().Length > 0 ? a[index].Trim() : throw new UserError($"missing <{name}>");

    private Idea IdeaArg(string[] a)
    {
        var id = Arg(a, 0, "idea id");
        return s.Idea(id) ?? throw new UserError($"no idea {id}");
    }

    private static double Number(string text, string name) =>
        double.TryParse(text, NumberStyles.Float, CultureInfo.InvariantCulture, out var n) && double.IsFinite(n)
            ? n
            : throw new UserError($"<{name}> must be a number, got '{text}'");

    private static string Num(double v) => v.ToString("0.##", CultureInfo.InvariantCulture);

    private const string Help = """
        vinci: one journal for ideas, knowledge, time and measurements.

        An idea grows through five stages, and each move has a gate:
          Seed -> Prepare    needs a goal you can measure
          Prepare -> Incubate needs notes and at least one questioned requirement
          Incubate -> Build  needs the incubation time to have passed
          Build -> Tend      happens when you ship
          Tend               never ends: ship again, tend it, or retire it with a reason

        Ideas
          idea "title"                      capture an idea
          goal <idea> "goal"                say what success is, measurably
          req <idea> "text" "who" "why"     a requirement, the person who asked, the reason
          cut <req> "why"                   delete a requirement
          advance <idea> [days]             pass the next gate (days: incubation, default 1)
          ship <idea> "what"                record something shipped
          tend <idea> "what"                record upkeep on a grown idea
          retire <idea> "why"               end an idea on purpose
          ideas                             every idea, with touch time and flow

        Time
          start <idea>                      start the timer (stops any other)
          stop [minutes]                    stop it; minutes corrects a timer left running
          spent <idea> <minutes>            add time after the fact

        Knowledge
          source "citation" [url]           where knowledge came from
          note "text" [ids...]              one thought; ids are sources, ideas, notes
          link <note> <id>                  connect a note to a note or idea
          notes [word]   sources   show <id>

        Measurement
          log <metric> <value> ["comment"]  measure anything
          spec <metric> <low> <high>        what counts as good
          stats [metric]                    control limits, defects, sigma level

        Improvement
          issue "what went wrong" [idea]    log a problem
          diagnose <issue> "root cause"     why it really happened
          principle "rule" [issue]          what you do from now on
          issues   principles
          review                            what needs fixing now. Run it weekly.

        Data lives in $VINCI_HOME/journal.jsonl (default ~/.vinci).

        """;
}
