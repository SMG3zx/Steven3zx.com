using System.Diagnostics;
using System.Globalization;
using System.Text;
using System.Text.Json;

namespace Vinci;

/// A stretch of time one window had the focus while you were at the keyboard.
/// Window is the app and the title together.
public sealed record Activity(DateTimeOffset Start, TimeSpan Length, string Window);

/// What is known about a period. Through is how far the record reaches: the
/// watcher only learns you were present some minutes after the fact, so time
/// past Through is not yet known and must be asked for again later.
public sealed record Seen(IReadOnlyList<Activity> Activities, DateTimeOffset Through);

/// Every stretch of activity that overlaps the period, or null when the
/// record of activity cannot be reached.
public delegate Seen? ActivitySource(DateTimeOffset from, DateTimeOffset to);

/// One commit and the full paths of the files it changed.
public sealed record Commit(string Hash, DateTimeOffset At, string Subject, IReadOnlyList<string> Files);

/// The commits that changed something under a folder, oldest first: those
/// after a moment, or only the latest one when no moment is given.
public delegate IReadOnlyList<Commit> CommitSource(string folder, DateTimeOffset? after);

/// Writes down what the machine already knows, so nobody has to: time in
/// windows, commits, and use of what was shipped.
public static class Tracker
{
    public const string NothingWatched = "Nothing is watched. vinci home <idea> <folder>, or vinci watch <idea> \"text in its window titles\"";

    /// With nothing to credit, the place reached is still written down this
    /// often, so one sync never has to read months of activity.
    public static readonly TimeSpan MarkEvery = TimeSpan.FromDays(1);

    /// A glance at a window is not use. This much time in it, in one sync, is.
    public static readonly TimeSpan UseAtLeast = TimeSpan.FromMinutes(1);

    /// Returns what it recorded, one line each, for `vinci sync` to print.
    public static IReadOnlyList<string> Sync(Journal journal, ActivitySource? activity, CommitSource? commits, DateTimeOffset now)
    {
        var s = State.From(journal.Read());
        if (!s.AllWatches.Any()) return [NothingWatched];
        List<string> lines = [.. Windows(journal, s, activity, now), .. Commits(journal, s, commits)];
        return lines;
    }

    /// Credits the window time since the last sync: work becomes a "spent"
    /// fact dated when it ended, use becomes at most one "used" fact a day.
    private static List<string> Windows(Journal journal, State s, ActivitySource? source, DateTimeOffset now)
    {
        // The most specific watch wins when several match the same window.
        var watches = s.AllWatches.OrderByDescending(w => w.Pattern.Length).ToList();
        var from = s.SyncedTo ?? s.WatchingSince!.Value;
        if (source?.Invoke(from, now) is not { } seen)
            return ["ActivityWatch did not answer, so no window time was added. It will be picked up next time."];

        var to = seen.Through < now ? seen.Through : now;
        var credit = new Dictionary<Watch, (TimeSpan Total, DateTimeOffset Last)>();
        foreach (var a in seen.Activities)
        {
            var start = a.Start > from ? a.Start : from;
            var end = a.Start + a.Length < to ? a.Start + a.Length : to;
            if (end <= start) continue;
            if (watches.Find(w => a.Window.Contains(w.Pattern, StringComparison.OrdinalIgnoreCase)) is not { } watch) continue;
            // Several watches can stand for one idea; they share one total.
            var key = new Watch(watch.IdeaId, "", watch.Use);
            var (total, last) = credit.GetValueOrDefault(key, (TimeSpan.Zero, end));
            credit[key] = (total + (end - start), end > last ? end : last);
        }

        var lines = new List<string>();
        foreach (var (watch, (total, last)) in credit)
        {
            var idea = s.Idea(watch.IdeaId)!;
            if (watch.Use)
            {
                if (idea.Ships.Count == 0 || total < UseAtLeast) continue;
                if (idea.Uses.Exists(u => u.ToLocalTime().Date == last.ToLocalTime().Date)) continue;
                journal.Append(new(last, "used", idea.Id, $"{Text.Span(total)} on screen", Where: "window"));
                lines.Add($"{idea.Id,-4} used  {idea.Title}");
                continue;
            }
            var minutes = Math.Round(total.TotalMinutes, 2);
            if (minutes <= 0) continue;
            journal.Append(new(last, "spent", idea.Id, Value: minutes, Where: "window"));
            lines.Add($"{idea.Id,-4} +{Text.Span(total)}  {idea.Title}");
        }
        if (credit.Count > 0 || to - from > MarkEvery) journal.Append(new(to, "sync"));
        return lines.Count > 0 ? lines : [$"No watched window since {from.ToLocalTime():yyyy-MM-dd HH:mm}."];
    }

    /// Records each new commit under an idea's home as a "work" fact. An idea
    /// that has just been given a home gets its latest commit only, so the
    /// journal knows when it was last worked on without copying its history.
    private static List<string> Commits(Journal journal, State s, CommitSource? source)
    {
        var homed = s.Ideas.Where(i => i.Home is not null && i.Stage != Stage.Retired).ToList();
        if (source is null || homed.Count == 0) return [];

        var found = new List<(Idea Idea, Commit Commit)>();
        foreach (var idea in homed.Where(i => i.Work.Count == 0))
            found.AddRange(source(idea.Home!, null).Select(c => (idea, c)));

        // One question per repository covers every idea that already has work.
        var since = homed.SelectMany(i => i.Work).Select(w => (DateTimeOffset?)w.At).Max();
        if (since is not null)
            foreach (var root in homed.Where(i => i.Work.Count > 0).Select(i => RepositoryOf(i.Home!)).OfType<string>().Distinct())
                foreach (var commit in source(root, since))
                    // A commit belongs to the deepest home that holds one of its files.
                    foreach (var idea in commit.Files.Select(f => homed.Where(i => i.Work.Count > 0 && Inside(f, i.Home!)).MaxBy(i => i.Home!.Length)).OfType<Idea>().Distinct())
                        found.Add((idea, commit));

        var lines = new List<string>();
        foreach (var group in found.Where(f => !s.Commits.Contains((f.Idea.Id, f.Commit.Hash))).GroupBy(f => f.Idea))
        {
            foreach (var (idea, commit) in group.OrderBy(f => f.Commit.At))
                journal.Append(new(commit.At, "work", idea.Id, commit.Subject, Where: commit.Hash));
            lines.Add($"{group.Key.Id,-4} {group.Count()} commit{(group.Count() == 1 ? "" : "s")}  {group.Key.Title}");
        }
        return lines;
    }

