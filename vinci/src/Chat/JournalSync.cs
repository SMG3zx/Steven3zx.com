using System.Text.Json.Nodes;

namespace Vinci.Chat;

/// Sends the journal's new facts to memory, so the chat can recall them with
/// their journal ids. Each fact is sent once: the cursor records how many have
/// gone, and every event carries an idempotency key, so a lost cursor only
/// costs a resend that the memory service ignores.
public static class JournalSync
{
    public const int BatchSize = 50;

    private static readonly HashSet<string> Decisions =
    [
        "predict",
        "result",
        "principle",
        "commit",
        "kept",
        "missed",
        "cut",
        "diagnose",
        "goal",
        "req",
        "advance",
    ];

    public static string Kind(Event e) =>
        e.Kind switch
        {
            "issue" => "error",
            var k when Decisions.Contains(k) => "decision",
            _ => "artifact",
        };

    /// One line that says what the fact is, with its id, so a recalled line can be looked up with the journal.
    public static string Describe(Event e)
    {
        var parts = new List<string> { e.Id is null ? e.Kind : $"{e.Kind} {e.Id}" };
        if (e.Text is { Length: > 0 } text)
            parts.Add(text);
        if (e.Metric is not null)
            parts.Add($"metric {e.Metric} = {e.Value}{(e.Hi is null ? "" : $" to {e.Hi}")}");
        else if (e.Value is not null)
            parts.Add($"value {e.Value}");
        if (e.Refs is { Length: > 0 } refs)
            parts.Add($"refs {string.Join(", ", refs)}");
        if (e.Who is not null)
            parts.Add($"who {e.Who}");
        if (e.Why is not null)
            parts.Add($"why {e.Why}");
        if (e.Url is not null)
            parts.Add(e.Url);
        if (e.Where is not null)
            parts.Add($"where {e.Where}");
        if (e.Tags is { Length: > 0 } tags)
            parts.Add(string.Join(" ", tags.Select(t => t.StartsWith('+') ? t : "+" + t)));
        return string.Join(" | ", parts);
    }

    /// The key is a hash of what the fact says, not its position. A position is unique only within
    /// one journal file: two journals (or a test journal) would send different facts under one key,
    /// and memory refuses the whole batch when a key is reused with different content.
    public static string IdempotencyKey(Event e) =>
        "vinci-journal:"
        + Convert.ToHexStringLower(
            System.Security.Cryptography.SHA256.HashData(
                System.Text.Encoding.UTF8.GetBytes(System.Text.Json.JsonSerializer.Serialize(e))
            )
        )[..32];

    public static JsonObject ToMemoryEvent(Event e, int index) =>
        new()
        {
            ["kind"] = Kind(e),
            ["content"] = Describe(e),
            ["source"] = "vinci-journal",
            ["occurredAt"] = e.At.ToString("o"),
            ["idempotencyKey"] = IdempotencyKey(e),
            ["metadata"] = new JsonObject
            {
                ["journalKind"] = e.Kind,
                ["journalId"] = e.Id,
                ["journalIndex"] = index,
            },
        };

    /// Sends what is new since the cursor. Returns how many facts were sent.
    public static async Task<int> Run(Journal journal, MemoryApi memory)
    {
        var events = journal.Read();
        var cursorPath = Path.Combine(
            Path.GetDirectoryName(Path.GetFullPath(journal.Path))!,
            "memory-sync.txt"
        );
        var cursor = ReadCursor(cursorPath);
        if (cursor > events.Count)
            cursor = 0; // the journal was replaced: send it again, the keys make that safe
        var sent = 0;
        while (cursor < events.Count)
        {
            var count = Math.Min(BatchSize, events.Count - cursor);
            var batch = Enumerable
                .Range(cursor, count)
                .Select(i => (JsonNode)ToMemoryEvent(events[i], i))
                .ToArray();
            await memory.Post(
                "/v1/events/batch",
                new JsonObject { ["events"] = new JsonArray(batch) }
            );
            cursor += count;
            sent += count;
            File.WriteAllText(
                cursorPath,
                cursor.ToString(System.Globalization.CultureInfo.InvariantCulture)
            );
        }
        return sent;
    }

    private static int ReadCursor(string path) =>
        File.Exists(path) && int.TryParse(File.ReadAllText(path).Trim(), out var n) && n >= 0
            ? n
            : 0;
}
