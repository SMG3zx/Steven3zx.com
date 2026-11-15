using System.Text.Json.Nodes;

namespace Vinci.Chat;

/// What the chat model can do. The tools do the work; the model chooses one
/// by writing its JSON action and turns the result into a sentence.
public sealed class ChatTools(Func<string[], TextWriter, int> journal, MemoryApi memory)
{
    /// A tool result is cut to this many characters before the model reads it.
    public const int ResultLimit = 4000;
    public static readonly string[] Categories = ["fact", "preference", "decision", "procedure", "episode"];

    /// The tools as the model is told about them. No square brackets or quoted
    /// JSON inside the description text: the examples are in the actions below.
    public const string Instructions = """
        You have three tools. To use one, reply with only a JSON object and nothing else:
        {"tool": "journal", "args": ["idea", "Flying machine"]}
        {"tool": "recall", "query": "what did I say about gliders"}
        {"tool": "remember", "content": "The person prefers short answers", "category": "preference"}

        journal runs one vinci command. Its args are the command word, then its arguments, one item each.
        To list things, use the plural word: ideas, notes, sources, predictions, issues, principles. show takes one id, such as i1.
        The command words are: idea, for, goal, req, cut, advance, ideas, pipeline, next, show, commit, kept, missed, ship, used, tend, retire,
        note, link, notes, tags, source, sources, bookmark, bookmarks, unbookmark, predict, result, predictions, log, spec, stats,
        issue, diagnose, principle, issues, principles, review, adopt, home, watch, unwatch, watches, sync, spent, path.
        recall searches memory of past conversations, notes and sources, and returns lines that each start with a type and an id.
        remember saves a durable fact about the person. Its category is one of fact, preference, decision, procedure, episode.
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
                _ => $"error: there is no tool named {name}. Use journal, recall or remember.",
            };
            return text.Length <= ResultLimit ? text : text[..ResultLimit] + "\n…(cut)";
        }
        catch (MemoryUnavailable e)
        {
            return $"error: {e.Message}";
        }
        catch (Exception e) when (e is ArgumentException or InvalidOperationException or InvalidCastException or FormatException)
        {
            return $"error: {e.Message}";
        }
    }

    private string Journal(JsonObject arguments)
    {
        var args = (arguments["args"] as JsonArray ?? throw new ArgumentException("args must be a list of strings"))
            .Select(a => a?.GetValue<string>() ?? "").ToArray();
        if (args.Length == 0) args = ["help"];
        using var output = new StringWriter();
        var exit = journal(args, output);
        var text = output.ToString().TrimEnd();
        return exit == 0 ? (text.Length > 0 ? text : "done") : $"error: {text}";
    }

    private string Recall(JsonObject arguments)
    {
        var query = arguments["query"]?.GetValue<string>() ?? throw new ArgumentException("query is required");
        var reply = memory.Post("/v1/context", new { query, limit = 8, maxChars = 3000 }).GetAwaiter().GetResult();
        var context = reply?["context"]?.GetValue<string>() ?? "";
        return context.Length > 0 ? context : "No memory matches that.";
    }

    private string Remember(JsonObject arguments)
    {
        var content = arguments["content"]?.GetValue<string>() ?? throw new ArgumentException("content is required");
        var category = arguments["category"]?.GetValue<string>() is { } c && Categories.Contains(c) ? c : "fact";
        var reply = memory.Post("/v1/memories", new { content, category, status = "active", source = "vinci-chat" }).GetAwaiter().GetResult();
        return $"remembered {reply?["id"]?.GetValue<string>() ?? reply?["memory"]?["id"]?.GetValue<string>() ?? "(saved)"}";
    }
}
