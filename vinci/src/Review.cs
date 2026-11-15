namespace Vinci;

/// Something the system found wrong, and the command that fixes it.
/// Rank orders findings: the lowest rank is the thing to do first.
public sealed record Finding(int Rank, string Subject, string Problem, string Fix);

/// The loop that keeps the system honest. Each finding is a defect; the count
/// per review is logged as a metric, so the system's own health gets the same
/// control chart as anything else it measures.
///
/// Every limit below is a first guess. Change one when a logged issue says so.
public static class Review
{
    public static readonly TimeSpan BuildingIdleLimit = TimeSpan.FromDays(14);
    public static readonly TimeSpan TendingIdleLimit = TimeSpan.FromDays(30);
    public static readonly TimeSpan UnusedLimit = TimeSpan.FromDays(14);
    public static readonly TimeSpan PredictionLimit = TimeSpan.FromDays(30);
    public static readonly TimeSpan CommitmentLength = TimeSpan.FromDays(7);
    public const int MinPointsForControl = 5;
    public const int BreaksBeforeRethink = 2;

    /// How many ideas may be in Build at once. More than this and none of them ships.
    public const int BuildLimit = 3;

    public static List<Finding> Run(State s, DateTimeOffset now)
    {
        var found = new List<Finding>();

        var live = s.Ideas.Where(i => i.Stage != Stage.Retired).ToList();

        if (s.OpenCommitment is { } open)
        {
            if (now - open.At >= CommitmentLength)
                found.Add(
                    new(
                        1,
                        "week",
                        $"commitment from {Text.Day(open.At)} is due: {Text.Clip(open.Text, 50)}",
                        "vinci kept, or vinci missed \"why\""
                    )
                );
        }
        else if (live.Count > 0)
            found.Add(
                new(
                    1,
                    "week",
                    "no commitment for this week",
                    "vinci commit \"the one thing I will get done\" [idea]"
                )
            );

        foreach (var idea in live)
        {
            if (idea.Stage == Stage.Incubate)
            {
                if (now >= idea.IncubateUntil)
                    found.Add(
                        new(
                            3,
                            idea.Id,
                            $"incubation is over: {idea.Title}",
                            $"vinci advance {idea.Id}"
                        )
                    );
                continue;
            }

            if (
                idea.Stage == Stage.Tend
                && idea.Uses.Count == 0
                && now - idea.Ships[0].At > UnusedLimit
            )
                found.Add(
                    new(
                        4,
                        idea.Id,
                        $"shipped {Text.Span(now - idea.Ships[0].At)} ago and never used: {idea.Title}",
                        $"use it and vinci used {idea.Id}, or vinci retire {idea.Id} \"why\""
                    )
                );

            if (idea.Stage == Stage.Build && !s.Watched(idea))
                found.Add(
                    new(
                        3,
                        idea.Id,
                        $"has no home, so its time is not counted: {idea.Title}",
                        $"vinci home {idea.Id} <folder>, or vinci watch {idea.Id} \"text in its window titles\""
                    )
                );

            var idle = now - idea.LastTouched;
            var limit = idea.Stage == Stage.Tend ? TendingIdleLimit : BuildingIdleLimit;
            if (idle > limit)
                found.Add(
                    new(
                        6,
                        idea.Id,
                        $"untouched for {Text.Span(idle)}: {idea.Title}",
                        $"work on it, vinci tend {idea.Id} \"...\", or vinci retire {idea.Id} \"why\""
                    )
                );
        }

        foreach (var issue in s.Issues.Where(x => x.PrincipleId is null))
        {
            var rank = issue.Big ? 2 : 5;
            var size = issue.Big ? "big issue, " : "";
            found.Add(
                issue.RootCause is null
                    ? new(
                        rank,
                        issue.Id,
                        $"{size}no root cause yet: {Text.Clip(issue.Text, 50)}",
                        $"vinci diagnose {issue.Id} \"root cause\""
                    )
                    : new(
                        rank,
                        issue.Id,
                        $"{size}diagnosed but nothing changed: {Text.Clip(issue.Text, 50)}",
                        $"vinci principle \"what I do from now on\" {issue.Id}"
                    )
            );
        }

        foreach (var p in s.Principles.Where(p => s.Breaks(p) >= BreaksBeforeRethink))
            found.Add(
                new(
                    7,
                    p.Id,
                    $"broken {s.Breaks(p)} times, so the rule is not working: {Text.Clip(p.Text, 50)}",
                    "a rule you keep breaking needs a change to the process, not more willpower"
                )
            );

        foreach (var h in s.Predictions.Where(h => h.Right is null && now - h.At > PredictionLimit))
            found.Add(
                new(
                    8,
                    h.Id,
                    $"predicted {Text.Span(now - h.At)} ago and never tested: {Text.Clip(h.Text, 50)}",
                    $"run the test, then vinci result {h.Id} right|wrong \"what happened\""
                )
            );

        foreach (var metric in s.Metrics.Where(m => m.Points.Count >= MinPointsForControl))
        {
            var values = metric.Points.Select(p => p.Value).ToList();
            var limits = Stats.ControlLimits(values)!;
            if (!limits.Holds(values[^1]))
                found.Add(
                    new(
                        9,
                        metric.Name,
                        $"out of control: last value {Text.Num(values[^1])} is outside {Text.Num(limits.Lower)}..{Text.Num(limits.Upper)}",
                        "find the special cause, then vinci issue \"...\""
                    )
                );
        }

        var linkedTo = s.Notes.SelectMany(n => n.Refs).ToHashSet();
        foreach (var note in s.Notes)
        {
            var linksOut = note.Refs.Exists(r => r[0] is 'n' or 'i' or 'h');
            if (!linksOut && !linkedTo.Contains(note.Id))
                found.Add(
                    new(
                        10,
                        note.Id,
                        $"note connects to nothing: {Text.Clip(note.Text, 50)}",
                        $"vinci link {note.Id} <note or idea>"
                    )
                );
        }

        return [.. found.OrderBy(f => f.Rank)];
    }

