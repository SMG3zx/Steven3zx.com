using System.Text.Json;
using System.Text.Json.Serialization;

namespace Vinci;

/// One fact, recorded once and never changed. Everything vinci knows is a
/// fold over these, so every fact carries when it was learned for free.
///
/// Id is the thing the event creates or acts on. Refs are other things it
/// points at. Ids carry their type in the first letter (i idea, r requirement,
/// n note, s source, x issue, p principle, h prediction), so Refs needs no
/// further shape.
public sealed record Event(
    DateTimeOffset At,
    string Kind,
    string? Id = null,
    string? Text = null,
    string[]? Refs = null,
    double? Value = null,
    double? Hi = null,
    string? Who = null,
    string? Why = null,
    string? Url = null,
    string? Metric = null,
    string[]? Tags = null,
    string? Where = null
);

/// The append-only file of events: one JSON object per line.
public sealed class Journal(string path)
{
    private static readonly JsonSerializerOptions Json = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    };

    public string Path => path;

    public IReadOnlyList<Event> Read() =>
        File.Exists(path)
            ?
            [
                .. File.ReadLines(path)
                    .Where(line => line.Length > 0)
                    .Select(line => JsonSerializer.Deserialize<Event>(line, Json)!),
            ]
            : [];

    public void Append(Event e)
    {
        Directory.CreateDirectory(
            System.IO.Path.GetDirectoryName(System.IO.Path.GetFullPath(path))!
        );
        File.AppendAllText(path, JsonSerializer.Serialize(e, Json) + "\n");
    }
}
