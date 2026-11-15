namespace Vinci;

/// Something the system found wrong with itself, and the command that fixes it.
public sealed record Finding(string Subject, string Problem, string Fix);

/// The loop that keeps the system honest. Each finding is a defect; the count
/// per review is logged as a metric, so the system's own health gets the same
/// control chart as anything else it measures.
public static class Review
{
    public static readonly TimeSpan BuildingIdleLimit = TimeSpan.FromDays(14);
    public static readonly TimeSpan TendingIdleLimit = TimeSpan.FromDays(30);
    public static readonly TimeSpan TimerLimit = TimeSpan.FromHours(12);
    public const int MinPointsForControl = 5;

    public static List<Finding> Run(State s, DateTimeOffset now)
    {
        var findings = new List<Finding>();

        if (s.Running is var (runningId, since) && now - since > TimerLimit)
            findings.Add(new(runningId, $"timer has run {Text.Span(now - since)}; nobody works that long",
                "vinci stop <minutes you really worked>"));

        foreach (var idea in s.Ideas.Where(i => i.Stage != Stage.Retired))
        {
            if (idea.Stage == Stage.Incubate)
            {
                if (now >= idea.IncubateUntil)
                    findings.Add(new(idea.Id, $"incubation is over: {idea.Title}", $"vinci advance {idea.Id}"));
                continue;
            }

            var idle = now - idea.LastTouched;
            var limit = idea.Stage == Stage.Tend ? TendingIdleLimit : BuildingIdleLimit;
            if (idle > limit)
                findings.Add(new(idea.Id, $"untouched for {Text.Span(idle)}: {idea.Title}",
                    $"work on it, vinci tend {idea.Id} \"...\", or vinci retire {idea.Id} \"why\""));
        }

        var linkedTo = s.Notes.SelectMany(n => n.Refs).ToHashSet();
        foreach (var note in s.Notes)
        {
            var linksOut = note.Refs.Exists(r => r[0] is 'n' or 'i');
            if (!linksOut && !linkedTo.Contains(note.Id))
                findings.Add(new(note.Id, $"note connects to nothing: {Text.Clip(note.Text, 50)}",
                    $"vinci link {note.Id} <note or idea>"));
        }

        foreach (var issue in s.Issues.Where(x => x.PrincipleId is null))
        {
            findings.Add(issue.RootCause is null
                ? new(issue.Id, $"no root cause yet: {Text.Clip(issue.Text, 50)}", $"vinci diagnose {issue.Id} \"root cause\"")
                : new(issue.Id, $"diagnosed but nothing changed: {Text.Clip(issue.Text, 50)}",
                    $"vinci principle \"what I do from now on\" {issue.Id}"));
        }

        foreach (var metric in s.Metrics.Where(m => m.Points.Count >= MinPointsForControl))
        {
            var values = metric.Points.Select(p => p.Value).ToList();
            var limits = Stats.ControlLimits(values)!;
            if (!limits.Holds(values[^1]))
                findings.Add(new(metric.Name,
                    $"out of control: last value {values[^1]:0.##} is outside {limits.Lower:0.##}..{limits.Upper:0.##}",
                    $"find the special cause, then vinci issue \"...\""));
        }

        return findings;
    }
}