    private static bool Inside(string file, string folder) =>
        file.StartsWith(folder + Path.DirectorySeparatorChar, StringComparison.Ordinal);

    /// The nearest folder at or above this one that holds a .git.
    public static string? RepositoryOf(string folder)
    {
        for (var dir = folder; dir is not null; dir = Path.GetDirectoryName(dir))
            if (Path.Exists(Path.Combine(dir, ".git"))) return dir;
        return null;
    }
}

/// Reads commits and tracked files by running git.
public static class Git
{
    /// Only tracked files are listed, so ignored and untracked ones never reach the page.
    public static IReadOnlyList<string> PortalFiles(string repository) =>
        [.. Run(repository, "ls-files", "-z", "--", "portal.json", "*/portal.json")
            .Split('\0', StringSplitOptions.RemoveEmptyEntries).Select(file => Path.Combine(repository, file))];

    private static string Run(string folder, params string[] args)
    {
        var start = new ProcessStartInfo("git") { RedirectStandardOutput = true, RedirectStandardError = true };
        start.ArgumentList.Add("-C");
        start.ArgumentList.Add(folder);
        foreach (var arg in args) start.ArgumentList.Add(arg);
        try
        {
            using var git = Process.Start(start)!;
            git.ErrorDataReceived += (_, _) => { };
            git.BeginErrorReadLine();
            var text = git.StandardOutput.ReadToEnd();
            git.WaitForExit();
            return git.ExitCode == 0 ? text : "";
        }
        catch (Exception e) when (e is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            // No git, or not a repository: there is simply nothing to read.
            return "";
        }
    }

    public static IReadOnlyList<Commit> Log(string folder, DateTimeOffset? after)
    {
        if (!Directory.Exists(folder)) return [];
        // \x01 marks the start of a commit; the changed files follow, one per line, relative to the folder.
        var text = Run(folder, "log", "--format=%x01%H%x09%cI%x09%s", "--name-only", "--relative", "--reverse",
            after is { } a ? $"--after={a.ToUnixTimeSeconds()}" : "-1", "--", ".");
        return [.. text.Split('\x01', StringSplitOptions.RemoveEmptyEntries).Select(block =>
        {
            var rows = block.Split('\n', StringSplitOptions.RemoveEmptyEntries);
            var head = rows[0].Split('\t', 3);
            return new Commit(head[0], DateTimeOffset.Parse(head[1], CultureInfo.InvariantCulture), head[2],
                [.. rows[1..].Select(file => Path.Combine(folder, file))]);
        })];
    }
}

/// Reads window activity from an ActivityWatch server (https://activitywatch.net).
public static class ActivityWatch
{
    /// The focused window, cut down to the time you were not away, and the
    /// presence record itself, whose end is how far the answer can be trusted.
    private static readonly string[] Query =
    [
        "window = flood(query_bucket(find_bucket(\"aw-watcher-window_\")));",
        "afk = flood(query_bucket(find_bucket(\"aw-watcher-afk_\")));",
        "present = filter_keyvals(afk, \"status\", [\"not-afk\"]);",
        "RETURN = {\"window\": filter_period_intersect(window, present), \"afk\": afk};",
    ];

    public static ActivitySource At(string server) => (from, to) =>
    {
        var body = JsonSerializer.Serialize(new Dictionary<string, string[]>
        {
            ["timeperiods"] = [$"{from.UtcDateTime:O}/{to.UtcDateTime:O}"],
            ["query"] = Query,
        });
        try
        {
            using var http = new HttpClient { Timeout = TimeSpan.FromSeconds(10) };
            using var request = new HttpRequestMessage(HttpMethod.Post, $"{server.TrimEnd('/')}/api/0/query/")
            {
                Content = new StringContent(body, Encoding.UTF8, "application/json"),
            };
            using var reply = http.Send(request);
            if (!reply.IsSuccessStatusCode) return null;
            using var doc = JsonDocument.Parse(reply.Content.ReadAsStream());
            var result = doc.RootElement[0];
            var through = from;
            foreach (var e in result.GetProperty("afk").EnumerateArray())
                if (End(e) is var end && end > through) through = end;
            return new Seen([.. result.GetProperty("window").EnumerateArray().Select(e =>
            {
                var data = e.GetProperty("data");
                return new Activity(e.GetProperty("timestamp").GetDateTimeOffset(), Length(e),
                    $"{data.GetProperty("app").GetString()} {data.GetProperty("title").GetString()}");
            })], through);
        }
        catch (Exception e) when (e is HttpRequestException or TaskCanceledException or JsonException
            or KeyNotFoundException or InvalidOperationException or FormatException or UriFormatException)
        {
            return null;
        }
    };

    private static TimeSpan Length(JsonElement e) => TimeSpan.FromSeconds(e.GetProperty("duration").GetDouble());

    private static DateTimeOffset End(JsonElement e) => e.GetProperty("timestamp").GetDateTimeOffset() + Length(e);
}
