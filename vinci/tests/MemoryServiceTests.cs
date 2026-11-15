using System.Net;
using System.Text;
using System.Text.Json.Nodes;
using Vinci.Memory;

namespace Vinci.Tests;

/// The audit log and the MCP tools need no database.
public sealed class AuditAndMcpTests
{
    [Fact]
    public void The_audit_log_records_nested_runs_and_redacts_credentials()
    {
        var audit = new AuditRegistry();
        var root = audit.Start("chat.turn", "chain", JsonNode.Parse("""{"prompt":"hello","apiKey":"sk-secret-value-123456"}"""));
        var child = audit.Start("memory.recall", "tool", JsonNode.Parse("""{"query":"hello"}"""), traceId: root.TraceId, parentRunId: root.Id);
        audit.Finish(child, JsonNode.Parse("""{"authorization":"Bearer secret-value-123456","note":"key-abcdefghijklmnop"}"""));
        audit.Finish(root, JsonNode.Parse("""{"answer":"hi"}"""));

        var trace = audit.Trace(root.TraceId);
        Assert.Equal(2, trace.Count);
        Assert.Null(trace[0].ParentRunId);
        Assert.Equal(root.Id, trace[1].ParentRunId);
        var kept = System.Text.Json.JsonSerializer.Serialize(trace);
        Assert.DoesNotContain("secret-value-123456", kept);
        Assert.DoesNotContain("abcdefghijklmnop", kept);
        Assert.All(trace, run => Assert.Equal("success", run.Status));
    }

    [Fact]
    public void The_audit_log_filters_recent_runs_and_redacts_by_key_name()
    {
        var audit = new AuditRegistry();
        audit.Fail(audit.Start("security.block", "security"), "blocked");
        audit.Finish(audit.Start("memory.recall", "retrieval"));
        Assert.Single(audit.Query(status: "error", kind: "security"));
        Assert.Equal("memory.recall", audit.Query(limit: 1)[0].Name);
        Assert.Single(audit.Query(name: "BLOCK"));
        Assert.Equal("""{"password":"[REDACTED]","nested":{"token":"[REDACTED]"}}""",
            AuditRegistry.Redact(JsonNode.Parse("""{"password":"do-not-store","nested":{"token":"also-private"}}"""))!.ToJsonString());
        audit.Clear();
        Assert.Equal(0, audit.Stats()["count"]!.GetValue<int>());
    }

    private sealed class Api(Func<HttpRequestMessage, string, (HttpStatusCode, string)> answer) : HttpMessageHandler
    {
        public List<(string Path, string Body, string? Auth)> Seen { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken stop)
        {
            var body = request.Content is null ? "" : await request.Content.ReadAsStringAsync(stop);
            Seen.Add((request.RequestUri!.AbsolutePath, body, request.Headers.Authorization?.ToString()));
            var (status, json) = answer(request, body);
            return new HttpResponseMessage(status) { Content = new StringContent(json, Encoding.UTF8, "application/json") };
        }
    }

    private static JsonObject Msg(string json) => (JsonObject)JsonNode.Parse(json)!;

    [Fact]
    public async Task The_mcp_server_lists_fifteen_tools_and_answers_the_handshake()
    {
        var mcp = new McpServer(new HttpClient(new Api((_, _) => (HttpStatusCode.OK, "{}"))), "http://memory.test", null);
        var init = await mcp.Handle(Msg("""{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}"""));
        Assert.Equal("2025-03-26", init!["result"]!["protocolVersion"]!.GetValue<string>());
        Assert.Null(await mcp.Handle(Msg("""{"jsonrpc":"2.0","method":"notifications/initialized"}""")));

        var list = await mcp.Handle(Msg("""{"jsonrpc":"2.0","id":2,"method":"tools/list"}"""));
        var tools = list!["result"]!["tools"]!.AsArray();
        Assert.Equal(15, tools.Count);
        Assert.Equal(McpServer.ToolNames, tools.Select(t => t!["name"]!.GetValue<string>()));
        Assert.Contains("recall_memory", McpServer.ToolNames);
        Assert.Equal("object", tools[0]!["inputSchema"]!["type"]!.GetValue<string>());

        var unknown = await mcp.Handle(Msg("""{"jsonrpc":"2.0","id":3,"method":"nope"}"""));
        Assert.Equal(-32601, unknown!["error"]!["code"]!.GetValue<int>());
    }

