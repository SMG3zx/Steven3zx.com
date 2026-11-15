using System.Globalization;

namespace Vinci.Chat;

/// `vinci` and `vinci chat`: the conversation with the chat model, which is
/// how everything else is done. The journal, memory and the rest are tools it calls.
public static class ChatCommand
{
    public const string DefaultModel = "qwen3-8b-vinci";
    public const string DefaultBaseUrl = "http://127.0.0.1:11434";

    public static async Task<int> Run(Journal journal, Func<string[], TextWriter, int> runJournal, TextReader input, TextWriter output, TextWriter errors, Func<DateTimeOffset> now)
    {
        var baseUrl = Environment.GetEnvironmentVariable("CHAT_BASE_URL") ?? DefaultBaseUrl;
        var model = new OllamaChat(baseUrl, Environment.GetEnvironmentVariable("CHAT_MODEL") ?? DefaultModel,
            think: Environment.GetEnvironmentVariable("CHAT_THINK") == "1");
        var memory = MemoryApi.FromEnvironment();
        var tools = new ChatTools(runJournal, memory);
        var conversation = new Conversation(model, tools, memory, $"vinci-chat-{now():yyyyMMddHHmmss}", now);

        try
        {
            var sent = await JournalSync.Run(journal, memory);
            if (sent > 0) await output.WriteLineAsync($"Recorded {sent} journal facts in memory.");
        }
        catch (MemoryUnavailable e)
        {
            await output.WriteLineAsync($"Memory is off: {e.Message}");
        }

        await output.WriteLineAsync($"vinci · chat model {model.Model} at {baseUrl} · /help for commands, /exit to leave");
        runJournal(["pipeline"], output);
        await output.WriteLineAsync();

        while (true)
        {
            await output.WriteAsync("you> ");
            var line = (await input.ReadLineAsync())?.Trim();
            if (line is null or "/exit" or "/quit") break;
            if (line.Length == 0) continue;
            if (line == "/help")
            {
                await output.WriteLineAsync("Ask anything, or tell it what to do. It uses the journal, memory and the model's own answers.");
                await output.WriteLineAsync("/pipeline  show the stages      /exit  leave");
                continue;
            }
            if (line == "/pipeline")
            {
                runJournal(["pipeline"], output);
                continue;
            }
            try
            {
                var answer = await conversation.Turn(line, output);
                await output.WriteLineAsync();
                await output.WriteLineAsync(answer);
                await output.WriteLineAsync();
            }
            catch (Exception e) when (e is InvalidOperationException or HttpRequestException or TaskCanceledException)
            {
                await errors.WriteLineAsync($"vinci: {e.Message}");
                await errors.WriteLineAsync($"The chat model is at {baseUrl}. Start it with: ollama serve, or set CHAT_BASE_URL.");
                return 1;
            }
        }
        return 0;
    }
}
