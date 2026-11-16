using System.Text;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Vinci.Chat;

/// One conversation with the chat model. A turn is a loop: the model either
/// writes a tool action, which runs and whose result it reads next, or it
/// answers. The answer's citations are checked against the evidence the tools
/// returned this turn, and the exchange is written to memory so later
/// questions can recall it.
public sealed partial class Conversation(
    OpenAiChat model,
    ChatTools tools,
    MemoryApi memory,
    string sessionId,
    Func<DateTimeOffset> now
)
{
    public const int MaxRounds = 6;

    /// The part of the history sent each turn, in characters. The chat model runs with a 32k
    /// window (deploy/llm/llama-chat.service); the prefix that stays the same is cached by the
    /// server, so a longer history costs little per turn. Older turns go first.
    public static int HistoryChars => (int)Memory.Env.Number("CHAT_HISTORY_CHARS", 24_000);

    private readonly List<JsonObject> history = [];
    private int turn;
    private bool warnedMemoryOff;

    public static string SystemPrompt(DateTimeOffset today) =>
        $"""
            You are the assistant inside vinci, a personal system for ideas, notes, predictions, measurements and time.
            The person you are talking to keeps their knowledge, goals and work in it.

            {ChatTools.Instructions}

            How to work:
            - For anything about their ideas, notes, sources, goals, predictions, metrics, commits or time, use the journal. Never guess what it holds.
            - For questions about the past, use recall first. Answer only from what recall returned, and cite each fact as [type:id] exactly as it appears there.
            - Passages from documents carry a from field with their title and source. Cite them like any other fact. Do not write a sources list: vinci adds one from the from fields.
            - If recall finds nothing relevant, say you do not know. Do not fill the gap.
            - When the person states a durable fact or preference, save it with remember.
            - When asked to record or change something, do it with the journal, then say in one line what you recorded.
            - Be brief. Plain sentences. No headings unless asked.

            Today is {today:yyyy-MM-dd}.
            """;

    public async Task<string> Turn(string userText, TextWriter output)
    {
        turn++;
        history.Add(Message("user", userText));
        var evidence = new StringBuilder();
        string? answer = null;
        for (var round = 0; round < MaxRounds && answer is null; round++)
        {
            var reply = (
                await model.Send([Message("system", SystemPrompt(now())), .. Recent()])
            ).Trim();
            if (!TryAction(reply, out var name, out var arguments))
            {
                answer = reply;
                history.Add(Message("assistant", answer));
                break;
            }
            history.Add(Message("assistant", reply));
            var result = tools.Run(name, arguments);
            evidence.AppendLine(result);
            await output.WriteLineAsync($"  · {name} {Summary(arguments)}");
            history.Add(Message("user", $"Result of {name}:\n{result}"));
        }
        answer ??= "I could not reach an answer in the steps I had. Ask it more narrowly.";

        var citations = Citation()
            .Matches(answer)
            .Select(m => m.Groups[1].Value)
            .Distinct()
            .ToList();
        var unsourced = citations
            .Where(c => !evidence.ToString().Contains($"[{c}]", StringComparison.Ordinal))
            .ToList();
        foreach (var cited in unsourced)
            await output.WriteLineAsync(
                $"  ! cites [{cited}], which recall did not return this turn"
            );

        // A passage from a document is cited with where it came from. The sources are read from
        // the evidence the model saw and listed by vinci, never written by the model.
        var origins = Origins(evidence.ToString());
        var unattributed = citations
            .Where(c => c.StartsWith("chunk:", StringComparison.Ordinal) && !origins.ContainsKey(c))
            .ToList();
        foreach (var cited in unattributed)
            await output.WriteLineAsync(
                $"  ! cites [{cited}], a passage with no source. The answer is not saved."
            );
        var listed = citations
            .Where(origins.ContainsKey)
            .Select(c => origins[c])
            .Distinct()
            .ToList();
        if (listed.Count > 0)
            answer += "\n\nSources:\n" + string.Join("\n", listed.Select(l => $"- {l}"));

        // An answer is stored only when it rests on recalled evidence, and every passage it cites
        // has a source. Otherwise the chat's own "I do not know" or wrong answer would be recalled
        // later as if it were a fact.
        var keepAnswer = citations.Count > 0 && unsourced.Count == 0 && unattributed.Count == 0;
        var saved = await Record(userText, keepAnswer ? answer : null);
        if (!keepAnswer && saved)
            await output.WriteLineAsync("  · answer not saved: it cites no recalled evidence");
        if (!saved && !warnedMemoryOff)
        {
            warnedMemoryOff = true;
            await output.WriteLineAsync(
                "  ! memory is not running, so exchanges are not saved (this is the only time it is said)"
            );
        }
        return answer;
    }

    /// A reply that is one JSON object with a "tool" name is an action. Anything else is the answer.
    public static bool TryAction(string reply, out string name, out JsonObject arguments)
    {
        name = "";
        arguments = [];
        var text = reply.Trim();
        if (text.StartsWith("```", StringComparison.Ordinal))
        {
            text = text.Trim('`').Trim();
            if (text.StartsWith("json", StringComparison.OrdinalIgnoreCase))
                text = text[4..].Trim();
        }
        if (!text.StartsWith('{'))
            return false;
        try
        {
            if (
                JsonNode.Parse(text) is not JsonObject obj
                || obj["tool"] is not JsonValue tool
                || !tool.TryGetValue<string>(out var toolName)
            )
                return false;
            name = toolName;
            obj.Remove("tool");
            arguments = obj;
            return true;
        }
        catch (System.Text.Json.JsonException)
        {
            return false;
        }
    }

    /// Writes the question, and the answer when it is kept, to memory. Returns false when the memory service is down.
    private async Task<bool> Record(string userText, string? answer)
    {
        var at = now().ToString("o");
        var messages = new List<object>
        {
            new
            {
                idempotencyKey = $"{sessionId}:{turn}:user",
                kind = "user_message",
                content = userText,
                source = "vinci-chat",
                occurredAt = at,
            },
        };
        if (answer is not null)
            messages.Add(
                new
                {
                    idempotencyKey = $"{sessionId}:{turn}:agent",
                    kind = "agent_message",
                    content = answer,
                    source = "vinci-chat",
                    occurredAt = at,
                }
            );
        try
        {
            await memory.Post("/v1/conversations", new { sessionId, messages });
            return true;
        }
        catch (MemoryUnavailable)
        {
            return false;
        }
    }

    /// The newest messages that fit the history budget, starting on a user message.
    private List<JsonObject> Recent()
    {
        var kept = new List<JsonObject>();
        var size = 0;
        for (var i = history.Count - 1; i >= 0; i--)
        {
            var length = (history[i]["content"]?.GetValue<string>().Length ?? 0) + 40;
            if (size + length > HistoryChars)
                break;
            size += length;
            kept.Insert(0, history[i]);
        }
        while (kept.Count > 0 && kept[0]["role"]?.GetValue<string>() != "user")
            kept.RemoveAt(0);
        return kept;
    }

    private static JsonObject Message(string role, string content) =>
        new() { ["role"] = role, ["content"] = content };

    private static string Summary(JsonObject arguments)
    {
        var text = arguments.ToJsonString();
        return text.Length <= 90 ? text : text[..89] + "…";
    }

    [GeneratedRegex(@"\[([a-z_]+:[^\]\s]+)\]")]
    private static partial Regex Citation();

    [GeneratedRegex("""\[(chunk:[^\]\s]+)\][^\n]*?\| from: "([^"\n]*)" <([^>\n]*)>""")]
    private static partial Regex Origin();

    /// Each passage the model was shown, with its title and source, by its citation.
    public static Dictionary<string, string> Origins(string evidence)
    {
        var found = new Dictionary<string, string>();
        foreach (Match m in Origin().Matches(evidence))
            found.TryAdd(m.Groups[1].Value, $"\"{m.Groups[2].Value}\", {m.Groups[3].Value}");
        return found;
    }
}