    [Fact]
    public async Task An_mcp_tool_call_is_a_call_to_the_memory_api_with_the_key()
    {
        var api = new Api((_, _) => (HttpStatusCode.Created, """{"id":"m1","traceId":"t1"}"""));
        var mcp = new McpServer(new HttpClient(api), "http://memory.test/", "key-1");
        var reply = await mcp.Handle(Msg("""{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"retract_memory","arguments":{"id":"a b/c","reason":"stale"}}}"""));
        Assert.Equal(("/v1/memories/a%20b%2Fc/retract", """{"reason":"stale"}""", "Bearer key-1"), api.Seen.Single());
        Assert.Contains("\"traceId\": \"t1\"", reply!["result"]!["content"]![0]!["text"]!.GetValue<string>());
        Assert.Null(reply["result"]!["isError"]);
    }

    [Fact]
    public async Task An_mcp_tool_that_fails_replies_with_the_reason_instead_of_crashing()
    {
        var mcp = new McpServer(new HttpClient(new Api((_, _) => (HttpStatusCode.BadRequest, """{"error":"query is required","traceId":"t9"}"""))), "http://memory.test", null);
        var failed = await mcp.Handle(Msg("""{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"recall_memory","arguments":{"query":"x"}}}"""));
        Assert.True(failed!["result"]!["isError"]!.GetValue<bool>());
        Assert.Equal("query is required (trace: t9)", failed["result"]!["content"]![0]!["text"]!.GetValue<string>());

        var missing = await mcp.Handle(Msg("""{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall_memory","arguments":{}}}"""));
        Assert.Contains("query is required", missing!["result"]!["content"]![0]!["text"]!.GetValue<string>());
        var unknown = await mcp.Handle(Msg("""{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"drop_everything"}}"""));
        Assert.Equal(-32602, unknown!["error"]!["code"]!.GetValue<int>());
    }

    [Fact]
    public async Task The_mcp_server_reads_lines_and_writes_one_answer_each()
    {
        var mcp = new McpServer(new HttpClient(new Api((_, _) => (HttpStatusCode.OK, "{}"))), "http://memory.test", null);
        var input = new StringReader("""
            {"jsonrpc":"2.0","id":1,"method":"ping"}
            not json
            {"jsonrpc":"2.0","method":"notifications/initialized"}
            {"jsonrpc":"2.0","id":2,"method":"ping"}

            """);
        var output = new StringWriter();
        await mcp.Run(input, output, CancellationToken.None);
        var lines = output.ToString().Split('\n', StringSplitOptions.RemoveEmptyEntries);
        Assert.Equal(3, lines.Length);
        Assert.Contains("-32700", lines[1]);
        Assert.Contains("\"id\":2", lines[2]);
    }
}

/// The REST service driven directly, against a real database.
[Collection("neo4j")]
public sealed class MemoryServiceTests : IAsyncLifetime
{
    private readonly string suffix = Guid.NewGuid().ToString();
    private Neo4jStore store = null!;
    private AuditRegistry audit = null!;
    private MetricsRegistry metrics = null!;
    private MemoryService service = null!;

    public async Task InitializeAsync()
    {
        store = new Neo4jStore();
        await store.EnsureSchema();
        (audit, metrics) = (new AuditRegistry(), new MetricsRegistry());
        service = new MemoryService(store, audit, metrics, new CodexAppServerClient(), new HeuristicConsolidator());
    }

    public async Task DisposeAsync() => await store.DisposeAsync();

    private async Task<Reply> Send(string method, string path, object? body = null, Dictionary<string, string>? query = null, string? auth = null, string? key = null) =>
        await service.Handle(new Call(method, path, query ?? [], auth, key, 0, body is null ? null : System.Text.Json.JsonSerializer.Serialize(body)));

