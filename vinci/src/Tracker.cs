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

/// Turns window activity into time on ideas, so nobody starts or stops a timer.
public static class Tracker
{
    /// With nothing to credit, the place reached is still written down this
    /// often, so one sync never has to read months of activity.
    public static readonly TimeSpan MarkEvery = TimeSpan.FromDays(1);

    /// Credits the activity since the last sync to the ideas that watch for
    /// it, as "spent" facts dated when the work ended. Returns what it did,
    /// one line each, for `vinci sync` to print.
    public static IReadOnlyList<string> Sync(Journal journal, ActivitySource? source, DateTimeOffset now)
    {
        var s = State.From(journal.Read());
        var watches = s.Watches.Where(w => s.Idea(w.IdeaId)!.Stage != Stage.Retired).ToList();
        if (watches.Count == 0) return ["Nothing is watched. vinci watch <idea> \"text in its window titles\""];

        var from = s.SyncedTo ?? s.WatchingSince!.Value;
        if (source?.Invoke(from, now) is not { } seen)
            return ["ActivityWatch did not answer, so no window time was added. It will be picked up next time."];

        var to = seen.Through < now ? seen.Through : now;
        var credit = new Dictionary<string, (TimeSpan Total, DateTimeOffset Last)>();
        foreach (var a in seen.Activities)
        {
            var start = a.Start > from ? a.Start : from;
            var end = a.Start + a.Length < to ? a.Start + a.Length : to;
            if (end <= start) continue;
            // The first watch recorded wins when two match the same window.
            if (watches.Find(w => a.Window.Contains(w.Pattern, StringComparison.OrdinalIgnoreCase)) is not { } watch) continue;
            var (total, last) = credit.GetValueOrDefault(watch.IdeaId, (TimeSpan.Zero, end));
            credit[watch.IdeaId] = (total + (end - start), end > last ? end : last);
        }

        var lines = new List<string>();
        foreach (var (ideaId, (total, last)) in credit)
        {
            var minutes = Math.Round(total.TotalMinutes, 2);
            if (minutes <= 0) continue;
            journal.Append(new(last, "spent", ideaId, Value: minutes, Where: "window"));
            lines.Add($"{ideaId,-4} +{Text.Span(total)}  {s.Idea(ideaId)!.Title}");
        }
        if (lines.Count > 0 || to - from > MarkEvery) journal.Append(new(to, "sync"));
        return lines.Count > 0 ? lines : [$"No watched window since {from.ToLocalTime():yyyy-MM-dd HH:mm}."];
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
