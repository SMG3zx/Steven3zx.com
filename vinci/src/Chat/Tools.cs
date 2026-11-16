using System.Text.Json.Nodes;

namespace Vinci.Chat;

/// What the chat model can do. The tools do the work; the model chooses one
/// by writing its JSON action and turns the result into a sentence.
public sealed class ChatTools(Func<string[], TextWriter, int> journal, MemoryApi memory)
{
    /// A tool result is cut to this many characters before the model reads it.
    public const int ResultLimit = 4000;
    public static readonly string[] Categories =
    [
        "fact",
        "preference",
        "decision",
        "procedure",
        "episode",
    ];

    /// The tools as the model is told about them. No square brackets or quoted
    /// JSON inside the description text: the examples are in the actions below.
    public const string Instructions = """
        You have four tools. To use one, reply with only a JSON object and nothing else:
        {"tool": "journal", "args": ["idea", "Flying machine"]}
        {"tool": "recall", "query": "what did I say about gliders"}
        {"tool": "remember", "content": "The person prefers short answers", "category": "preference"}
        {"tool": "read", "path": "notes/glider.md"}
        {"tool": "read", "url": "https://example.org/article"}

        journal runs one vinci command. Its args are the command word, then its arguments, one item each.
        To list things, use the plural word: ideas, notes, sources, predictions, issues, principles. show takes one id, such as i1.
        Each call runs one command. To do two things, make two calls.
        The command words are: idea, for, goal, req, cut, advance, ideas, pipeline, next, show, commit, kept, missed, ship, used, tend, retire,
        note, link, notes, tags, source, sources, bookmark, bookmarks, unbookmark, predict, result, predictions, log, spec, stats,
        issue, diagnose, principle, issues, principles, review, adopt, home, watch, unwatch, watches, sync, spent, path.
        recall searches memory of past conversations, notes and sources, and returns lines that each start with a type and an id.
        remember saves a durable fact about the person. Its category is one of fact, preference, decision, procedure, episode.
        read saves a text file (.txt, .md) or a web page that the person named, so recall can find its passages. Use it only when the person gives you the path or the address. Each passage from recall comes with the title and source it came from, and vinci lists those sources under your answer.
        After a tool runs you will see its result. When you have what you need, reply in plain sentences instead of JSON.
        """;

    public string Run(string name, JsonObject arguments)
    {
        try
        {
            var text = name switch
            {
                "journal" => Journal(arguments),
                "recall" => Recall(arguments),
                "remember" => Remember(arguments),
                "read" => Read(arguments),
                _ => $"error: there is no tool named {name}. Use journal, recall, remember or read.",
            };
            return text.Length <= ResultLimit ? text : text[..ResultLimit] + "\n…(cut)";
        }
        catch (MemoryUnavailable e)
        {
            return $"error: {e.Message}";
        }
        catch (Exception e)
            when (e
                    is ArgumentException
                        or InvalidOperationException
                        or InvalidCastException
                        or FormatException
            )
        {
            return $"error: {e.Message}";
        }
    }

    private string Journal(JsonObject arguments)
    {
        var args = (
            arguments["args"] as JsonArray
            ?? throw new ArgumentException("args must be a list of strings")
        )
            .Select(a => a?.GetValue<string>() ?? "")
            .ToArray();
        if (args.Length == 0)
            args = ["help"];
        using var output = new StringWriter();
        var exit = journal(args, output);
        var text = output.ToString().TrimEnd();
        return exit == 0 ? (text.Length > 0 ? text : "done") : $"error: {text}";
    }

    private string Recall(JsonObject arguments)
    {
        var query = arguments["query"]?.GetValue<string>()
            ?? throw new ArgumentException("query is required");
        var reply = memory
            .Post(
                "/v1/context",
                new
                {
                    query,
                    limit = 8,
                    maxChars = 3000,
                }
            )
            .GetAwaiter()
            .GetResult();
        var context = reply?["context"]?.GetValue<string>() ?? "";
        return context.Length > 0 ? context : "No memory matches that.";
    }