    private static string Str(Reply reply, string key) => reply.Json![key]!.GetValue<string>();

    [Neo4jFact]
    public async Task Events_are_saved_once_and_replayed_by_idempotency_key()
    {
        var made = await Send("POST", "/v1/events", new { kind = "decision", content = $"Choose the memory port {suffix}", projectId = suffix, source = "test" }, key: $"key-{suffix}");
        Assert.Equal(201, made.Status);
        Assert.NotNull(made.Json!["traceId"]);
        var again = await Send("POST", "/v1/events", new { kind = "decision", content = $"Choose the memory port {suffix}", projectId = suffix, source = "test" }, key: $"key-{suffix}");
        Assert.Equal(Str(made, "id"), Str(again, "id"));
        var conflict = await Send("POST", "/v1/events", new { kind = "error", content = "different", projectId = suffix }, key: $"key-{suffix}");
        Assert.Equal(409, conflict.Status);

        Assert.Equal(400, (await Send("POST", "/v1/events", new { kind = "decision", content = "  " })).Status);
        var found = await Send("GET", $"/v1/events/key-{suffix}");
        Assert.Equal(200, found.Status);
        Assert.Equal(404, (await Send("GET", "/v1/events/none-" + suffix)).Status);
    }

    [Neo4jFact]
    public async Task A_batch_and_a_conversation_are_checked_before_anything_is_written()
    {
        Assert.Equal(400, (await Send("POST", "/v1/events/batch", new { events = Array.Empty<object>() })).Status);
        Assert.Equal(400, (await Send("POST", "/v1/events/batch", new { events = new[] { new { kind = "decision", content = "" } } })).Status);
        var batch = await Send("POST", "/v1/events/batch", new { events = new[] { new { kind = "decision", content = $"One {suffix}", projectId = suffix }, new { kind = "error", content = $"Two {suffix}", projectId = suffix } } });
        Assert.Equal(201, batch.Status);
        Assert.Equal(2, batch.Json!["count"]!.GetValue<int>());

        Assert.Equal(400, (await Send("POST", "/v1/conversations", new { sessionId = "", messages = new[] { new { kind = "user_message", content = "hi" } } })).Status);
        var session = $"conversation-{suffix}";
        var chat = await Send("POST", "/v1/conversations", new { sessionId = session, projectId = suffix, messages = new[] { new { kind = "user_message", content = $"Hello there {suffix}" } } });
        Assert.Equal(201, chat.Status);
        var event0 = chat.Json!["events"]![0]!;
        Assert.Equal("codex", event0["source"]!.GetValue<string>());
        Assert.Equal(session, (await store.GetSessionEvents(session))[0].SessionId);
        Assert.Equal("conversation", (await store.GetSessionEvents(session))[0].Metadata!["ingestion"]!.GetValue<string>());
    }

    [Neo4jFact]
    public async Task Recall_finds_evidence_reports_its_route_and_audits_every_step()
    {
        await Send("POST", "/v1/events", new { kind = "decision", content = $"Currently the memory service runs on port {suffix}", projectId = suffix, source = "test" });
        var recall = await Send("POST", "/v1/recall", new { query = $"Currently the memory service runs on port {suffix}", projectId = suffix, includeTestData = true });
        Assert.Equal(200, recall.Status);
        Assert.Equal("temporal", Str(recall, "route"));
        Assert.NotEmpty(recall.Json!["results"]!.AsArray());
        Assert.False(recall.Json["abstain"]!.GetValue<bool>());
        var trace = audit.Trace(Str(recall, "traceId"));
        Assert.Contains(trace, run => run.Name == "memory.recall" && run.Status == "success");
        Assert.Contains(trace, run => run.Name == "neo4j.recall" && run.ParentRunId == trace.First(r => r.Name == "memory.recall").Id);
        // The audit trail keeps how long the query was, never the query.
        Assert.DoesNotContain("Currently the memory service", System.Text.Json.JsonSerializer.Serialize(trace));
        Assert.True(metrics.Snapshot()["retrievals"]!["count"]!.GetValue<int>() >= 1);

        var nothing = await Send("POST", "/v1/recall", new { query = $"Zyx-not-real-{suffix}", projectId = suffix, includeTestData = true });
        Assert.True(nothing.Json!["abstain"]!.GetValue<bool>());
        Assert.NotNull(nothing.Json["reason"]);
        var bad = await Send("POST", "/v1/recall", new { query = "x", limit = 99 });
        Assert.Equal(400, bad.Status);
        Assert.Equal("limit must be an integer between 1 and 50", Str(bad, "error"));
        Assert.NotNull(bad.Json!["traceId"]);
        Assert.Equal(400, (await Send("POST", "/v1/recall", new { query = "x", retrievalMethods = new[] { "magic" } })).Status);
    }