    /// The one thing to do now: the worst finding, or failing that the idea
    /// in Build that has waited longest, or failing that the idea nearest Build.
    public static (string What, string How) Next(State s, DateTimeOffset now)
    {
        if (Run(s, now) is [var worst, ..])
            return ($"{worst.Subject}: {worst.Problem}", worst.Fix);

        var live = s
            .Ideas.Where(i => i.Stage != Stage.Retired)
            .OrderBy(i => i.LastTouched)
            .ToList();
        if (live.Find(i => i.Stage == Stage.Build) is { } building)
            return (
                $"build {building.Id}: {building.Title}",
                $"vinci ship {building.Id} \"what shipped\""
            );
        if (live.Find(i => i.Stage is Stage.Prepare or Stage.Seed) is { } early)
            return ($"move {early.Id} forward: {early.Title}", $"vinci advance {early.Id}");
        if (live.Find(i => i.Stage == Stage.Tend && s.GoalMet(i) != true) is { } unmet)
            return (
                $"{unmet.Id} has not met its goal: {unmet.Title}",
                $"vinci log {unmet.GoalMetric} <value>"
            );
        return ("nothing is waiting on you", "vinci idea \"title\"");
    }

    /// The stage where ideas sit longest on average: the constraint. Incubate
    /// is left out because that wait is deliberate, and Tend because it never ends.
    public static (Stage Stage, TimeSpan Average)? SlowestStage(State s, DateTimeOffset now)
    {
        (Stage, TimeSpan)? slowest = null;
        foreach (var stage in new[] { Stage.Seed, Stage.Prepare, Stage.Build })
        {
            var times = s
                .Ideas.Where(i => i.Entered(stage))
                .Select(i => i.TimeIn(stage, now).TotalSeconds)
                .ToList();
            if (times.Count == 0)
                continue;
            var average = TimeSpan.FromSeconds(times.Average());
            if (slowest is null || average > slowest.Value.Item2)
                slowest = (stage, average);
        }
        return slowest;
    }
}
