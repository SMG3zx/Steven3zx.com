using System.Net;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using Vinci;
using Vinci.Chat;

namespace Vinci.Tests;

public class ChatTests
{
    /// Answers each request in turn from a script, and records the bodies it was sent.
    private sealed class Script(params (string Path, string Json)[] replies) : HttpMessageHandler
    {
        private int next;
        public List<(string Path, string Body)> Requests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken stop
        )
        {
            var body = request.Content is null ? "" : await request.Content.ReadAsStringAsync(stop);
            Requests.Add((request.RequestUri!.AbsolutePath, body));
            var (path, json) =
                next < replies.Length
                    ? replies[next++]
                    : throw new InvalidOperationException(
                        $"unexpected request {request.RequestUri}"
                    );
            if (request.RequestUri!.AbsolutePath != path)
                throw new InvalidOperationException(
                    $"expected {path}, got {request.RequestUri.AbsolutePath}"
                );
            return new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent(json, Encoding.UTF8, "application/json"),
            };
        }
    }

    /// A service that is not running: every request fails to connect.
    private sealed class Refused : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken stop
        ) => throw new HttpRequestException("connection refused");
    }

    private static readonly DateTimeOffset Now = new(2026, 11, 15, 9, 0, 0, TimeSpan.Zero);

    private static string Reply(string content) =>
        JsonSerializer.Serialize(new { choices = new[] { new { message = new { role = "assistant", content } } } });

    [Fact]
    public async Task Chat_client_asks_for_no_thinking_and_returns_the_content()
    {
        var script = new Script(("/chat/completions", Reply("""{"tool": "pipeline"}""")));
        var content = await new OpenAiChat(
            "http://chat.test",
            "swift-qwen3.8-27b",
            think: false,
            script
        ).Send([new JsonObject { ["role"] = "user", ["content"] = "hi" }]);
        Assert.Equal("""{"tool": "pipeline"}""", content);
        var sent = JsonNode.Parse(script.Requests[0].Body)!;
        Assert.Equal("swift-qwen3.8-27b", sent["model"]!.GetValue<string>());
        Assert.False(sent["chat_template_kwargs"]!["enable_thinking"]!.GetValue<bool>());
        Assert.False(sent["stream"]!.GetValue<bool>());
        Assert.Null(sent["tools"]);
    }

    [Fact]
    public void A_json_action_is_read_from_plain_or_fenced_replies_and_prose_is_an_answer()
    {
        Assert.True(
            Conversation.TryAction(
                """{"tool": "journal", "args": ["idea", "Flying machine"]}""",
                out var name,
                out var args
            )
        );
        Assert.Equal("journal", name);
        Assert.Equal("Flying machine", args["args"]![1]!.GetValue<string>());
        Assert.False(args.ContainsKey("tool"));

        Assert.True(
            Conversation.TryAction(
                "```json\n{\"tool\": \"recall\", \"query\": \"gliders\"}\n```",
                out name,
                out args
            )
        );
        Assert.Equal("recall", name);
        Assert.Equal("gliders", args["query"]!.GetValue<string>());

        Assert.False(Conversation.TryAction("You have one idea.", out _, out _));
        Assert.False(Conversation.TryAction("""{"count": 3}""", out _, out _));
        Assert.False(Conversation.TryAction("{not json", out _, out _));
    }

    [Fact]
    public async Task Conversation_runs_a_tool_then_answers_and_records_the_exchange()
    {
        var script = new Script(
            ("/chat/completions", Reply("""{"tool": "journal", "args": ["pipeline"]}""")),
            ("/chat/completions", Reply("Seed has 1 idea [event:e1].")),
            ("/v1/conversations", """{"ok":true}""")
        );
        var memory = new MemoryApi("http://memory.test", null, script);
        var tools = new ChatTools(
            (args, output) =>
            {
                output.Write("Seed 1");
                return 0;
            },
            memory
        );
        var conversation = new Conversation(
            new OpenAiChat("http://chat.test", "m", false, script),
            tools,
            memory,
            "s1",
            () => Now
        );

        using var console = new StringWriter();
        var answer = await conversation.Turn("what is in the pipeline?", console);

        Assert.Equal("Seed has 1 idea [event:e1].", answer);
        // The second request carried the tool's result back to the model, as the user's next message.
        Assert.Contains("Result of journal:\\nSeed 1", script.Requests[1].Body);
        Assert.Contains("· journal", console.ToString());
        // The answer cites an id that recall never returned, so the turn says so.
        Assert.Contains("cites [event:e1], which recall did not return", console.ToString());
        // The question is written to memory under the session. The answer cites an unsourced id, so it is not kept.
        var recorded = JsonNode.Parse(script.Requests[2].Body)!;
        Assert.Equal("s1", recorded["sessionId"]!.GetValue<string>());
        Assert.Equal("user_message", recorded["messages"]![0]!["kind"]!.GetValue<string>());
        Assert.Single(recorded["messages"]!.AsArray());
        Assert.Contains("answer not saved", console.ToString());
    }

    [Fact]
    public async Task A_citation_recall_returned_is_not_flagged()
    {
        var script = new Script(
            ("/chat/completions", Reply("""{"tool": "recall", "query": "gliders"}""")),
            ("/v1/context", """{"context":"[event:e1] A glider wing tore | sources: s1"}"""),
            ("/chat/completions", Reply("A wing tore [event:e1].")),
            ("/v1/conversations", """{"ok":true}""")
        );
        var memory = new MemoryApi("http://memory.test", null, script);
        var conversation = new Conversation(
            new OpenAiChat("http://chat.test", "m", false, script),
            new ChatTools((a, o) => 0, memory),
            memory,
            "s3",
            () => Now
        );
        using var console = new StringWriter();
        Assert.Equal(
            "A wing tore [event:e1].",
            await conversation.Turn("what happened to the wing?", console)
        );
        Assert.DoesNotContain("did not return", console.ToString());
        // A grounded answer is kept: the exchange has both the question and the answer.
        var recorded = JsonNode.Parse(script.Requests[3].Body)!;
        Assert.Equal(2, recorded["messages"]!.AsArray().Count);
    }

    [Fact]
    public async Task Conversation_keeps_answering_when_memory_is_down_and_says_so()
    {
        var script = new Script(("/chat/completions", Reply("Noted.")));
        var memory = new MemoryApi("http://127.0.0.1:1", null, new Refused());
        var conversation = new Conversation(
            new OpenAiChat("http://chat.test", "m", false, script),
            new ChatTools((a, o) => 0, memory),
            memory,
            "s2",
            () => Now
        );
        using var console = new StringWriter();
        Assert.Equal("Noted.", await conversation.Turn("remember that I like wings", console));
        Assert.Contains("memory is not running", console.ToString());
    }

    [Fact]
    public void Remember_and_recall_report_a_missing_memory_service_as_text_not_as_a_crash()
    {
        var memory = new MemoryApi("http://127.0.0.1:1", null);
        var tools = new ChatTools((a, o) => 0, memory);
        var result = tools.Run("recall", new JsonObject { ["query"] = "anything" });
        Assert.StartsWith("error: memory service unavailable", result);
        Assert.Contains("vinci memory serve", result);
    }

    [Fact]
    public void Journal_tool_runs_the_same_commands_and_returns_their_errors()
    {
        var calls = new List<string[]>();
        var tools = new ChatTools(
            (args, output) =>
            {
                calls.Add(args);
                if (args[0] == "bad")
                {
                    output.Write("vinci: no such idea");
                    return 1;
                }
                output.Write("ok");
                return 0;
            },
            new MemoryApi("http://127.0.0.1:1", null)
        );
        Assert.Equal(
            "ok",
            tools.Run("journal", new JsonObject { ["args"] = new JsonArray("note", "x") })
        );
        Assert.Equal(
            "error: vinci: no such idea",
            tools.Run("journal", new JsonObject { ["args"] = new JsonArray("bad") })
        );
        Assert.Equal(["note", "x"], calls[0]);
        Assert.StartsWith(
            "error: there is no tool named delete_everything",
            tools.Run("delete_everything", new JsonObject())
        );
    }

    [Fact]
    public void Journal_facts_become_memory_events_that_name_their_ids_and_repeat_safely()
    {
        var note = new Event(
            Now,
            "note",
            "n1",
            Text: "A bird steers by shifting its weight",
            Tags: ["flight"]
        );
        var prediction = new Event(
            Now,
            "predict",
            "h1",
            Text: "A 10 m wing lifts 80 kg",
            Value: 70,
            Refs: ["i1"]
        );
        var issue = new Event(
            Now,
            "issue",
            "x1",
            Text: "Wing tore",
            Why: "cloth never load-tested"
        );

        Assert.Equal(
            "note n1 | A bird steers by shifting its weight | +flight",
            JournalSync.Describe(note)
        );
        Assert.Equal(
            "predict h1 | A 10 m wing lifts 80 kg | value 70 | refs i1",
            JournalSync.Describe(prediction)
        );
        Assert.Equal("artifact", JournalSync.Kind(note));
        Assert.Equal("decision", JournalSync.Kind(prediction));
        Assert.Equal("error", JournalSync.Kind(issue));

        var sent = JournalSync.ToMemoryEvent(note, 7);
        Assert.Equal(JournalSync.IdempotencyKey(note), sent["idempotencyKey"]!.GetValue<string>());
        Assert.NotEqual(
            JournalSync.IdempotencyKey(note),
            JournalSync.IdempotencyKey(note with { Text = "different" })
        );
        Assert.Equal("n1", sent["metadata"]!["journalId"]!.GetValue<string>());
    }

    [Fact]
    public async Task Journal_sync_sends_only_new_facts_and_moves_its_cursor()
    {
        var home = Path.Combine(Path.GetTempPath(), "vinci-sync-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(home);
        try
        {
            var journal = new Journal(Path.Combine(home, "journal.jsonl"));
            journal.Append(new Event(Now, "note", "n1", Text: "first"));
            journal.Append(new Event(Now, "note", "n2", Text: "second"));

            var script = new Script(
                ("/v1/events/batch", """{"ok":true}"""),
                ("/v1/events/batch", """{"ok":true}""")
            );
            var memory = new MemoryApi("http://memory.test", null, script);
            Assert.Equal(2, await JournalSync.Run(journal, memory));
            Assert.Equal(0, await JournalSync.Run(journal, memory));

            journal.Append(new Event(Now, "note", "n3", Text: "third"));
            var again = new Script(("/v1/events/batch", """{"ok":true}"""));
            Assert.Equal(
                1,
                await JournalSync.Run(journal, new MemoryApi("http://memory.test", null, again))
            );
            var body = JsonNode.Parse(again.Requests[0].Body)!["events"]!.AsArray();
            Assert.Single(body);
            Assert.Equal(
                JournalSync.IdempotencyKey(new Event(Now, "note", "n3", Text: "third")),
                body[0]!["idempotencyKey"]!.GetValue<string>()
            );
        }
        finally
        {
            Directory.Delete(home, recursive: true);
        }
    }
}