    [Neo4jFact]
    public async Task A_context_pack_is_bounded_and_a_memory_can_be_corrected_retracted_and_archived()
    {
        var first = await Send("POST", "/v1/memories", new { id = $"mem-{suffix}", content = $"The pack limit is small {suffix}", category = "fact", projectId = suffix });
        Assert.Equal(201, first.Status);
        var correction = await Send("POST", "/v1/memories", new { id = $"mem2-{suffix}", content = $"The pack limit is large {suffix}", category = "fact", projectId = suffix, supersedesMemoryId = $"mem-{suffix}" });
        Assert.Equal(201, correction.Status);
        Assert.Contains(audit.Query(name: "memory.correct"), run => run.TraceId == Str(correction, "traceId"));
        var superseded = await Send("GET", "/v1/memories", query: new() { ["projectId"] = suffix, ["status"] = "superseded" });
        Assert.Contains(superseded.Json!["memories"]!.AsArray(), m => m!["id"]!.GetValue<string>() == $"mem-{suffix}");
        Assert.Equal(400, (await Send("GET", "/v1/memories", query: new() { ["status"] = "bogus" })).Status);

        var context = await Send("POST", "/v1/context", new { query = $"pack limit large {suffix}", projectId = suffix, includeTestData = true, maxChars = 300 });
        Assert.Equal(200, context.Status);
        Assert.True(context.Json!["citationLocked"]!.GetValue<bool>());
        Assert.True(context.Json["characterCount"]!.GetValue<int>() <= 300);
        Assert.Contains($"memory:mem2-{suffix}", context.Json["context"]!.GetValue<string>());
        Assert.Equal(400, (await Send("POST", "/v1/context", new { query = "x", maxChars = 10 })).Status);

        Assert.Equal(404, (await Send("POST", "/v1/memories/none-x/retract", new { })).Status);
        Assert.Equal("retracted", Str(await Send("POST", $"/v1/memories/mem2-{suffix}/retract", new { reason = "test" }), "status"));
        Assert.Equal(400, (await Send("POST", $"/v1/memories/mem-{suffix}/utility", new { useful = "yes" })).Status);
        var impact = await Send("GET", $"/v1/memories/mem2-{suffix}/impact");
        Assert.Equal("retracted", Str(impact, "status"));
        Assert.Equal("archived", Str(await Send("POST", $"/v1/memories/mem-{suffix}/archive", new { }), "status"));
    }

    [Neo4jFact]
    public async Task Answers_are_checked_for_citations_and_a_judge_can_be_required()
    {
        var results = new[] { new { type = "memory", id = "m1", content = "Neo4j is used." } };
        var good = await Send("POST", "/v1/answers/validate", new { answer = "Neo4j is used. [memory:m1]", results });
        Assert.Equal(200, good.Status);
        Assert.Equal("citation-only", good.Json!["faithfulness"]!["fallback"]!.GetValue<string>());
        Assert.Equal(422, (await Send("POST", "/v1/answers/validate", new { answer = "Neo4j is used.", results })).Status);
        Assert.Equal(422, (await Send("POST", "/v1/answers/validate", new { answer = "Neo4j is used. [memory:m1]", results, requireJudge = true })).Status);
        Assert.Equal(400, (await Send("POST", "/v1/answers/validate", new { answer = "x" })).Status);
        var rejected = metrics.Snapshot()["answerValidation"]!["rejected"]!.GetValue<int>();
        Assert.True(rejected >= 2);
    }

