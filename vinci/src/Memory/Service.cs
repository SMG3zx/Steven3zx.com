using System.Diagnostics;
using System.Globalization;
using System.Net;
using System.Reflection;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Vinci.Memory;

/// One request, already separated from the socket it came in on.
public sealed record Call(
    string Method,
    string Path,
    IReadOnlyDictionary<string, string> Query,
    string? Authorization = null,
    string? IdempotencyKey = null,
    long ContentLength = 0,
    string? Body = null);

/// An answer: JSON, or a web page.
public sealed record Reply(int Status, JsonNode? Json = null, string? Html = null);

/// A recall that failed, with the audit trace that says how far it got.
public sealed class RecallException(string message, string traceId) : Exception(message)
{
    public string TraceId => traceId;
}

/// The memory service's API: every route, its checks, and its audit trail.
/// It knows nothing about sockets, so it can be driven directly.
public sealed partial class MemoryService(
    Neo4jStore store,
    AuditRegistry audit,
    MetricsRegistry metrics,
    CodexAppServerClient codex,
    IConsolidator consolidator)
{
    private static readonly string[] Pages = ["/", "/chat", "/ingest", "/sessions", "/imports", "/memories", "/jobs", "/status", "/metrics"];
    private static readonly string[] JobStatuses = ["queued", "running", "completed", "failed"];

    private readonly long maxBodyBytes = (long)Env.Number("MEMORY_MAX_BODY_BYTES", 4 * 1024 * 1024);

    [GeneratedRegex("(tool|command|mcp|shell|function)", RegexOptions.IgnoreCase)]
    private static partial Regex ToolWords();

    // ---- small helpers ----

    private static Reply Ok(JsonNode? data, int status = 200) => new(status, data);

    private static Reply Error(int status, string message, string? traceId = null)
    {
        var body = new JsonObject { ["error"] = message };
        if (traceId is not null) body["traceId"] = traceId;
        return new(status, body);
    }

    private static JsonObject With(JsonObject source, string key, JsonNode? value)
    {
        var copy = Json.Clone(source);
        copy[key] = value;
        return copy;
    }

    private static JsonObject Obj(params (string Key, JsonNode? Value)[] fields) =>
        new(fields.Select(field => KeyValuePair.Create(field.Key, field.Value)));

    private static JsonNode? Copy(JsonNode? node) => node?.DeepClone();

    /// The body as an object. Anything else, or nothing, is an empty object
    /// when lenient and an error when not.
    private static JsonObject Body(Call call, bool lenient = false)
    {
        try
        {
            return JsonNode.Parse(call.Body ?? "") as JsonObject ?? (lenient ? [] : throw new JsonException("request body must be a JSON object"));
        }
        catch (JsonException) when (lenient)
        {
            return [];
        }
    }

    /// Text that is present and not blank, or null.
    private static string? Text(JsonObject body, string key) =>
        body[key]?.GetValueKind() == JsonValueKind.String && body[key]!.GetValue<string>() is { } text && text.Trim().Length > 0 ? text : null;

    private static T As<T>(JsonObject body) => body.Deserialize<T>(Json.Options)!;

    private static JsonNode? Node<T>(T value) => JsonSerializer.SerializeToNode(value, Json.Options);

    private static string? Param(Call call, string key) => call.Query.TryGetValue(key, out var value) && value.Length > 0 ? value : null;

    private static double NumberParam(Call call, string key, double fallback) =>
        Param(call, key) is { } raw && double.TryParse(raw, NumberStyles.Float, CultureInfo.InvariantCulture, out var value) ? value : fallback;

    private static bool IsInteger(JsonNode? node, double min, double max) =>
        node?.GetValueKind() == JsonValueKind.Number && Json.Num(node) is var value && value == Math.Floor(value) && value >= min && value <= max;

    /// The id in a path like /v1/memories/{id}/retract, or null when the path is another route.
    private static string? IdIn(string path, string prefix, string suffix = "")
    {
        if (!path.StartsWith(prefix, StringComparison.Ordinal) || !path.EndsWith(suffix, StringComparison.Ordinal)) return null;
        var id = path[prefix.Length..^suffix.Length];
        return id.Length > 0 && !id.Contains('/') ? Uri.UnescapeDataString(id) : null;
    }

    /// Too large is 413 and a clash with an earlier event is 409; the rest is the server's fault.
    private static int IngestionErrorStatus(Exception error) => error switch
    {
        ContentTooLargeException => 413,
        _ when error.Message.StartsWith("idempotency conflict", StringComparison.Ordinal) => 409,
        _ => 500,
    };

    // ---- authorization ----

    private static bool Authorized(Call call)
    {
        var (apiKey, adminKey) = (Env.Get("MEMORY_API_KEY"), Env.Get("MEMORY_ADMIN_KEY"));
        return (apiKey is null && adminKey is null)
            || (apiKey is not null && call.Authorization == $"Bearer {apiKey}")
            || (adminKey is not null && call.Authorization == $"Bearer {adminKey}");
    }

    private static bool AdminAuthorized(Call call)
    {
        var expected = Env.Get("MEMORY_ADMIN_KEY") ?? Env.Get("MEMORY_API_KEY");
        return expected is null ? !MemoryServer.Production : call.Authorization == $"Bearer {expected}";
    }

    private Reply AdminAuthorizationFailure(Call call)
    {
        var run = audit.Start("security.admin_authorization", "security", Obj(("method", call.Method), ("path", call.Path)),
            Obj(("reason", "admin authorization required")), ["security", "auth", "admin"]);
        audit.Fail(run, "admin authorization required");
        return Error(403, "admin authorization required", run.TraceId);
    }

    // ---- recall ----

    private static JsonObject RedactedRecallInput(JsonObject body, int? queryChars = null) => Obj(
        ("queryChars", queryChars ?? Json.Str(body["query"]).Length), ("userId", Copy(body["userId"])), ("projectId", Copy(body["projectId"])),
        ("sessionId", Copy(body["sessionId"])), ("limit", Copy(body["limit"])), ("includeHistorical", Copy(body["includeHistorical"])),
        ("includeTestData", Copy(body["includeTestData"])), ("retrievalMethods", Copy(body["retrievalMethods"])));

    /// Looks a question up, tries once more with only its meaningful words
    /// when the first attempt is weak, and says whether there is enough to
    /// answer from. Every step is in the audit trail under one trace.
    public async Task<JsonObject> Retrieve(JsonObject body, AuditRun? parent = null)
    {
        var watch = Stopwatch.StartNew();
        var trace = audit.Start("memory.recall", "retrieval", RedactedRecallInput(body), Obj(("component", "personal-memory")),
            ["memory", "retrieval"], parent?.TraceId, parent?.Id);
        var (ok, route) = (false, "semantic");
        List<string> methods = body["retrievalMethods"] is JsonArray chosen ? Json.Strings(chosen) : [.. Vocabulary.RetrievalMethods];
        try
        {
            if (Text(body, "query") is not { } query) throw new ArgumentException("query is required");
            if (body["limit"] is not null && !IsInteger(body["limit"], 1, 50)) throw new ArgumentException("limit must be an integer between 1 and 50");
            if (body["retrievalMethods"] is not null
                && (body["retrievalMethods"] is not JsonArray { Count: > 0 } || methods.Any(method => !Vocabulary.RetrievalMethods.Contains(method))))
                throw new ArgumentException("retrievalMethods must contain lexical, vector, or graph");
            route = Retrieval.RouteQuery(query);
            var plan = Retrieval.Plan(query, route);
            var scope = new MemoryScope
            {
                UserId = Text(body, "userId"), ProjectId = Text(body, "projectId"), SessionId = Text(body, "sessionId"),
                IncludeHistorical = body["includeHistorical"] is { } historical ? Json.Flag(historical) : route == "temporal",
                IncludeTestData = body["includeTestData"] is { } test ? Json.Flag(test) : null,
                RetrievalMethods = body["retrievalMethods"] is null ? null : methods,
            };
            var limit = body["limit"] is null ? 12 : (int)Json.Num(body["limit"]);

            async Task<List<JsonObject>> Look(string name, string question, JsonObject metadata)
            {
                var run = audit.Start(name, "tool", Obj(("queryChars", question.Length), ("scope", RedactedRecallInput(body, question.Length)), ("limit", limit), ("route", route)),
                    metadata, traceId: trace.TraceId, parentRunId: trace.Id);
                try
                {
                    var found = await store.Recall(question, scope, limit, route);
                    audit.Finish(run, Obj(("count", found.Count), ("ids", new JsonArray([.. found.Where(r => r["id"] is not null).Select(r => Copy(r["id"]))]))));
                    return found;
                }
                catch (Exception e)
                {
                    audit.Fail(run, e.Message);
                    throw;
                }
            }

            var results = await Look("neo4j.recall", query, Obj(("retrievalMethods", new JsonArray([.. methods.Select(m => (JsonNode)m)]))));
            string? correctionQuery = null;
            if (Retrieval.AssessEvidence(results).NeedsCorrection && (correctionQuery = Retrieval.CorrectiveQuery(query)) is not null)
            {
                var corrected = await Look("neo4j.corrective_recall", correctionQuery, Obj(("reason", "weak initial evidence")));
                // The better-scored copy of each piece of evidence is kept.
                var merged = new Dictionary<string, JsonObject>();
                var order = new List<string>();
                foreach (var result in results.Concat(corrected))
                {
                    var key = $"{Json.Str(result["type"])}:{Json.Str(result["id"])}";
                    if (!merged.TryGetValue(key, out var existing)) order.Add(key);
                    if (existing is null || Json.Num(result["score"]) > Json.Num(existing["score"])) merged[key] = result;
                }
                results = [.. order.Select(key => merged[key]).Take(limit)];
            }
            ok = true;
            var abstention = Retrieval.ShouldAbstain(results, query);
            var answer = Obj(
                ("traceId", trace.TraceId), ("results", new JsonArray([.. results.Select(r => (JsonNode)Json.Clone(r))])), ("route", route),
                ("plan", Node(plan)), ("corrected", correctionQuery is not null), ("correctionQuery", correctionQuery),
                ("evidence", Node(Retrieval.AssessEvidence(results))), ("abstain", abstention.Abstain));
            if (abstention.Reason is not null) answer["reason"] = abstention.Reason;
            return answer;
        }
        catch (Exception e) when (e is not RecallException)
        {
            throw new RecallException(e.Message, trace.TraceId);
        }
        finally
        {
            var timing = Obj(("route", route), ("durationMs", watch.Elapsed.TotalMilliseconds));
            if (ok) audit.Finish(trace, null, timing);
            else audit.Fail(trace, "recall failed", timing);
            metrics.RecordRetrieval(route, methods, watch.Elapsed.TotalMilliseconds, ok, Env.Number("MEMORY_RECALL_COST_USD", 0));
        }
    }

    private static List<JsonObject> Results(JsonNode? results) => [.. (results as JsonArray ?? []).OfType<JsonObject>()];

    // ---- the routes ----

    public async Task<Reply> Handle(Call call)
    {
        var path = call.Path;
        var get = call.Method == "GET";
        var post = call.Method == "POST";
        if (call.Method == "OPTIONS") return new Reply(204);

        var page = get ? Array.IndexOf(Pages, path) : -1;
        if (page < 0 && !Authorized(call))
        {
            var run = audit.Start("security.unauthorized_request", "security", Obj(("method", call.Method), ("path", path)),
                Obj(("reason", "invalid bearer credential")), ["security", "auth"]);
            audit.Fail(run, "unauthorized");
            return Error(401, "unauthorized", run.TraceId);
        }
        if (call.ContentLength > maxBodyBytes || Encoding.UTF8.GetByteCount(call.Body ?? "") > maxBodyBytes)
        {
            var run = audit.Start("security.request_too_large", "security", Obj(("method", call.Method), ("path", path), ("contentLength", call.ContentLength)),
                Obj(("maxBodyBytes", maxBodyBytes)), ["security", "limits"]);
            audit.Fail(run, "request body too large");
            return Error(413, "request body too large", run.TraceId);
        }

        try
        {
            if (page >= 0) return new Reply(200, Html: MemoryPages.Get(path, maxBodyBytes));
            if (path == "/health" && get)
            {
                await store.Verify();
                return Ok(Obj(("ok", true), ("service", "vinci-memory"), ("neo4j", "connected")));
            }
            if (path.StartsWith("/v1/admin/", StringComparison.Ordinal) || path.StartsWith("/v1/jobs", StringComparison.Ordinal))
                return AdminAuthorized(call) ? await Admin(call) : AdminAuthorizationFailure(call);
            if (path.StartsWith("/v1/chat", StringComparison.Ordinal)) return await Chat(call);
            if (path.StartsWith("/v1/memories", StringComparison.Ordinal)) return await Memories(call);

            if (path == "/v1/events" && post) return await IngestEvent(call);
            if (get && IdIn(path, "/v1/events/") is { } eventId)
            {
                var found = await store.GetEvent(eventId, new MemoryScope { UserId = Param(call, "userId"), ProjectId = Param(call, "projectId"), SessionId = Param(call, "sessionId") });
                return found is null ? Error(404, "event not found") : Ok(Node(found));
            }
            if (path == "/v1/events/batch" && post) return await IngestBatch(call);
            if (path == "/v1/conversations" && post) return await IngestConversation(call);
            if (post && IdIn(path, "/v1/sessions/", "/summary") is { } summarySession) return await IngestSummary(call, summarySession);
            if (post && IdIn(path, "/v1/sessions/", "/reflection") is { } reflectionSession) return await IngestReflection(call, reflectionSession);

            if (path == "/v1/entities" && post)
                return await Ingest(call, "ingest.entity", "entity",
                    b => Obj(("nameChars", Json.Str(b["name"]).Length), ("type", Copy(b["type"]))),
                    b => Text(b, "name") is null || Text(b, "type") is null ? "name and type are required" : null,
                    b => store.UpsertEntity(As<Entity>(b)));
            if (path == "/v1/documents" && post)
                return await Ingest(call, "ingest.document", "document",
                    b => Obj(("titleChars", Json.Str(b["title"]).Length), ("sourceChars", Json.Str(b["source"]).Length)),
                    b => Text(b, "title") is null || Text(b, "source") is null ? "title and source are required" : null,
                    b => store.UpsertDocument(As<DocumentInput>(b)));
            if (path == "/v1/chunks" && post)
                return await Ingest(call, "ingest.chunk", "chunk",
                    b => Obj(("documentId", Copy(b["documentId"])), ("ordinal", Copy(b["ordinal"]))),
                    b => Text(b, "documentId") is null || Text(b, "content") is null || !IsInteger(b["ordinal"], 0, int.MaxValue) ? "documentId, content, and ordinal are required" : null,
                    b => store.UpsertChunk(As<ChunkInput>(b)));
            if (path == "/v1/claims" && post)
                return await Ingest(call, "ingest.claim", "claim",
                    b => Obj(("predicate", Copy(b["predicate"])), ("subjectEntityId", Copy(b["subjectEntityId"]))),
                    b => Text(b, "statement") is null || Text(b, "predicate") is null || Text(b, "subjectEntityId") is null ? "statement, predicate, and subjectEntityId are required" : null,
                    b => store.CreateClaim(As<ClaimInput>(b)));
            if (path == "/v1/procedures" && post)
                return await Ingest(call, "ingest.procedure", "procedure",
                    b => Obj(("titleChars", Json.Str(b["title"]).Length)),
                    b => Text(b, "title") is null || Text(b, "goal") is null || b["steps"] is not JsonArray { Count: > 0 } ? "title, goal, and steps are required" : null,
                    b => store.UpsertProcedure(As<ProcedureInput>(b)));

            if (path == "/v1/recall" && post)
            {
                try { return Ok(await Retrieve(Body(call))); }
                catch (RecallException e) { return Error(400, e.Message, e.TraceId); }
            }
            if (path == "/v1/context" && post) return await Context(call);
            if (path == "/v1/answers/validate" && post) return await ValidateAnswer(call);
            return Error(404, "not found");
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            Console.Error.WriteLine(e);
            return Error(e.Message.StartsWith("idempotency conflict", StringComparison.Ordinal) ? 409 : 500, e.Message);
        }
    }

    // ---- ingestion ----

    private static JsonObject ScopeFields(JsonObject body) =>
        Obj(("userId", Copy(body["userId"])), ("projectId", Copy(body["projectId"])), ("sessionId", Copy(body["sessionId"])));

    /// The shape every "save one thing" route shares: audit it, check it, store it.
    private async Task<Reply> Ingest(Call call, string name, string what, Func<JsonObject, JsonObject> describe,
        Func<JsonObject, string?> problem, Func<JsonObject, Task<JsonObject>> save)
    {
        var body = Body(call);
        var input = describe(body);
        foreach (var (key, value) in ScopeFields(body)) input[key] = Copy(value);
        var run = audit.Start(name, "chain", input, tags: ["ingest", what]);
        if (problem(body) is { } message)
        {
            audit.Fail(run, message);
            return Error(400, message, run.TraceId);
        }
        try
        {
            var saved = await save(body);
            audit.Finish(run, Obj(("id", Copy(saved["id"]))));
            return Ok(With(saved, "traceId", run.TraceId), 201);
        }
        catch (Exception e)
        {
            audit.Fail(run, e.Message);
            return Error(500, e.Message, run.TraceId);
        }
    }

    private async Task<Reply> IngestEvent(Call call)
    {
        var body = Body(call);
        if (body["idempotencyKey"] is null && call.IdempotencyKey is not null) body["idempotencyKey"] = call.IdempotencyKey;
        var input = ScopeFields(body);
        (input["kind"], input["source"]) = (Copy(body["kind"]), Copy(body["source"]));
        var run = audit.Start("ingest.event", "chain", input, tags: ["ingest", "event"]);
        if (Text(body, "content") is null || Text(body, "kind") is null)
        {
            audit.Fail(run, "kind and content are required");
            return Error(400, "kind and content are required", run.TraceId);
        }
        try
        {
            var saved = await store.AppendEvent(As<MemoryEvent>(body));
            audit.Finish(run, Obj(("id", Copy(saved["id"])), ("idempotent", Copy(saved["idempotent"]))));
            return Ok(With(saved, "traceId", run.TraceId), 201);
        }
        catch (Exception e)
        {
            audit.Fail(run, e.Message);
            return Error(IngestionErrorStatus(e), e.Message, run.TraceId);
        }
    }

    private async Task<Reply> SaveEvents(AuditRun run, List<MemoryEvent> events, Func<List<JsonObject>, JsonObject> answer)
    {
        try
        {
            var saved = await store.AppendEvents(events);
            audit.Finish(run, Obj(("count", saved.Count)));
            var body = answer(saved);
            (body["events"], body["count"], body["traceId"]) = (new JsonArray([.. saved]), saved.Count, run.TraceId);
            return Ok(body, 201);
        }
        catch (Exception e)
        {
            audit.Fail(run, e.Message);
            return Error(IngestionErrorStatus(e), e.Message, run.TraceId);
        }
    }

    private async Task<Reply> IngestBatch(Call call)
    {
        var parsed = JsonNode.Parse(call.Body ?? "");
        var events = parsed as JsonArray ?? parsed?["events"] as JsonArray;
        var kinds = (events ?? []).Select(e => Json.Str(e?["kind"])).Where(kind => kind.Length > 0).Distinct();
        var run = audit.Start("ingest.event_batch", "chain", Obj(("count", events?.Count ?? 0), ("kinds", new JsonArray([.. kinds.Select(k => (JsonNode)k)]))), tags: ["ingest", "batch"]);
        string? problem = events is not { Count: >= 1 and <= 100 } ? "events must contain between 1 and 100 items"
            : events.Any(e => e is not JsonObject item || Text(item, "content") is null || Text(item, "kind") is null) ? "every event requires kind and content"
            : null;
        if (problem is not null)
        {
            audit.Fail(run, problem);
            return Error(400, problem, run.TraceId);
        }
        return await SaveEvents(run, [.. events!.Select(e => As<MemoryEvent>((JsonObject)e!))], _ => []);
    }

    private async Task<Reply> IngestConversation(Call call)
    {
        var body = Body(call);
        var messages = body["messages"] as JsonArray;
        var input = ScopeFields(body);
        input["count"] = messages?.Count ?? 0;
        var run = audit.Start("ingest.conversation", "chain", input, tags: ["ingest", "conversation"]);
        string? problem = Text(body, "sessionId") is null || messages is not { Count: >= 1 and <= 100 } ? "sessionId and 1–100 messages are required"
            : messages.Any(m => m is not JsonObject item || Text(item, "content") is null || Text(item, "kind") is null) ? "every message requires kind and content"
            : null;
        if (problem is not null)
        {
            audit.Fail(run, problem);
            return Error(400, problem, run.TraceId);
        }
        var sessionId = Text(body, "sessionId")!;
        var events = messages!.Select(m =>
        {
            var message = As<ConversationMessage>((JsonObject)m!);
            var metadata = message.Metadata is null ? [] : Json.Clone(message.Metadata);
            (metadata["ingestion"], metadata["origin"]) = ("conversation", "codex");
            return new MemoryEvent
            {
                Id = message.Id, IdempotencyKey = message.IdempotencyKey, Kind = message.Kind, Content = message.Content,
                Source = message.Source ?? "codex", OccurredAt = message.OccurredAt, Metadata = metadata,
                UserId = Text(body, "userId"), ProjectId = Text(body, "projectId"), SessionId = sessionId,
            };
        }).ToList();
        return await SaveEvents(run, events, _ => Obj(("sessionId", sessionId)));
    }

    private async Task<Reply> IngestSummary(Call call, string sessionId)
    {
        var body = Body(call);
        var run = audit.Start("ingest.session_summary", "chain", Obj(("sessionId", sessionId), ("userId", Copy(body["userId"])), ("projectId", Copy(body["projectId"]))), tags: ["ingest", "summary"]);
        if (Text(body, "content") is null)
        {
            audit.Fail(run, "summary content is required");
            return Error(400, "summary content is required", run.TraceId);
        }
        try
        {
            var summary = As<SessionSummaryInput>(body);
            var memory = await store.CreateMemory(new CuratedMemory
            {
                Id = summary.IdempotencyKey, Content = summary.Content, Category = "summary", Status = "active", Confidence = summary.Confidence ?? 0.9,
                SourceEventIds = summary.SourceEventIds, UserId = summary.UserId, ProjectId = summary.ProjectId, SessionId = sessionId,
            });
            audit.Finish(run, Obj(("id", Copy(memory["id"])), ("idempotent", summary.IdempotencyKey is not null)));
            return Ok(With(memory, "traceId", run.TraceId), 201);
        }
        catch (Exception e)
        {
            audit.Fail(run, e.Message);
            return Error(500, e.Message, run.TraceId);
        }
    }

    private async Task<Reply> IngestReflection(Call call, string sessionId)
    {
        var body = Body(call);
        var run = audit.Start("ingest.session_reflection", "chain", Obj(("sessionId", sessionId), ("userId", Copy(body["userId"])), ("projectId", Copy(body["projectId"]))), tags: ["ingest", "reflection"]);
        if (Text(body, "content") is null)
        {
            audit.Fail(run, "reflection content is required");
            return Error(400, "reflection content is required", run.TraceId);
        }
        try
        {
            var reflection = As<SessionReflectionInput>(body);
            (string Content, string Category)[] memories =
            [
                (reflection.Content, "reflection"),
                .. (reflection.Lessons ?? []).Where(lesson => lesson.Length > 0).Select(lesson => (lesson, "belief")),
                .. (reflection.Failures ?? []).Where(failure => failure.Length > 0).Select(failure => (failure, "failure")),
            ];
            var saved = new JsonArray();
            for (var index = 0; index < memories.Length; index++)
                saved.Add(await store.CreateMemory(new CuratedMemory
                {
                    Id = reflection.IdempotencyKey is null ? null : $"{reflection.IdempotencyKey}:{index}",
                    Content = memories[index].Content, Category = memories[index].Category, Status = "active",
                    SourceEventIds = reflection.SourceEventIds, UserId = reflection.UserId, ProjectId = reflection.ProjectId, SessionId = sessionId,
                }));
            audit.Finish(run, Obj(("count", saved.Count), ("idempotent", reflection.IdempotencyKey is not null)));
            return Ok(Obj(("sessionId", sessionId), ("memories", saved), ("traceId", run.TraceId)), 201);
        }
        catch (Exception e)
        {
            audit.Fail(run, e.Message);
            return Error(500, e.Message, run.TraceId);
        }
    }

    // ---- memories ----

    private async Task<Reply> Memories(Call call)
    {
        var (path, get, post) = (call.Path, call.Method == "GET", call.Method == "POST");
        if (path == "/v1/memories" && get)
        {
            var status = Param(call, "status");
            if (status is not null && !Vocabulary.MemoryStatuses.Contains(status)) return Error(400, "invalid memory status");
            var scope = new MemoryScope { UserId = Param(call, "userId"), ProjectId = Param(call, "projectId"), SessionId = Param(call, "sessionId") };
            return Ok(Obj(("memories", new JsonArray([.. await store.ListMemories(scope, (int)NumberParam(call, "limit", 50), status)]))));
        }
        if (path == "/v1/memories" && post)
        {
            var body = Body(call);
            var correction = body["supersedesMemoryId"] is not null;
            var run = audit.Start(correction ? "memory.correct" : "memory.create", "chain",
                Obj(("category", Copy(body["category"])), ("projectId", Copy(body["projectId"])), ("sessionId", Copy(body["sessionId"])), ("supersedesMemoryId", Copy(body["supersedesMemoryId"]))),
                tags: ["memory", correction ? "correction" : "capture"]);
            if (Text(body, "content") is null)
            {
                audit.Fail(run, "content is required");
                return Error(400, "content is required", run.TraceId);
            }
            return await Audited(run, async () =>
            {
                var memory = await store.CreateMemory(As<CuratedMemory>(body));
                audit.Finish(run, Obj(("id", Copy(memory["id"])), ("status", Copy(memory["status"])), ("supersedesMemoryId", Copy(body["supersedesMemoryId"]))));
                return Ok(With(memory, "traceId", run.TraceId), 201);
            });
        }
        if (get && IdIn(path, "/v1/memories/", "/impact") is { } impactId)
            return await store.MemoryImpact(impactId) is { } impact ? Ok(impact) : Error(404, "memory not found");
        if (post && IdIn(path, "/v1/memories/", "/retract") is { } retractId)
            return await Change(call, "memory.retract", "forgetting", retractId, reason => store.RetractMemory(retractId, reason));
        if (post && IdIn(path, "/v1/memories/", "/archive") is { } archiveId)
            return await Change(call, "memory.archive", "retention", archiveId, reason => store.ArchiveMemory(archiveId, reason));
        if (post && IdIn(path, "/v1/memories/", "/utility") is { } utilityId)
        {
            var body = Body(call);
            var run = audit.Start("memory.utility", "validation",
                Obj(("memoryId", utilityId), ("useful", Copy(body["useful"])), ("feedbackChars", Json.Str(body["feedback"]).Length)),
                tags: ["memory", "feedback"], traceId: Text(body, "traceId"));
            if (body["useful"]?.GetValueKind() is not (JsonValueKind.True or JsonValueKind.False))
            {
                audit.Fail(run, "useful must be boolean");
                return Error(400, "useful must be boolean", run.TraceId);
            }
            return await Audited(run, async () =>
            {
                if (await store.RecordMemoryUtility(utilityId, Json.Flag(body["useful"]), Text(body, "feedback")) is not { } utility)
                {
                    audit.Fail(run, "memory not found");
                    return Error(404, "memory not found");
                }
                audit.Finish(run, Obj(("id", Copy(utility["id"])), ("useful", Copy(body["useful"]))));
                return Ok(With(utility, "traceId", run.TraceId));
            });
        }
        return Error(404, "not found");
    }

    /// Retract and archive are the same act with a different word.
    private async Task<Reply> Change(Call call, string name, string tag, string memoryId, Func<string?, Task<JsonObject?>> change)
    {
        var reason = Text(Body(call, lenient: true), "reason");
        var run = audit.Start(name, "security", Obj(("memoryId", memoryId), ("reasonChars", reason?.Length ?? 0)), tags: ["memory", tag]);
        return await Audited(run, async () =>
        {
            if (await change(reason) is not { } memory)
            {
                audit.Fail(run, "memory not found");
                return Error(404, "memory not found");
            }
            audit.Finish(run, Obj(("id", Copy(memory["id"])), ("status", Copy(memory["status"]))));
            return Ok(With(memory, "traceId", run.TraceId));
        });
    }

    /// A failure inside is written to the run before it travels on.
    private async Task<Reply> Audited(AuditRun run, Func<Task<Reply>> work)
    {
        try
        {
            return await work();
        }
        catch (Exception e)
        {
            audit.Fail(run, e.Message);
            throw;
        }
    }

    // ---- context and answers ----

    private async Task<Reply> Context(Call call)
    {
        var body = Body(call);
        if (body["maxChars"] is not null && !IsInteger(body["maxChars"], 256, 100_000))
        {
            var input = ScopeFields(body);
            (input["queryChars"], input["maxChars"]) = (Json.Str(body["query"]).Length, Copy(body["maxChars"]));
            var run = audit.Start("memory.context", "validation", input, tags: ["memory", "context", "validation"]);
            audit.Fail(run, "maxChars must be an integer between 256 and 100000");
            return Error(400, "maxChars must be an integer between 256 and 100000", run.TraceId);
        }
        try
        {
            var retrieval = await Retrieve(body);
            var pack = ContextBuilder.Build(Results(retrieval["results"]), body["maxChars"] is null ? 12_000 : (int)Json.Num(body["maxChars"]));
            // The pack's results replace the retrieval's: only what fit is listed.
            foreach (var (key, value) in (JsonObject)Node(pack)!) retrieval[key] = Copy(value);
            return Ok(retrieval);
        }
        catch (RecallException e)
        {
            return Error(400, e.Message, e.TraceId);
        }
    }

    private async Task<Reply> ValidateAnswer(Call call)
    {
        var body = Body(call);
        var requireJudge = Json.Flag(body["requireJudge"]);
        var run = audit.Start("answers.validate", "validation",
            Obj(("queryChars", Json.Str(body["query"]).Length), ("answerChars", Json.Str(body["answer"]).Length),
                ("resultCount", (body["results"] as JsonArray)?.Count ?? 0), ("requireJudge", requireJudge)), tags: ["answers", "validation"]);
        if (Text(body, "answer") is not { } answer || body["results"] is not JsonArray)
        {
            audit.Fail(run, "answer and results are required");
            return Error(400, "answer and results are required", run.TraceId);
        }
        var results = Results(body["results"]);
        var citation = Citations.Validate(answer, results);
        Reply Rejected(string why, JsonNode? faithfulness = null, JsonObject? metadata = null)
        {
            metrics.RecordAnswerValidation(false);
            audit.Fail(run, why, metadata);
            var rejection = Obj(("valid", false), ("citation", Node(citation)), ("traceId", run.TraceId));
            if (faithfulness is not null) rejection["faithfulness"] = faithfulness;
            return Ok(rejection, 422);
        }
        if (!citation.Valid) return Rejected("citation validation failed", metadata: Obj(("citationCount", citation.Citations.Count)));

        static IEnumerable<string> Contents(JsonObject item) => item["content"]?.GetValueKind() == JsonValueKind.String ? [Json.Str(item["content"])] : [];
        var evidence = results.SelectMany(r => Contents(r).Concat((r["evidence"] as JsonArray ?? []).OfType<JsonObject>().SelectMany(Contents)))
            .Where(text => text.Length > 0).ToList();
        var judge = new FaithfulnessJudge();
        FaithfulnessVerdict? external;
        try
        {
            external = Text(body, "query") is { } query && judge.Enabled ? await judge.Judge(query, answer, evidence) : null;
        }
        catch (Exception e) when (e is InvalidOperationException or HttpRequestException)
        {
            audit.Fail(run, e.Message);
            return Error(502, e.Message, run.TraceId);
        }
        if (requireJudge && external is null)
            return Rejected("external faithfulness judge is required", Obj(("judged", false), ("reason", "An external faithfulness judge is required.")));
        if (external is { Faithful: false }) return Rejected("faithfulness validation failed", Node(external), Obj(("judged", true)));
        metrics.RecordAnswerValidation(true);
        audit.Finish(run, Obj(("valid", true), ("citationCount", citation.Citations.Count), ("judged", external is not null)));
        return Ok(Obj(("valid", true), ("citation", Node(citation)),
            ("faithfulness", external is null ? Obj(("judged", false), ("fallback", "citation-only")) : Node(external)), ("traceId", run.TraceId)));
    }

    // ---- administration and jobs ----

    private async Task<Reply> Admin(Call call)
    {
        var (path, get, post) = (call.Path, call.Method == "GET", call.Method == "POST");
        if (get)
        {
            switch (path)
            {
                case "/v1/admin/export": return Ok(Node(await store.ExportSnapshot()));
                case "/v1/admin/stats": return Ok(await store.Stats());
                case "/v1/admin/schema": return Ok(await store.SchemaStatus());
                case "/v1/admin/metrics": return Ok(metrics.Snapshot());
                case "/v1/admin/audit/stats": return Ok(audit.Stats());
                case "/v1/admin/context-hubs": return Ok(await store.ContextHubs((int)NumberParam(call, "limit", 20)));
                case "/v1/admin/audit/runs":
                    return Ok(Obj(("runs", Node(audit.Query(Param(call, "traceId"), Param(call, "status"), Param(call, "kind"), Param(call, "name"), (int)NumberParam(call, "limit", 50))))));
                case "/v1/admin/audit/export":
                    return Ok(Obj(("format", "personal-memory-audit"), ("version", 1),
                        ("exportedAt", DateTimeOffset.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ", CultureInfo.InvariantCulture)), ("runs", Node(audit.Query(limit: 200)))));
                case "/v1/admin/providers":
                    var probes = await Task.WhenAll(
                        ProviderStatus.Probe(Env.Get("MODEL_BASE_URL"), path: "/models"), ProviderStatus.Probe(Env.Get("EMBEDDING_BASE_URL"), path: "/models"),
                        ProviderStatus.Probe(Env.Get("RERANKER_BASE_URL")), ProviderStatus.Probe(Env.Get("FAITHFULNESS_JUDGE_BASE_URL")));
                    JsonObject Probe(int index, params (string Key, JsonNode? Value)[] more)
                    {
                        var status = (JsonObject)Node(probes[index])!;
                        foreach (var (key, value) in more) status[key] = value;
                        return status;
                    }
                    return Ok(Obj(
                        ("neo4j", Obj(("configured", true), ("database", Env.Get("NEO4J_DATABASE") ?? "neo4j"))),
                        ("consolidation", Probe(0, ("model", Env.Get("MODEL_NAME") ?? "local-model"))),
                        ("embedding", Probe(1, ("model", Env.Get("EMBEDDING_MODEL") ?? EmbeddingClient.DefaultModel), ("dimensions", Env.Number("EMBEDDING_DIMENSIONS", EmbeddingClient.DefaultDimensions)))),
                        ("reranker", Probe(2)),
                        ("faithfulnessJudge", Probe(3, ("requiredForStrictBenchmark", Env.Get("FAITHFULNESS_REQUIRE_JUDGE") == "1")))));
                case "/v1/jobs":
                    var status = Param(call, "status");
                    if (status is not null && !JobStatuses.Contains(status)) return Error(400, "invalid job status");
                    return Ok(Obj(("jobs", new JsonArray([.. await store.ListJobs(status, (int)NumberParam(call, "limit", 100))]))));
            }
            if (IdIn(path, "/v1/admin/audit/traces/") is { } shown) return Ok(Obj(("traceId", shown), ("runs", Node(audit.Trace(shown)))));
        }
        if (!post) return Error(404, "not found");

        if (IdIn(path, "/v1/admin/audit/traces/") is { } traceId)
        {
            if (Text(Body(call, lenient: true), "confirm") != "replay") return Error(400, "confirm must equal replay");
            var root = audit.Trace(traceId).Find(run => run.ParentRunId is null);
            if (root is not { Name: "memory.recall", Input: JsonObject input })
                return Ok(Obj(("replayable", false), ("error", "only memory.recall traces can be safely replayed")), 409);
            // The audit trail keeps how long a query was, never the query itself.
            if (Text(input, "query") is null) return Ok(Obj(("replayable", false), ("error", "trace input does not contain a query")), 409);
            return Ok(Obj(("replayable", true), ("originalTraceId", traceId), ("replay", await Retrieve(Json.Clone(input)))));
        }
        if (path == "/v1/admin/audit/clear")
        {
            if (Text(Body(call, lenient: true), "confirm") != "clear-audit") return Error(400, "confirm must equal clear-audit");
            audit.Clear();
            return Ok(With(audit.Stats(), "cleared", true));
        }
        if (path == "/v1/jobs/next")
        {
            var job = await store.ClaimNextJob(Text(Body(call, lenient: true), "targetId"), [], Env.Get("CONSOLIDATION_REQUIRE_APPROVAL") == "1");
            return Ok(Obj(("job", job is null ? null : Node(job))));
        }
        if (IdIn(path, "/v1/jobs/", "/preview") is { } previewId)
        {
            if (await store.GetJobEvent(previewId) is not { } job) return Error(404, "job not found");
            if (Json.Str(job["kind"]) != "consolidate_event" || job["event"] is not JsonObject target)
                return Error(409, "only event consolidation jobs can be previewed");
            var run = audit.Start("consolidation.preview", "chain", Obj(("jobId", previewId), ("eventId", Copy(target["id"]))), tags: ["consolidation", "proposal"]);
            try
            {
                var proposal = await consolidator.Extract(As<MemoryEvent>(target));
                await store.SaveJobProposal(previewId, Node(proposal));
                audit.Finish(run, Obj(("memoryCandidates", proposal.Memories.Count), ("entityCandidates", proposal.Entities.Count),
                    ("claimCandidates", proposal.Claims.Count), ("procedureCandidates", proposal.Procedures.Count)));
                return Ok(Obj(("jobId", Copy(job["id"])), ("proposal", Node(proposal)), ("traceId", run.TraceId)));
            }
            catch (Exception e)
            {
                audit.Fail(run, e.Message);
                return Error(502, e.Message, run.TraceId);
            }
        }
        if (IdIn(path, "/v1/jobs/", "/approve") is { } approveId)
            return await Decide("consolidation.approve", "approval", Obj(("jobId", approveId)), "job not found", () => store.ApproveJob(approveId));
        if (IdIn(path, "/v1/jobs/", "/reject") is { } rejectId)
        {
            var reason = Text(Body(call, lenient: true), "reason");
            return await Decide("consolidation.reject", "approval", Obj(("jobId", rejectId), ("reasonChars", reason?.Length ?? 0)), "job not found", () => store.RejectJob(rejectId, reason));
        }
        if (IdIn(path, "/v1/jobs/", "/complete") is { } completeId)
            return Ok(await store.CompleteJob(completeId, Text(Body(call, lenient: true), "error")));
        if (IdIn(path, "/v1/jobs/", "/retry") is { } retryId)
            return await Decide("consolidation.retry", "retry", Obj(("jobId", retryId)), "failed job not found", () => store.RequeueFailedJob(retryId));
        return Error(404, "not found");
    }

    private async Task<Reply> Decide(string name, string tag, JsonObject input, string missing, Func<Task<JsonObject?>> change)
    {
        var run = audit.Start(name, "security", input, tags: ["consolidation", tag]);
        if (await change() is not { } result)
        {
            audit.Fail(run, missing);
            return Error(404, missing, run.TraceId);
        }
        audit.Finish(run, Json.Clone(result));
        return Ok(With(result, "traceId", run.TraceId));
    }

    // ---- chat ----

    private async Task<Reply> Chat(Call call)
    {
        var (path, disabled) = (call.Path, Env.Get("CODEX_APP_SERVER_DISABLED") == "1");
        const string Off = "Codex App Server integration is disabled";
        if (path == "/v1/chat/auth" && call.Method == "GET")
        {
            if (disabled) return Ok(Obj(("connected", false), ("disabled", true)));
            try { return Ok(Obj(("connected", true), ("account", await codex.Account()))); }
            catch (Exception e) when (e is not OutOfMemoryException) { return Ok(Obj(("connected", false), ("error", e.Message)), 503); }
        }
        if (path == "/v1/chat/auth/start" && call.Method == "POST")
        {
            var login = audit.Start("auth.chatgpt_login", "security", Obj(("action", "begin_device_login")), tags: ["auth", "chatgpt"]);
            if (disabled)
            {
                audit.Fail(login, Off);
                return Error(503, Off, login.TraceId);
            }
            try
            {
                var result = await codex.BeginChatGPTLogin() as JsonObject ?? [];
                audit.Finish(login, Obj(("fields", new JsonArray([.. result.Select(field => (JsonNode)field.Key).OrderBy(key => key.GetValue<string>(), StringComparer.Ordinal)]))));
                return Ok(With(result, "traceId", login.TraceId));
            }
            catch (Exception e) when (e is not OutOfMemoryException)
            {
                audit.Fail(login, e.Message);
                return Error(503, e.Message, login.TraceId);
            }
        }
        if (path != "/v1/chat" || call.Method != "POST") return Error(404, "not found");
        if (disabled)
        {
            var off = audit.Start("chat.turn", "chain", Obj(("integration", "codex"), ("disabled", true)), tags: ["chat", "codex", "error"]);
            audit.Fail(off, Off);
            return Error(503, Off, off.TraceId);
        }

        var body = Body(call);
        var sessionId = Text(body, "sessionId") ?? "web-chat";
        var memoryScope = Text(body, "memoryScope") ?? "session";
        var captureTurn = body["captureTurn"]?.GetValueKind() != JsonValueKind.False;
        var (userId, projectId, model) = (Text(body, "userId"), Text(body, "projectId"), Text(body, "model") ?? Env.Get("CODEX_MODEL"));
        var trace = audit.Start("chat.turn", "chain",
            Obj(("messageChars", Json.Str(body["message"]).Length), ("threadId", Copy(body["threadId"])), ("sessionId", sessionId), ("memoryScope", memoryScope),
                ("captureTurn", captureTurn), ("projectId", memoryScope == "project" ? projectId : null)),
            Obj(("provider", "openai-codex"), ("model", model ?? "default")), ["chat", "codex", "memory"]);
        string? problem = Text(body, "message") is null ? "message is required"
            : memoryScope is not ("session" or "project" or "global") ? "memoryScope must be session, project, or global"
            : memoryScope == "project" && projectId is null ? "projectId is required for project memory scope"
            : null;
        if (problem is not null)
        {
            audit.Fail(trace, problem);
            return Error(400, problem, trace.TraceId);
        }
        var message = Text(body, "message")!;
        try
        {
            var question = Obj(("query", message), ("userId", userId), ("limit", 12));
            if (memoryScope == "project") question["projectId"] = projectId;
            if (memoryScope == "session") question["sessionId"] = sessionId;
            var retrieval = await Retrieve(question, trace);
            var context = ContextBuilder.Build(Results(retrieval["results"]), 12_000);
            var modelRun = audit.Start("codex.turn", "llm",
                Obj(("messageChars", message.Length), ("contextChars", context.Context.Length), ("evidenceCount", context.Results.Count)),
                Obj(("provider", "openai-codex"), ("model", model ?? "default"), ("citationLocked", context.CitationLocked)), traceId: trace.TraceId, parentRunId: trace.Id);
            var prompt = string.Join("\n\n",
                "You are the private assistant for Personal Memory. Use the retrieved memory only when it is relevant. Do not invent facts. Cite retrieved claims using the exact [type:id] markers. If the evidence is insufficient, say so.",
                "Retrieved memory:\n" + (context.Context.Length > 0 ? context.Context : "(no relevant memory found)"),
                "User message:\n" + message);
            ChatResult result;
            try
            {
                result = await codex.Chat(prompt, Text(body, "threadId"), model);
            }
            catch (Exception e)
            {
                audit.Fail(modelRun, e.Message);
                throw;
            }
            RecordCodexToolRuns(result.Events, trace.TraceId, modelRun.Id);
            audit.Finish(modelRun, Obj(("textChars", result.Text.Length)), Obj(("turnId", result.TurnId), ("eventCount", result.Events.Count), ("usage", Copy(result.Usage))));

            var validation = audit.Start("answers.validate", "validation", Obj(("answerChars", result.Text.Length), ("evidenceCount", context.Results.Count)),
                traceId: trace.TraceId, parentRunId: trace.Id);
            var citation = context.Results.Count > 0
                ? Citations.Validate(result.Text, context.Results)
                : new CitationValidation(true, [], "No retrieved evidence was available; the response must not claim private-memory facts.");
            audit.Finish(validation, Node(citation), Obj(("citationLocked", context.CitationLocked)));

            var saved = captureTurn
                ? await store.AppendEvents([
                    new MemoryEvent { Kind = "user_message", Content = message, Source = "web-chat", UserId = userId, ProjectId = projectId, SessionId = sessionId },
                    new MemoryEvent
                    {
                        Kind = "agent_message", Content = result.Text, Source = "openai-codex", UserId = userId, ProjectId = projectId, SessionId = sessionId,
                        Metadata = Obj(("traceId", trace.TraceId), ("threadId", result.ThreadId), ("turnId", result.TurnId)),
                    },
                ])
                : [];
            audit.Finish(trace, Obj(("textChars", result.Text.Length), ("threadId", result.ThreadId), ("turnId", result.TurnId)), Obj(
                ("citations", new JsonArray([.. context.Results.Select(item => (JsonNode)Obj(("type", Copy(item["type"])), ("id", Copy(item["id"])), ("sourceEventIds", Copy(item["sourceEventIds"]))))])),
                ("citationValidation", Node(citation)), ("usage", Copy(result.Usage))));
            return Ok(Obj(
                ("threadId", result.ThreadId), ("turnId", result.TurnId), ("text", result.Text),
                ("events", new JsonArray([.. result.Events.Select(e => (JsonNode)Json.Clone(e))])), ("usage", Copy(result.Usage)),
                ("traceId", trace.TraceId), ("memory", Node(context)), ("citation", Node(citation)), ("captureTurn", captureTurn),
                ("savedEventIds", new JsonArray([.. saved.Select(e => Copy(e["id"]))]))));
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            audit.Fail(trace, e.Message);
            return Error(502, e.Message, trace.TraceId);
        }
    }

    /// Each tool the model used during a turn gets its own run in the trace.
    /// Only the names of its parameters are kept, never their values.
    private void RecordCodexToolRuns(IEnumerable<JsonObject> events, string traceId, string parentRunId)
    {
        foreach (var e in events)
        {
            var method = Json.Str(e["method"]);
            var parameters = e["params"] as JsonObject ?? [];
            var itemType = Json.Str((parameters["item"] as JsonObject)?["type"] ?? parameters["type"]);
            if (!ToolWords().IsMatch($"{method} {itemType}")) continue;
            JsonArray Keys() => new([.. parameters.Select(field => field.Key).Order(StringComparer.Ordinal).Select(key => (JsonNode)key)]);
            var run = audit.Start($"codex.{(itemType.Length > 0 ? itemType : method.Replace('/', '.'))}", "tool",
                Obj(("method", method), ("itemType", itemType), ("parameterKeys", Keys())),
                Obj(("provider", "openai-codex"), ("event", method)), ["codex", "tool"], traceId, parentRunId);
            audit.Finish(run, Obj(("parameterKeys", Keys()), ("itemType", itemType)));
        }
    }
}

/// The web pages, kept inside the program.
public static class MemoryPages
{
    private static readonly Dictionary<string, string> Files = new()
    {
        ["/"] = "audit", ["/chat"] = "chat", ["/ingest"] = "ingest", ["/sessions"] = "sessions", ["/imports"] = "imports",
        ["/memories"] = "memories", ["/jobs"] = "jobs", ["/status"] = "status", ["/metrics"] = "metrics",
    };

    /// The size the import page was written out with, replaced by the real limit.
    private const string ImportLimitMarker = "777777777";

    public static string Get(string route, long maxBodyBytes)
    {
        var name = $"Vinci.Memory.Pages.{Files[route]}.html";
        using var stream = Assembly.GetExecutingAssembly().GetManifestResourceStream(name) ?? throw new FileNotFoundException(name);
        using var reader = new StreamReader(stream, Encoding.UTF8);
        var html = reader.ReadToEnd();
        return route == "/imports"
            ? html.Replace(ImportLimitMarker, Math.Max(1, maxBodyBytes - 64 * 1024).ToString(CultureInfo.InvariantCulture))
            : html;
    }
}

/// Puts the memory service on a port.
public sealed class MemoryServer(MemoryService service)
{
    /// In production the service refuses to run without an API key.
    public static bool Production => Env.Get("VINCI_ENV") == "production" || Env.Get("NODE_ENV") == "production";

    private readonly string corsOrigin = Env.Get("MEMORY_CORS_ORIGIN") ?? "http://127.0.0.1:5173";

    public async Task Run(CancellationToken stop, TextWriter log)
    {
        var host = Env.Get("MEMORY_HOST") ?? "127.0.0.1";
        var port = (int)Env.Number("MEMORY_PORT", 4781);
        using var listener = new HttpListener();
        // The listener answers only the host names it is given, so a loopback
        // service is given both of the names a browser would use for it.
        foreach (var name in host is "0.0.0.0" or "::" ? ["+"] : host is "127.0.0.1" or "localhost" ? (string[])["127.0.0.1", "localhost"] : [host])
            listener.Prefixes.Add($"http://{name}:{port}/");
        listener.Start();
        log.WriteLine($"vinci memory API listening on http://{host}:{port}");
        using var stopping = stop.Register(listener.Stop);
        while (!stop.IsCancellationRequested)
        {
            HttpListenerContext context;
            try { context = await listener.GetContextAsync(); }
            catch (Exception e) when (e is HttpListenerException or ObjectDisposedException or InvalidOperationException) { break; }
            _ = Task.Run(async () =>
            {
                try { await Answer(context); }
                catch (Exception e) when (e is HttpListenerException or IOException or ObjectDisposedException) { }
                finally { context.Response.Close(); }
            }, CancellationToken.None);
        }
    }

    private async Task Answer(HttpListenerContext context)
    {
        var request = context.Request;
        string? body = null;
        if (request.HasEntityBody)
        {
            using var reader = new StreamReader(request.InputStream, Encoding.UTF8);
            body = await reader.ReadToEndAsync();
        }
        var query = request.QueryString.AllKeys.Where(key => key is not null).ToDictionary(key => key!, key => request.QueryString[key] ?? "");
        var reply = await service.Handle(new Call(request.HttpMethod, request.Url!.AbsolutePath, query,
            request.Headers["Authorization"], request.Headers["Idempotency-Key"], Math.Max(0, request.ContentLength64), body));

        var response = context.Response;
        response.StatusCode = reply.Status;
        response.Headers["Access-Control-Allow-Origin"] = corsOrigin;
        response.Headers["Access-Control-Allow-Headers"] = "authorization, content-type";
        response.Headers["Access-Control-Allow-Methods"] = "GET, POST, OPTIONS";
        if (reply.Html is null && reply.Json is null) return;
        response.ContentType = reply.Html is null ? "application/json; charset=utf-8" : "text/html; charset=utf-8";
        var bytes = Encoding.UTF8.GetBytes(reply.Html ?? reply.Json!.ToJsonString(Json.Options));
        response.ContentLength64 = bytes.Length;
        await response.OutputStream.WriteAsync(bytes);
    }
}