    private static readonly HttpClient Web = new() { Timeout = TimeSpan.FromSeconds(30) };

    /// Saves a text file or a web page as a document, with its passages, so recall can find
    /// them. The document keeps its title, its path or address, and the date it was read.
    private string Read(JsonObject arguments)
    {
        var path = arguments["path"]?.GetValue<string>();
        var url = arguments["url"]?.GetValue<string>();
        if ((path is null) == (url is null))
            throw new ArgumentException("read needs either a path or a url, not both");

        string title,
            locator,
            text;
        if (path is not null)
        {
            var full = Path.GetFullPath(path);
            if (!Reading.IsTextFile(full))
                throw new ArgumentException("only .txt, .md and .markdown files can be read for now");
            if (!File.Exists(full))
                throw new ArgumentException($"there is no file at {full}");
            if (new FileInfo(full).Length > Reading.MaxBytes)
                throw new ArgumentException("the file is larger than 1 MB");
            (title, text, locator) = (Path.GetFileNameWithoutExtension(full), File.ReadAllText(full), full);
        }
        else
        {
            if (!Uri.TryCreate(url, UriKind.Absolute, out var uri) || uri.Scheme is not ("http" or "https"))
                throw new ArgumentException("the address must start with http:// or https://");
            using var response = Web.GetAsync(uri).GetAwaiter().GetResult();
            if (!response.IsSuccessStatusCode)
                throw new ArgumentException($"the page answered {(int)response.StatusCode}");
            var media = response.Content.Headers.ContentType?.MediaType ?? "";
            if (!media.StartsWith("text/", StringComparison.Ordinal))
                throw new ArgumentException($"the page is {media}, not text");
            var body = response.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            if (body.Length > Reading.MaxBytes)
                throw new ArgumentException("the page is larger than 1 MB");
            var isHtml = media == "text/html";
            text = isHtml ? Reading.FromHtml(body) : body;
            title = (isHtml ? Reading.HtmlTitle(body) : null) ?? uri.Host + uri.AbsolutePath;
            locator = uri.AbsoluteUri;
        }

        var passages = Reading.Passages(text);
        if (passages.Count == 0)
            throw new ArgumentException("there is no text in it");
        if (passages.Count > Reading.MaxPassages)
            throw new ArgumentException($"it has {passages.Count} passages; the limit is {Reading.MaxPassages}");

        var read = DateTime.UtcNow.ToString("yyyy-MM-dd");
        var document = memory
            .Post("/v1/documents", new { title, source = $"{locator} (read {read})" })
            .GetAwaiter()
            .GetResult();
        var id = document?["id"]?.GetValue<string>()
            ?? throw new MemoryUnavailable("memory did not return the document id");
        for (var i = 0; i < passages.Count; i++)
            memory
                .Post("/v1/chunks", new { documentId = id, content = passages[i], ordinal = i })
                .GetAwaiter()
                .GetResult();
        return $"read \"{title}\" from {locator} on {read}: {passages.Count} passages saved as document {id}. Ask recall about it to get the passages.";
    }

    private string Remember(JsonObject arguments)
    {
        var content = arguments["content"]?.GetValue<string>()
            ?? throw new ArgumentException("content is required");
        var category =
            arguments["category"]?.GetValue<string>() is { } c && Categories.Contains(c)
                ? c
                : "fact";
        var reply = memory
            .Post(
                "/v1/memories",
                new
                {
                    content,
                    category,
                    status = "active",
                    source = "vinci-chat",
                }
            )
            .GetAwaiter()
            .GetResult();
        return $"remembered {reply?["id"]?.GetValue<string>() ?? reply?["memory"]?["id"]?.GetValue<string>() ?? "(saved)"}";
    }
}