    [Neo4jFact]
    public async Task Other_documents_chunks_claims_and_procedures_need_their_required_fields()
    {
        Assert.Equal(400, (await Send("POST", "/v1/entities", new { name = "" })).Status);
        var entity = await Send("POST", "/v1/entities", new { id = $"e-{suffix}", name = $"Service entity {suffix}", type = "tool", projectId = suffix });
        Assert.Equal(201, entity.Status);
        Assert.Equal(400, (await Send("POST", "/v1/documents", new { title = "t" })).Status);
        var document = await Send("POST", "/v1/documents", new { id = $"d-{suffix}", title = "Doc", source = "test", projectId = suffix });
        Assert.Equal(201, document.Status);
        Assert.Equal(400, (await Send("POST", "/v1/chunks", new { documentId = $"d-{suffix}", content = "c", ordinal = -1 })).Status);
        Assert.Equal(201, (await Send("POST", "/v1/chunks", new { id = $"c-{suffix}", documentId = $"d-{suffix}", content = $"Chunk text {suffix}", ordinal = 0, projectId = suffix })).Status);
        Assert.Equal(400, (await Send("POST", "/v1/claims", new { statement = "s", predicate = "" })).Status);
        Assert.Equal(201, (await Send("POST", "/v1/claims", new { statement = $"A claim {suffix}", predicate = "USES", subjectEntityId = $"e-{suffix}", projectId = suffix })).Status);
        Assert.Equal(400, (await Send("POST", "/v1/procedures", new { title = "t", goal = "g", steps = Array.Empty<string>() })).Status);
        Assert.Equal(201, (await Send("POST", "/v1/procedures", new { title = "Run", goal = "Do it", steps = new[] { "one" }, projectId = suffix })).Status);
    }

    [Neo4jFact]
    public async Task Administration_needs_the_admin_key_and_dangerous_actions_need_confirmation()
    {
        using var admin = new Setting("MEMORY_ADMIN_KEY", "admin-" + suffix);
        using var api = new Setting("MEMORY_API_KEY", "api-" + suffix);
        Assert.Equal(401, (await Send("GET", "/v1/jobs")).Status);
        Assert.Equal(401, (await Send("GET", "/v1/memories", auth: "Bearer wrong")).Status);
        Assert.Equal(200, (await Send("GET", "/v1/memories", auth: $"Bearer api-{suffix}")).Status);
        Assert.Equal(403, (await Send("GET", "/v1/admin/stats", auth: $"Bearer api-{suffix}")).Status);
        Assert.Contains(audit.Query(kind: "security"), run => run.Name == "security.admin_authorization");
        var adminAuth = $"Bearer admin-{suffix}";
        Assert.Equal(200, (await Send("GET", "/v1/admin/stats", auth: adminAuth)).Status);
        Assert.Equal(200, (await Send("GET", "/v1/admin/schema", auth: adminAuth)).Status);
        Assert.True((await Send("GET", "/v1/admin/context-hubs", auth: adminAuth)).Json!["hubs"] is JsonArray);
        Assert.Equal(400, (await Send("GET", "/v1/jobs", query: new() { ["status"] = "weird" }, auth: adminAuth)).Status);
        Assert.Equal(200, (await Send("GET", "/v1/jobs", query: new() { ["status"] = "queued", ["limit"] = "5" }, auth: adminAuth)).Status);
        Assert.Equal(400, (await Send("POST", "/v1/admin/audit/clear", new { }, auth: adminAuth)).Status);
        Assert.True((await Send("POST", "/v1/admin/audit/clear", new { confirm = "clear-audit" }, auth: adminAuth)).Json!["cleared"]!.GetValue<bool>());
        // Pages are public; the API they call is not.
        Assert.Equal(200, (await Send("GET", "/memories")).Status);
    }

    [Neo4jFact]
    public async Task A_recall_trace_can_be_replayed_only_when_it_kept_a_query_and_a_job_can_be_previewed_and_decided()
    {
        using var admin = new Setting("MEMORY_ADMIN_KEY", "admin-" + suffix);
        var auth = $"Bearer admin-{suffix}";
        var recall = await Send("POST", "/v1/recall", new { query = "anything at all", projectId = suffix, includeTestData = true }, auth: auth);
        var replay = await Send("POST", $"/v1/admin/audit/traces/{Str(recall, "traceId")}", new { confirm = "replay" }, auth: auth);
        // The trail never holds the query, so a replay says why it cannot run.
        Assert.Equal(409, replay.Status);
        Assert.Equal(400, (await Send("POST", $"/v1/admin/audit/traces/{Str(recall, "traceId")}", new { }, auth: auth)).Status);

        var eventId = $"preview-{suffix}";
        await Send("POST", "/v1/events", new { id = eventId, kind = "decision", content = $"Remember this decision {suffix}", projectId = suffix, metadata = new { remember = true } }, auth: auth);
        var preview = await Send("POST", $"/v1/jobs/consolidate-event:{eventId}/preview", new { }, auth: auth);
        Assert.Equal(200, preview.Status);
        Assert.Single(preview.Json!["proposal"]!["memories"]!.AsArray());
        Assert.Equal(404, (await Send("POST", "/v1/jobs/no-such-job/preview", new { }, auth: auth)).Status);
        Assert.Equal("approved", Str(await Send("POST", $"/v1/jobs/consolidate-event:{eventId}/approve", new { }, auth: auth), "approvalStatus"));
        var rejected = await Send("POST", $"/v1/jobs/consolidate-event:{eventId}/reject", new { reason = "no" }, auth: auth);
        Assert.Equal("failed", Str(rejected, "status"));
        Assert.Equal("queued", Str(await Send("POST", $"/v1/jobs/consolidate-event:{eventId}/retry", new { }, auth: auth), "status"));
        Assert.Equal(404, (await Send("POST", "/v1/jobs/nope/retry", new { }, auth: auth)).Status);
    }

    [Neo4jFact]
    public async Task Pages_unknown_routes_and_oversized_bodies_are_answered_correctly()
    {
        foreach (var page in new[] { "/", "/chat", "/ingest", "/sessions", "/imports", "/memories", "/jobs", "/status", "/metrics" })
        {
            var reply = await Send("GET", page);
            Assert.Equal(200, reply.Status);
            Assert.Contains("<html", reply.Html);
        }
        Assert.DoesNotContain("777777777", (await Send("GET", "/imports")).Html);
        Assert.Contains("vinci memory restore", (await Send("GET", "/status")).Html);
        Assert.Equal(404, (await Send("GET", "/nowhere")).Status);
        Assert.Equal(200, (await Send("GET", "/health")).Status);
        using var small = new Setting("MEMORY_MAX_BODY_BYTES", "1000");
        var tiny = new MemoryService(store, audit, metrics, new CodexAppServerClient(), new HeuristicConsolidator());
        var big = await tiny.Handle(new Call("POST", "/v1/events", new Dictionary<string, string>(), null, null, 5000, new string('x', 5000)));
        Assert.Equal(413, big.Status);
        using var off = new Setting("CODEX_APP_SERVER_DISABLED", "1");
        Assert.Equal(503, (await Send("POST", "/v1/chat", new { message = "hi" })).Status);
        Assert.True((await Send("GET", "/v1/chat/auth")).Json!["disabled"]!.GetValue<bool>());
    }

    private sealed class Setting : IDisposable
    {
        private readonly string name;
        private readonly string? before;

        public Setting(string name, string value)
        {
            (this.name, before) = (name, Environment.GetEnvironmentVariable(name));
            Environment.SetEnvironmentVariable(name, value);
        }

        public void Dispose() => Environment.SetEnvironmentVariable(name, before);
    }
}
