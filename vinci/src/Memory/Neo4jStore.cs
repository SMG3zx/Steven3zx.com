using System.Collections;
using System.Globalization;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;
using Neo4j.Driver;

namespace Vinci.Memory;

/// The graph the memory service keeps in Neo4j: events as the source of
/// truth, and memories, entities, documents, chunks, claims and procedures
/// that point back at them. Every query is scoped by user, project and
/// session, and nothing is deleted: a memory is superseded, archived or
/// retracted.
public sealed partial class Neo4jStore : IAsyncDisposable
{
    public const int SchemaVersion = 2;
    private const string SchemaMigrationId = "v2-content-index-safety";

    private readonly IDriver driver;
    private readonly string database;
    private readonly EmbeddingClient embeddings;
    private readonly RerankerClient reranker;

    public Neo4jStore(EmbeddingClient? embeddings = null, RerankerClient? reranker = null)
    {
        var (uri, username, password) = (Env.Get("NEO4J_URI"), Env.Get("NEO4J_USERNAME"), Env.Get("NEO4J_PASSWORD"));
        if (uri is null || username is null || password is null)
            throw new InvalidOperationException("NEO4J_URI, NEO4J_USERNAME, and NEO4J_PASSWORD are required");
        driver = GraphDatabase.Driver(uri, AuthTokens.Basic(username, password));
        database = Env.Get("NEO4J_DATABASE") ?? "neo4j";
        this.embeddings = embeddings ?? new EmbeddingClient();
        this.reranker = reranker ?? new RerankerClient();
    }

    public ValueTask DisposeAsync() => driver.DisposeAsync();

    public async Task<bool> Verify()
    {
        await driver.VerifyConnectivityAsync();
        return true;
    }

    // ---- plumbing ----

    private IAsyncSession Session() => driver.AsyncSession(options => options.WithDatabase(database));

    private static async Task<List<JsonObject>> Rows(IAsyncQueryRunner runner, string cypher, Dictionary<string, object?>? parameters = null)
    {
        var cursor = await runner.RunAsync(cypher, parameters ?? []);
        return [.. (await cursor.ToListAsync()).Select(record => new JsonObject(record.Keys.Select(key => KeyValuePair.Create(key, ToJson(record[key])))))];
    }

    private async Task<List<JsonObject>> Rows(string cypher, Dictionary<string, object?>? parameters = null)
    {
        await using var session = Session();
        return await Rows(session, cypher, parameters);
    }

    private async Task<JsonObject?> Row(string cypher, Dictionary<string, object?>? parameters = null) =>
        (await Rows(cypher, parameters)).FirstOrDefault();

    private async Task<T> Write<T>(Func<IAsyncQueryRunner, Task<T>> work)
    {
        await using var session = Session();
        return await session.ExecuteWriteAsync(tx => work(tx));
    }

    /// What Neo4j hands back, as plain JSON: integers and dates become
    /// numbers and text, nodes become their properties.
    private static JsonNode? ToJson(object? value) => value switch
    {
        null => null,
        string text => text,
        bool flag => flag,
        long number => number,
        int number => number,
        double number => number,
        float number => number,
        INode node => ToJson(node.Properties),
        IRelationship relationship => ToJson(relationship.Properties),
        IEnumerable<KeyValuePair<string, object>> map => new JsonObject(map.Select(entry => KeyValuePair.Create(entry.Key, ToJson(entry.Value)))),
        IEnumerable list => new JsonArray([.. list.Cast<object?>().Select(ToJson)]),
        _ => Convert.ToString(value, CultureInfo.InvariantCulture),
    };

    /// JSON as a Neo4j parameter or property value.
    private static object? FromJson(JsonNode? node) => node switch
    {
        null => null,
        JsonObject map => map.ToDictionary(entry => entry.Key, entry => FromJson(entry.Value)),
        JsonArray list => list.Select(FromJson).ToList(),
        _ => node.GetValueKind() switch
        {
            System.Text.Json.JsonValueKind.String => node.GetValue<string>(),
            System.Text.Json.JsonValueKind.True => true,
            System.Text.Json.JsonValueKind.False => false,
            _ => Json.Num(node) is var number && number == Math.Floor(number) && Math.Abs(number) < 9e15 ? (long)number : number,
        },
    };

    private static Dictionary<string, object?> ScopeOf(MemoryScope? scope) => new()
    {
        ["userId"] = scope?.UserId,
        ["projectId"] = scope?.ProjectId,
        ["sessionId"] = scope?.SessionId,
        ["includeHistorical"] = scope?.IncludeHistorical ?? false,
        ["includeTestData"] = scope?.IncludeTestData ?? false,
    };

    private static Dictionary<string, object?> With(Dictionary<string, object?> parameters, params (string Key, object? Value)[] more)
    {
        foreach (var (key, value) in more) parameters[key] = value;
        return parameters;
    }

    private static Dictionary<string, object?> P(params (string Key, object? Value)[] parameters) => With([], parameters);

    private static string NewId() => Guid.NewGuid().ToString();

    /// The id of a memory that has none: the same text in the same scope always gets the same id,
    /// so consolidating an event again reinforces the memory instead of storing a copy of it.
    public static string ContentMemoryId(MemoryScope scope, string content)
    {
        var normalized = string.Join(' ', content.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries)).ToLowerInvariant();
        var key = JsonSerializer.Serialize(new[] { scope.UserId ?? "", scope.ProjectId ?? "", scope.SessionId ?? "", normalized });
        return "mem-" + Convert.ToHexStringLower(System.Security.Cryptography.SHA256.HashData(Encoding.UTF8.GetBytes(key)))[..32];
    }

    private static string Now() => DateTimeOffset.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ", CultureInfo.InvariantCulture);

    private static string Metadata(JsonObject? metadata) => (metadata ?? []).ToJsonString(Json.Options);

    private static JsonObject? ParseMetadata(JsonNode? stored)
    {
        try { return Json.Str(stored) is { Length: > 0 } text ? JsonNode.Parse(text) as JsonObject : null; }
        catch (System.Text.Json.JsonException) { return null; }
    }

    private static long Clamp(int value, int min, int max) => Math.Min(Math.Max(value, min), max);

    // ---- administration ----

    public async Task<MemorySnapshot> ExportSnapshot()
    {
        await using var session = Session();
        var nodes = await Rows(session, "MATCH (n) RETURN elementId(n) AS key, labels(n) AS labels, properties(n) AS properties");
        var relationships = await Rows(session, "MATCH (source)-[r]->(target) RETURN elementId(source) AS sourceKey, type(r) AS type, elementId(target) AS targetKey, properties(r) AS properties");
        return new MemorySnapshot(MemorySnapshot.FormatName, 1, Now(),
            [.. nodes.Select(n => new SnapshotNode(Json.Str(n["key"]), Json.Strings(n["labels"]), (JsonObject)n["properties"]!.DeepClone()))],
            [.. relationships.Select(r => new SnapshotRelationship(Json.Str(r["sourceKey"]), Json.Str(r["type"]), Json.Str(r["targetKey"]), (JsonObject)r["properties"]!.DeepClone()))]);
    }

    /// Writes a snapshot back. Nodes are matched by their id when they have
    /// one, so restoring over existing data updates it instead of doubling it.
    public async Task Restore(MemorySnapshot snapshot)
    {
        snapshot.Validate();
        await using var session = Session();
        foreach (var node in snapshot.Nodes)
        {
            var labels = string.Concat(node.Labels.Select(label => $":{label}"));
            var hasId = node.Properties["id"]?.GetValueKind() == System.Text.Json.JsonValueKind.String;
            var properties = (Dictionary<string, object?>)FromJson(node.Properties)!;
            properties["__snapshotKey"] = node.Key;
            await Rows(session, $"MERGE (n{labels} {{{(hasId ? "id" : "__snapshotKey")}: $identity}}) SET n += $properties",
                P(("identity", hasId ? Json.Str(node.Properties["id"]) : node.Key), ("properties", properties)));
        }
        foreach (var relationship in snapshot.Relationships)
            await Rows(session,
                $$"""
                MATCH (source {__snapshotKey: $sourceKey}), (target {__snapshotKey: $targetKey})
                MERGE (source)-[r:{{relationship.Type}}]->(target) SET r += $properties
                """,
                P(("sourceKey", relationship.SourceKey), ("targetKey", relationship.TargetKey), ("properties", FromJson(relationship.Properties))));
    }

    public async Task<JsonObject> Stats()
    {
        var row = await Row("""
            CALL {
              MATCH (n) RETURN count(n) AS nodeCount
            }
            CALL {
              MATCH ()-[r]->() RETURN count(r) AS relationshipCount
            }
            CALL {
              MATCH (j:Job) RETURN collect({status: j.status, count: 1}) AS rawJobs
            }
            RETURN nodeCount, relationshipCount, rawJobs
            """);
        var jobs = new JsonObject();
        foreach (var job in row?["rawJobs"] as JsonArray ?? [])
        {
            var status = Json.Str(job?["status"], "unknown");
            jobs[status] = Json.Num(jobs[status]) + 1;
        }
        return new JsonObject { ["nodeCount"] = Json.Num(row?["nodeCount"]), ["relationshipCount"] = Json.Num(row?["relationshipCount"]), ["jobs"] = jobs };
    }

    public async Task<JsonObject> SchemaStatus()
    {
        var row = await Row("""
            MATCH (s:SchemaMetadata {name: 'personal-memory'})
            RETURN s.version AS version, s.createdAt AS createdAt, s.checkedAt AS checkedAt,
                   coalesce(s.appliedMigrations, []) AS appliedMigrations
            """);
        if (row is null)
            return new JsonObject { ["currentVersion"] = SchemaVersion, ["storedVersion"] = null, ["initialized"] = false, ["migrations"] = new JsonArray() };
        return new JsonObject
        {
            ["currentVersion"] = SchemaVersion,
            ["storedVersion"] = Json.Num(row["version"]),
            ["initialized"] = true,
            ["createdAt"] = Json.Str(row["createdAt"]),
            ["checkedAt"] = Json.Str(row["checkedAt"]),
            ["migrations"] = row["appliedMigrations"]!.DeepClone(),
        };
    }

    /// The most connected things in scope. Uses GDS PageRank when a
    /// projection exists, and falls back to counting relationships.
    public async Task<JsonObject> ContextHubs(int limit = 20, MemoryScope? scope = null)
    {
        var graphName = Env.Get("NEO4J_GDS_GRAPH") ?? "personal-memory-context";
        var parameters = With(ScopeOf(scope), ("graphName", graphName), ("limit", Clamp(limit, 1, 100)));
        await using var session = Session();
        try
        {
            var exists = await Rows(session, "CALL gds.graph.exists($graphName) YIELD exists RETURN exists", parameters);
            if (exists.Count > 0 && Json.Flag(exists[0]["exists"]))
            {
                var ranked = await Rows(session, """
                    CALL gds.pageRank.stream($graphName)
                    YIELD nodeId, score
                    WITH gds.util.asNode(nodeId) AS node, score
                    WHERE node:Entity
                      AND ($userId IS NULL OR node.userId IS NULL OR node.userId = $userId)
                      AND ($projectId IS NULL OR node.projectId IS NULL OR node.projectId = $projectId)
                      AND ($sessionId IS NULL OR node.sessionId IS NULL OR node.sessionId = $sessionId)
                      AND ($includeTestData OR coalesce(node.source, '') <> 'test')
                    RETURN node.id AS id, node.name AS label, node.type AS type, score
                    ORDER BY score DESC LIMIT $limit
                    """, parameters);
                return Hubs("gds-pagerank", ranked);
            }
        }
        catch (Neo4jException)
        {
            // GDS is optional; the degree count below works everywhere.
        }
        var counted = await Rows(session, """
            MATCH (n)
            WHERE (n:Entity OR n:Memory OR n:Claim OR n:Procedure)
              AND ($userId IS NULL OR n.userId = $userId)
              AND ($projectId IS NULL OR n.projectId = $projectId)
              AND ($sessionId IS NULL OR n.sessionId = $sessionId)
              AND ($includeTestData OR coalesce(n.source, '') <> 'test')
            OPTIONAL MATCH (n)-[r]-()
            RETURN labels(n)[0] AS type, n.id AS id, coalesce(n.name, n.title, n.content, n.statement) AS label,
                   count(r) AS degree
            ORDER BY degree DESC, label ASC LIMIT $limit
            """, parameters);
        return Hubs("cypher-degree", counted);

        JsonObject Hubs(string method, List<JsonObject> hubs) =>
            new() { ["method"] = method, ["graphName"] = graphName, ["hubs"] = new JsonArray([.. hubs]) };
    }

    public async Task EnsureSchema()
    {
        var dimensions = Env.Number("EMBEDDING_DIMENSIONS", EmbeddingClient.DefaultDimensions);
        if (dimensions != Math.Floor(dimensions) || dimensions < 32 || dimensions > 4096)
            throw new InvalidOperationException("EMBEDDING_DIMENSIONS must be an integer between 32 and 4096");
        await using (var check = Session())
        {
            // A vector index keeps its size for good. Refuse to run against one of another size
            // instead of failing on the first write.
            var indexes = await Rows(check, """
                SHOW INDEXES YIELD name, options
                WHERE name IN ['memory_embedding', 'chunk_embedding']
                RETURN name, options.indexConfig['vector.dimensions'] AS dimensions
                """);
            foreach (var index in indexes)
            {
                var existing = Json.Num(index["dimensions"], -1);
                if (existing > 0 && existing != dimensions)
                    throw new InvalidOperationException(
                        $"vector index {Json.Str(index["name"])} holds {existing} dimensions but EMBEDDING_DIMENSIONS is {dimensions}. " +
                        "Vectors cannot change size in place: drop the index and re-embed the memories before changing it.");
            }
        }
        string[] statements =
        [
            "CREATE CONSTRAINT event_id IF NOT EXISTS FOR (e:Event) REQUIRE e.id IS UNIQUE",
            "CREATE CONSTRAINT memory_id IF NOT EXISTS FOR (m:Memory) REQUIRE m.id IS UNIQUE",
            "CREATE CONSTRAINT entity_id IF NOT EXISTS FOR (e:Entity) REQUIRE e.id IS UNIQUE",
            "CREATE CONSTRAINT document_id IF NOT EXISTS FOR (d:Document) REQUIRE d.id IS UNIQUE",
            "CREATE CONSTRAINT chunk_id IF NOT EXISTS FOR (c:Chunk) REQUIRE c.id IS UNIQUE",
            "CREATE CONSTRAINT claim_id IF NOT EXISTS FOR (c:Claim) REQUIRE c.id IS UNIQUE",
            "CREATE CONSTRAINT procedure_id IF NOT EXISTS FOR (p:Procedure) REQUIRE p.id IS UNIQUE",
            "CREATE CONSTRAINT job_id IF NOT EXISTS FOR (j:Job) REQUIRE j.id IS UNIQUE",
            "CREATE CONSTRAINT conversation_id IF NOT EXISTS FOR (c:Conversation) REQUIRE c.id IS UNIQUE",
            "CREATE CONSTRAINT message_id IF NOT EXISTS FOR (m:Message) REQUIRE m.id IS UNIQUE",
            "CREATE CONSTRAINT reasoning_trace_id IF NOT EXISTS FOR (r:ReasoningTrace) REQUIRE r.id IS UNIQUE",
            // RANGE indexes cannot hold the large text of events and memories.
            // Dropping them repairs a database made by schema version 1.
            "DROP INDEX event_content IF EXISTS",
            "DROP INDEX memory_content IF EXISTS",
            "CREATE INDEX entity_name IF NOT EXISTS FOR (e:Entity) ON (e.name)",
            "CREATE INDEX event_occurred_at IF NOT EXISTS FOR (e:Event) ON (e.occurredAt)",
            "CREATE INDEX event_scope IF NOT EXISTS FOR (e:Event) ON (e.userId, e.projectId, e.sessionId)",
            "CREATE INDEX conversation_session IF NOT EXISTS FOR (c:Conversation) ON (c.sessionId)",
            "CREATE INDEX message_occurred_at IF NOT EXISTS FOR (m:Message) ON (m.occurredAt)",
            "CREATE INDEX memory_validity IF NOT EXISTS FOR (m:Memory) ON (m.validFrom, m.validUntil)",
            "CREATE INDEX memory_scope IF NOT EXISTS FOR (m:Memory) ON (m.userId, m.projectId, m.sessionId)",
            "CREATE INDEX chunk_scope IF NOT EXISTS FOR (c:Chunk) ON (c.userId, c.projectId, c.sessionId)",
            "CREATE INDEX claim_scope IF NOT EXISTS FOR (c:Claim) ON (c.userId, c.projectId, c.sessionId)",
            "CREATE INDEX procedure_scope IF NOT EXISTS FOR (p:Procedure) ON (p.userId, p.projectId, p.sessionId)",
            "CREATE FULLTEXT INDEX event_fulltext IF NOT EXISTS FOR (e:Event) ON EACH [e.content]",
            "CREATE FULLTEXT INDEX memory_fulltext IF NOT EXISTS FOR (m:Memory) ON EACH [m.content]",
            "CREATE FULLTEXT INDEX procedure_fulltext IF NOT EXISTS FOR (p:Procedure) ON EACH [p.title, p.goal]",
            "CREATE FULLTEXT INDEX procedure_fulltext_v2 IF NOT EXISTS FOR (p:Procedure) ON EACH [p.title, p.goal, p.steps, p.preconditions, p.knownFailures]",
            "CREATE FULLTEXT INDEX claim_fulltext IF NOT EXISTS FOR (c:Claim) ON EACH [c.statement, c.predicate]",
            "CREATE FULLTEXT INDEX chunk_fulltext IF NOT EXISTS FOR (c:Chunk) ON EACH [c.content]",
            $"CREATE VECTOR INDEX memory_embedding IF NOT EXISTS FOR (m:Memory) ON m.embedding OPTIONS {{ indexConfig: {{ `vector.dimensions`: {(int)dimensions}, `vector.similarity_function`: 'cosine' }} }}",
            $"CREATE VECTOR INDEX chunk_embedding IF NOT EXISTS FOR (c:Chunk) ON c.embedding OPTIONS {{ indexConfig: {{ `vector.dimensions`: {(int)dimensions}, `vector.similarity_function`: 'cosine' }} }}",
        ];
        await using var session = Session();
        foreach (var statement in statements) await Rows(session, statement);
        var stored = await Rows(session, """
            MERGE (s:SchemaMetadata {name: 'personal-memory'})
            ON CREATE SET s.version = $version, s.createdAt = datetime(), s.appliedMigrations = [$migration]
            SET s.checkedAt = datetime()
            RETURN s.version AS version
            """, P(("version", (long)SchemaVersion), ("migration", SchemaMigrationId)));
        var storedVersion = Json.Num(stored.FirstOrDefault()?["version"]);
        var recorded = await Rows(session, """
            MATCH (s:SchemaMetadata {name: 'personal-memory'})
            RETURN s.embeddingModel AS model
            """);
        var recordedModel = Json.Str(recorded.FirstOrDefault()?["model"], "");
        if (recordedModel.Length > 0 && recordedModel != embeddings.Model)
            throw new InvalidOperationException(
                $"the stored vectors were made by {recordedModel} but EMBEDDING_MODEL is {embeddings.Model}. " +
                "Vectors from two models cannot be compared. Re-embed before changing the model.");
        if (recordedModel.Length == 0 && embeddings.Enabled)
            await Rows(session, """
                MATCH (s:SchemaMetadata {name: 'personal-memory'})
                SET s.embeddingModel = $model, s.embeddingDimensions = $dimensions
                """, P(("model", embeddings.Model), ("dimensions", (long)dimensions)));

        if (storedVersion > SchemaVersion)
            throw new InvalidOperationException($"database schema version {storedVersion} is newer than supported version {SchemaVersion}");
        if (storedVersion < SchemaVersion)
            await Rows(session, """
                MATCH (s:SchemaMetadata {name: 'personal-memory'})
                SET s.version = $version,
                    s.checkedAt = datetime(),
                    s.appliedMigrations = coalesce(s.appliedMigrations, []) + $migration
                """, P(("version", (long)SchemaVersion), ("migration", SchemaMigrationId)));
    }

    // ---- events ----

    public async Task<JsonObject> AppendEvent(MemoryEvent input) => (await AppendEvents([input]))[0];

    /// Writes events and queues the work that follows from them. Writing an
    /// event again with the same id is harmless; writing different content
    /// under an id already used is refused, and then none of the batch is kept.
    public async Task<List<JsonObject>> AppendEvents(IReadOnlyList<MemoryEvent> inputs)
    {
        foreach (var input in inputs) ContentLimits.Validate(input.Content);
        var events = inputs.Select(input => new Dictionary<string, object?>
        {
            ["id"] = input.Id ?? input.IdempotencyKey ?? NewId(),
            ["kind"] = input.Kind,
            ["content"] = input.Content,
            ["source"] = input.Source ?? "unknown",
            ["metadata"] = Metadata(input.Metadata),
            ["occurredAt"] = input.OccurredAt ?? Now(),
            ["userId"] = input.UserId,
            ["projectId"] = input.ProjectId,
            ["sessionId"] = input.SessionId,
        }).ToList();

        var stored = await Write(async tx =>
        {
            var existing = await Rows(tx, """
                UNWIND $events AS event
                OPTIONAL MATCH (e:Event {id: event.id})
                RETURN event.id AS id, event.content AS incomingContent, event.kind AS incomingKind,
                       e.content AS storedContent, e.kind AS storedKind
                """, P(("events", events)));
            foreach (var row in existing)
                if (row["storedContent"] is not null
                    && (Json.Str(row["storedContent"]) != Json.Str(row["incomingContent"]) || Json.Str(row["storedKind"]) != Json.Str(row["incomingKind"])))
                    throw new InvalidOperationException($"idempotency conflict for event {Json.Str(row["id"])}");
            return await Rows(tx, """
                UNWIND $events AS event
                MERGE (e:Event {id: event.id})
                ON CREATE SET e += event, e.ingestCount = 1
                ON MATCH SET e.ingestCount = coalesce(e.ingestCount, 0) + 1
                FOREACH (id IN CASE WHEN e.userId IS NULL THEN [] ELSE [e.userId] END |
                  MERGE (u:User {id: id}) MERGE (u)-[:GENERATED]->(e))
                FOREACH (id IN CASE WHEN e.projectId IS NULL THEN [] ELSE [e.projectId] END |
                  MERGE (p:Project {id: id}) MERGE (p)-[:CONTAINS_EVENT]->(e))
                FOREACH (_ IN CASE WHEN e.sessionId IS NULL THEN [] ELSE [1] END |
                  MERGE (c:Conversation {id: e.sessionId})
                  ON CREATE SET c.sessionId = e.sessionId, c.createdAt = e.occurredAt
                  SET c.updatedAt = e.occurredAt, c.userId = e.userId, c.projectId = e.projectId
                  MERGE (m:Message {id: e.id})
                  ON CREATE SET m.role = CASE e.kind WHEN 'user_message' THEN 'user' WHEN 'tool_call' THEN 'tool' WHEN 'tool_result' THEN 'tool' ELSE 'assistant' END,
                                m.kind = e.kind, m.content = e.content, m.occurredAt = e.occurredAt,
                                m.userId = e.userId, m.projectId = e.projectId, m.sessionId = e.sessionId
                  MERGE (c)-[:HAS_MESSAGE]->(m)
                  MERGE (m)-[:REPRESENTS]->(e))
                FOREACH (_ IN CASE WHEN e.kind = 'decision' THEN [1] ELSE [] END |
                  MERGE (r:ReasoningTrace {id: e.id})
                  ON CREATE SET r.content = e.content, r.occurredAt = e.occurredAt, r.sessionId = e.sessionId,
                                r.userId = e.userId, r.projectId = e.projectId
                  MERGE (r)-[:DERIVED_FROM]->(e))
                MERGE (j:Job {id: 'consolidate-event:' + e.id})
                ON CREATE SET j.kind = 'consolidate_event', j.targetId = e.id, j.status = 'queued',
                              j.approvalStatus = $approvalStatus, j.attempts = 0, j.availableAt = e.occurredAt, j.createdAt = datetime()
                FOREACH (_ IN CASE WHEN e.sessionId IS NULL THEN [] ELSE [1] END |
                  MERGE (rj:Job {id: 'reflect-session:' + e.sessionId})
                  ON CREATE SET rj.kind = 'reflect_session', rj.targetId = e.sessionId, rj.attempts = 0, rj.createdAt = datetime(), rj.approvalStatus = $approvalStatus
                  SET rj.status = 'queued', rj.availableAt = datetime() + duration({seconds: $reflectionDelaySeconds}))
                RETURN e.id AS id, e.content AS storedContent, e.kind AS storedKind
                """, P(("events", events),
                    ("reflectionDelaySeconds", (long)Env.Number("REFLECTION_DELAY_SECONDS", 300)),
                    ("approvalStatus", Env.Get("CONSOLIDATION_REQUIRE_APPROVAL") == "1" ? "pending" : "approved")));
        });

        var storedById = stored.ToDictionary(row => Json.Str(row["id"]));
        foreach (var e in events)
            if (storedById.TryGetValue((string)e["id"]!, out var row)
                && (Json.Str(row["storedContent"]) != (string)e["content"]! || Json.Str(row["storedKind"]) != (string)e["kind"]!))
                throw new InvalidOperationException($"idempotency conflict for event {e["id"]}");
        return [.. events.Select((e, index) =>
        {
            var written = new JsonObject(e.Select(field => KeyValuePair.Create(field.Key, ToJson(field.Value))));
            written["idempotent"] = inputs[index].Id is not null || inputs[index].IdempotencyKey is not null;
            return written;
        })];
    }

    private const string EventFields = """
        e.id AS id, e.kind AS kind, e.content AS content, e.source AS source,
        e.metadata AS metadata, e.occurredAt AS occurredAt, e.userId AS userId,
        e.projectId AS projectId, e.sessionId AS sessionId
        """;

    private static MemoryEvent ToEvent(JsonObject row) => new()
    {
        Id = row["id"] is null ? null : Json.Str(row["id"]),
        Kind = Json.Str(row["kind"]),
        Content = Json.Str(row["content"]),
        Source = row["source"] is null ? null : Json.Str(row["source"]),
        Metadata = ParseMetadata(row["metadata"]),
        OccurredAt = row["occurredAt"] is null ? null : Json.Str(row["occurredAt"]),
        UserId = row["userId"] is null ? null : Json.Str(row["userId"]),
        ProjectId = row["projectId"] is null ? null : Json.Str(row["projectId"]),
        SessionId = row["sessionId"] is null ? null : Json.Str(row["sessionId"]),
    };

    public async Task<MemoryEvent?> GetEvent(string id, MemoryScope? scope = null) =>
        await Row($"""
            MATCH (e:Event {"{id: $id}"})
            WHERE ($userId IS NULL OR e.userId = $userId)
              AND ($projectId IS NULL OR e.projectId = $projectId)
              AND ($sessionId IS NULL OR e.sessionId = $sessionId)
            RETURN {EventFields}
            """, With(ScopeOf(scope), ("id", id))) is { } row ? ToEvent(row) : null;

    public async Task<List<MemoryEvent>> GetSessionEvents(string sessionId, int limit = 200) =>
        [.. (await Rows($"""
            MATCH (e:Event {"{sessionId: $sessionId}"})
            RETURN {EventFields}
            ORDER BY datetime(e.occurredAt) ASC, e.id ASC LIMIT $limit
            """, P(("sessionId", sessionId), ("limit", Clamp(limit, 1, 1000))))).Select(ToEvent)];

    // ---- jobs ----

    /// Takes the next job that is due, or one whose lease ran out because
    /// the worker holding it died.
    public async Task<MemoryJob?> ClaimNextJob(string? targetId = null, IReadOnlyList<string>? skipKinds = null, bool requireApproval = false)
    {
        var rows = await Write(tx => Rows(tx, """
            CALL {
              MATCH (j:Job)
              WHERE ($targetId IS NULL OR j.targetId = $targetId)
                AND ((j.status = 'queued' AND (j.availableAt IS NULL OR datetime(j.availableAt) <= datetime()))
                   OR (j.status = 'running' AND j.leaseUntil IS NOT NULL AND datetime(j.leaseUntil) <= datetime()))
                AND NOT j.kind IN $skipKinds
                AND ($requireApproval = false OR coalesce(j.approvalStatus, 'approved') = 'approved')
              WITH j ORDER BY j.availableAt ASC, j.createdAt ASC LIMIT 1
              SET j.status = 'running', j.attempts = coalesce(j.attempts, 0) + 1, j.startedAt = datetime(),
                  j.leaseUntil = datetime() + duration({seconds: $leaseSeconds})
              RETURN j
            }
            RETURN j.id AS id, j.kind AS kind, j.targetId AS targetId,
                   j.status AS status, j.availableAt AS availableAt, j.attempts AS attempts,
                   j.leaseUntil AS leaseUntil, j.error AS error, j.approvalStatus AS approvalStatus, j.proposal AS proposal
            """, P(("targetId", targetId), ("skipKinds", (skipKinds ?? []).ToList()), ("requireApproval", requireApproval),
                ("leaseSeconds", (long)Env.Number("JOB_LEASE_SECONDS", 300)))));
        if (rows.FirstOrDefault() is not { } row) return null;
        string? Text(string key) => row[key] is null ? null : Json.Str(row[key]);
        return new MemoryJob
        {
            Id = Json.Str(row["id"]), Kind = Json.Str(row["kind"]), TargetId = Json.Str(row["targetId"]),
            Status = Text("status"), AvailableAt = Text("availableAt"), Attempts = (long)Json.Num(row["attempts"]),
            LeaseUntil = Text("leaseUntil"), Error = Text("error"), ApprovalStatus = Text("approvalStatus"), Proposal = Text("proposal"),
        };
    }

    public Task<List<JsonObject>> ListJobs(string? status = null, int limit = 100) => Rows("""
        MATCH (j:Job)
        OPTIONAL MATCH (e:Event {id: j.targetId})
        WHERE $status IS NULL OR j.status = $status
        RETURN j.id AS id, j.kind AS kind, j.targetId AS targetId, j.status AS status, j.approvalStatus AS approvalStatus, j.proposal AS proposal,
               j.availableAt AS availableAt, j.attempts AS attempts, j.leaseUntil AS leaseUntil,
               j.error AS error, j.createdAt AS createdAt, j.startedAt AS startedAt,
               j.completedAt AS completedAt, e.kind AS eventKind, e.source AS eventSource,
               e.content AS eventContent, e.occurredAt AS eventOccurredAt
        ORDER BY coalesce(j.createdAt, j.availableAt) DESC
        LIMIT $limit
        """, P(("status", status), ("limit", Clamp(limit, 1, 200))));

    public Task<JsonObject?> ApproveJob(string id) => Row("""
        MATCH (j:Job {id: $id})
        SET j.approvalStatus = 'approved', j.status = CASE WHEN j.status = 'failed' AND j.approvalStatus = 'rejected' THEN 'queued' ELSE j.status END,
            j.availableAt = datetime(), j.rejectedAt = null
        RETURN j.id AS id, j.status AS status, j.approvalStatus AS approvalStatus
        """, P(("id", id)));

    /// A job together with the event it is about, for review before approval.
    public async Task<JsonObject?> GetJobEvent(string id)
    {
        var row = await Row("""
            MATCH (j:Job {id: $id}) OPTIONAL MATCH (e:Event {id: j.targetId})
            RETURN j.id AS id, j.kind AS kind, j.targetId AS targetId, j.status AS status,
                   j.approvalStatus AS approvalStatus, j.proposal AS proposal,
                   e.id AS eventId, e.kind AS eventKind, e.content AS content, e.source AS source,
                   e.metadata AS metadata, e.occurredAt AS occurredAt, e.userId AS userId,
                   e.projectId AS projectId, e.sessionId AS sessionId
            """, P(("id", id)));
        if (row is null) return null;
        var metadata = ParseMetadata(row["metadata"]);
        row["metadata"] = metadata?.DeepClone();
        row["event"] = row["eventId"] is null ? null : new JsonObject
        {
            ["id"] = row["eventId"]!.DeepClone(), ["kind"] = row["eventKind"]?.DeepClone(), ["content"] = row["content"]?.DeepClone(),
            ["source"] = row["source"]?.DeepClone(), ["metadata"] = metadata, ["occurredAt"] = row["occurredAt"]?.DeepClone(),
            ["userId"] = row["userId"]?.DeepClone(), ["projectId"] = row["projectId"]?.DeepClone(), ["sessionId"] = row["sessionId"]?.DeepClone(),
        };
        return row;
    }

    public Task<JsonObject?> SaveJobProposal(string id, JsonNode? proposal) => Row("""
        MATCH (j:Job {id: $id}) SET j.proposal = $proposal, j.proposalAt = datetime()
        RETURN j.id AS id, j.approvalStatus AS approvalStatus, j.proposal AS proposal
        """, P(("id", id), ("proposal", proposal?.ToJsonString(Json.Options) ?? "null")));

    public Task<JsonObject?> RejectJob(string id, string? reason = null) => Row("""
        MATCH (j:Job {id: $id})
        SET j.approvalStatus = 'rejected', j.status = 'failed', j.error = $reason, j.rejectedAt = datetime(), j.leaseUntil = null
        RETURN j.id AS id, j.status AS status, j.approvalStatus AS approvalStatus, j.error AS error
        """, P(("id", id), ("reason", reason ?? "rejected during review")));

    public async Task<JsonObject> CompleteJob(string id, string? error = null)
    {
        var status = error is null ? "completed" : "failed";
        await Rows("""
            MATCH (j:Job {id: $id})
            SET j.status = $status, j.completedAt = datetime(), j.error = $error
            """, P(("id", id), ("status", status), ("error", error)));
        return new JsonObject { ["id"] = id, ["status"] = status };
    }

    /// Puts a failed job back in the queue with a doubling delay, until it
    /// has been tried too often; then it stays failed.
    public async Task<JsonObject> RetryJob(string id, string error, int? maxAttempts = null)
    {
        var max = maxAttempts ?? (int)Env.Number("JOB_MAX_ATTEMPTS", 5);
        await using var session = Session();
        var current = await Rows(session, "MATCH (j:Job {id: $id}) RETURN coalesce(j.attempts, 0) AS attempts", P(("id", id)));
        if (current.Count == 0) return new JsonObject { ["status"] = "missing" };
        var attempts = Json.Num(current[0]["attempts"]);
        var backoff = Env.Number("JOB_RETRY_BACKOFF_SECONDS", 5) * Math.Pow(2, Math.Max(0, attempts - 1));
        var rows = await Rows(session, """
            MATCH (j:Job {id: $id})
            WITH j, coalesce(j.attempts, 0) AS attempts
            SET j.error = $error,
                j.status = CASE WHEN attempts < $maxAttempts THEN 'queued' ELSE 'failed' END,
                j.availableAt = CASE WHEN attempts < $maxAttempts THEN datetime() + duration({seconds: $backoff}) ELSE j.availableAt END,
                j.leaseUntil = null,
                j.completedAt = CASE WHEN attempts < $maxAttempts THEN null ELSE datetime() END
            RETURN j.status AS status, j.attempts AS attempts
            """, P(("id", id), ("error", error), ("maxAttempts", (long)max), ("backoff", (long)backoff)));
        return rows.FirstOrDefault() ?? new JsonObject { ["status"] = "missing" };
    }

    public Task<JsonObject?> RequeueFailedJob(string id) => Row("""
        MATCH (j:Job {id: $id})
        WHERE j.status = 'failed'
        SET j.status = 'queued', j.attempts = 0, j.availableAt = datetime(),
            j.error = null, j.completedAt = null, j.leaseUntil = null
        RETURN j.id AS id, j.status AS status
        """, P(("id", id)));

    // ---- memories ----

    private async Task<string?> NearestDuplicate(CuratedMemory input, IReadOnlyList<double> embedding)
    {
        await using var session = Session();
        var rows = await Rows(session, """
            CALL db.index.vector.queryNodes('memory_embedding', 5, $embedding) YIELD node, score
            WHERE score >= $threshold AND node.status = 'active'
              AND coalesce(node.userId, '') = coalesce($userId, '')
              AND coalesce(node.projectId, '') = coalesce($projectId, '')
              AND coalesce(node.sessionId, '') = coalesce($sessionId, '')
            RETURN node.id AS id
            ORDER BY score DESC
            LIMIT 1
            """, P(("embedding", embedding.ToList()), ("threshold", DuplicateSimilarity),
                   ("userId", input.UserId), ("projectId", input.ProjectId), ("sessionId", input.SessionId)));
        return rows.FirstOrDefault()?["id"] is { } id ? Json.Str(id) : null;
    }

    /// A memory at least this similar (cosine) to an active one in the same scope is the same memory.
    public const double DuplicateSimilarity = 0.95;

    /// Saves a memory with its sources and the entities it is about. A memory
    /// that says something else about the same subject and predicate is
    /// linked as a contradiction, and one that agrees as a reinforcement;
    /// neither is removed. The answer lists those links as "conflicts".
    public async Task<JsonObject> CreateMemory(CuratedMemory input)
    {
        var embedding = embeddings.Enabled ? (await embeddings.EmbedDocuments([input.Content]))[0].ToList() : null;
        // Consolidating the same event twice, or a paraphrase of it, must not store a second copy:
        // the nearest active memory in scope is reinforced instead.
        var existing = input.Id is null && embedding is not null
            ? await NearestDuplicate(input, embedding) : null;
        var memory = new Dictionary<string, object?>
        {
            ["id"] = input.Id ?? existing ?? ContentMemoryId(input, input.Content),
            ["content"] = input.Content,
            ["category"] = input.Category ?? "fact",
            ["status"] = input.Status ?? "active",
            ["confidence"] = input.Confidence ?? 1,
            ["metadata"] = Metadata(input.Metadata),
            ["subject"] = input.Subject,
            ["predicate"] = input.Predicate,
            ["object"] = input.Object,
            ["embedding"] = embedding,
            ["embeddingModel"] = embedding is null ? null : embeddings.Model,
            ["validFrom"] = input.ValidFrom ?? Now(),
            ["validUntil"] = input.ValidUntil,
            ["userId"] = input.UserId,
            ["projectId"] = input.ProjectId,
            ["sessionId"] = input.SessionId,
        };
        var id = (string)memory["id"]!;
        var conflicts = await Write(async tx =>
        {
            await Rows(tx, """
                MERGE (m:Memory {id: $memory.id})
                ON CREATE SET m += $memory, m.reinforcementCount = 0
                ON MATCH SET m += $memory,
                           m.reinforcementCount = coalesce(m.reinforcementCount, 0) + 1
                """, P(("memory", memory)));
            await Rows(tx, """
                UNWIND $sourceEventIds AS eventId
                MATCH (m:Memory {id: $memoryId}), (e:Event {id: eventId})
                MERGE (m)-[:SUPPORTED_BY]->(e)
                """, P(("memoryId", id), ("sourceEventIds", (input.SourceEventIds ?? []).ToList())));
            await Rows(tx, """
                MATCH (m:Memory {id: $memoryId})
                UNWIND $entityIds AS entityId
                MATCH (e:Entity {id: entityId})
                MERGE (m)-[r:ABOUT]->(e)
                SET r.weight = 1.0
                """, P(("memoryId", id), ("entityIds", (input.EntityIds ?? []).ToList())));
            if (input.SupersedesMemoryId is not null)
                await Rows(tx, """
                    MATCH (new:Memory {id: $newId}), (old:Memory {id: $oldId})
                    SET old.status = 'superseded', old.validUntil = coalesce(old.validUntil, $validFrom)
                    MERGE (new)-[:SUPERSEDES]->(old)
                    """, P(("newId", id), ("oldId", input.SupersedesMemoryId), ("validFrom", memory["validFrom"])));
            var found = await Rows(tx, """
                MATCH (m:Memory {id: $memoryId}), (other:Memory)
                WHERE other.id <> m.id
                  AND m.status = 'active' AND other.status = 'active'
                  AND m.subject IS NOT NULL AND m.predicate IS NOT NULL
                  AND other.subject = m.subject AND other.predicate = m.predicate
                  AND ((m.userId IS NULL AND other.userId IS NULL) OR other.userId = m.userId)
                  AND ((m.projectId IS NULL AND other.projectId IS NULL) OR other.projectId = m.projectId)
                WITH m, other, CASE WHEN coalesce(m.object, '') = coalesce(other.object, '') THEN 'reinforces' ELSE 'contradicts' END AS relation
                FOREACH (_ IN CASE WHEN relation = 'contradicts' THEN [1] ELSE [] END |
                  MERGE (m)-[r:CONTRADICTS]->(other)
                  SET r.weight = 0.8, m.contradictionStatus = 'contested', other.contradictionStatus = 'contested')
                FOREACH (_ IN CASE WHEN relation = 'reinforces' THEN [1] ELSE [] END |
                  MERGE (m)-[r:REINFORCES]->(other)
                  SET r.weight = 0.6)
                RETURN collect({id: other.id, relation: relation}) AS conflicts
                """, P(("memoryId", id)));
            await Rows(tx, """
                MERGE (j:Job {id: $jobId})
                ON CREATE SET j.kind = 'reconsolidate_memory', j.targetId = $memoryId,
                              j.status = 'queued', j.attempts = 0,
                              j.availableAt = datetime(), j.createdAt = datetime()
                """, P(("jobId", $"reconsolidate-memory:{id}"), ("memoryId", id)));
            return found.FirstOrDefault()?["conflicts"]?.DeepClone() ?? new JsonArray();
        });
        var saved = new JsonObject(memory.Select(field => KeyValuePair.Create(field.Key, ToJson(field.Value))));
        saved["conflicts"] = conflicts;
        return saved;
    }

    public Task<List<JsonObject>> ListMemories(MemoryScope? scope = null, int limit = 50, string? status = null) => Rows("""
        MATCH (m:Memory)
        WHERE ($userId IS NULL OR m.userId = $userId)
          AND ($projectId IS NULL OR m.projectId = $projectId)
          AND ($sessionId IS NULL OR m.sessionId = $sessionId)
          AND ($status IS NULL OR m.status = $status)
        OPTIONAL MATCH (m)-[:SUPPORTED_BY]->(e:Event)
        WITH m, collect(e.id)[0..10] AS sourceEventIds
        OPTIONAL MATCH (m)-[:SUPERSEDES]->(old:Memory)
        WITH m, sourceEventIds, collect(old.id)[0] AS supersedesMemoryId
        OPTIONAL MATCH (newer:Memory)-[:SUPERSEDES]->(m)
        WITH m, sourceEventIds, supersedesMemoryId, collect(newer.id) AS supersededByIds
        RETURN m.id AS id, m.content AS content, m.category AS category, m.status AS status,
               m.confidence AS confidence, m.metadata AS metadata, m.subject AS subject,
               m.predicate AS predicate, m.object AS object, m.validFrom AS validFrom,
               m.validUntil AS validUntil, m.userId AS userId, m.projectId AS projectId,
               m.sessionId AS sessionId, sourceEventIds, supersedesMemoryId, supersededByIds
        ORDER BY m.validFrom DESC
        LIMIT $limit
        """, With(ScopeOf(scope), ("status", status), ("limit", Clamp(limit, 1, 200))));

    /// What depends on a memory: the events behind it and what replaced it.
    public Task<JsonObject?> MemoryImpact(string id) => Row("""
        MATCH (m:Memory {id: $id})
        OPTIONAL MATCH (m)-[:SUPPORTED_BY]->(source:Event)
        OPTIONAL MATCH (replacement:Memory)-[:SUPERSEDES]->(m)
        RETURN m.id AS id, m.status AS status,
               collect(DISTINCT source.id) AS sourceEventIds,
               collect(DISTINCT replacement.id) AS supersededByIds
        """, P(("id", id)));

    public Task<JsonObject?> RetractMemory(string id, string? reason = null) => Row("""
        MATCH (m:Memory {id: $id})
        SET m.status = 'retracted', m.retractedAt = datetime(), m.retractionReason = $reason
        RETURN m.id AS id, m.status AS status
        """, P(("id", id), ("reason", reason)));

    /// Records whether a recalled memory actually helped, which later raises
    /// or lowers its rank.
    public async Task<JsonObject?> RecordMemoryUtility(string id, bool useful, string? feedback = null) =>
        (await Write(tx => Rows(tx, """
            MATCH (m:Memory {id: $id})
            SET m.utilityUses = coalesce(m.utilityUses, 0) + 1,
                m.utilitySuccesses = coalesce(m.utilitySuccesses, 0) + CASE WHEN $useful THEN 1 ELSE 0 END,
                m.lastUsedAt = datetime(),
                m.lastUtilityFeedback = CASE WHEN $feedback IS NULL THEN m.lastUtilityFeedback ELSE $feedback END
            RETURN m.id AS id, m.utilityUses AS utilityUses, m.utilitySuccesses AS utilitySuccesses,
                   toFloat(m.utilitySuccesses) / CASE WHEN m.utilityUses = 0 THEN 1 ELSE m.utilityUses END AS utilityScore
            """, P(("id", id), ("useful", useful), ("feedback", feedback))))).FirstOrDefault();

    public Task<JsonObject?> ArchiveMemory(string id, string? reason = null) => Row("""
        MATCH (m:Memory {id: $id})
        SET m.status = 'archived', m.archivedAt = datetime(), m.archiveReason = $reason
        RETURN m.id AS id, m.status AS status, m.archivedAt AS archivedAt, m.archiveReason AS archiveReason
        """, P(("id", id), ("reason", reason)));

    /// Looks again for memories that agree with or contradict this one, and
    /// links them. Returns the links as { id, relation }.
    public async Task<JsonArray> ReconsolidateMemory(string memoryId)
    {
        var rows = await Write(tx => Rows(tx, """
            MATCH (m:Memory {id: $memoryId})
            WITH m, m.userId AS memoryUserId, m.projectId AS memoryProjectId
            MATCH (other:Memory)
            WHERE other.id <> m.id
              AND other.status = 'active'
              AND m.status = 'active'
              AND m.subject IS NOT NULL AND m.predicate IS NOT NULL
              AND other.subject = m.subject AND other.predicate = m.predicate
              AND ((memoryUserId IS NULL AND other.userId IS NULL) OR other.userId = memoryUserId)
              AND ((memoryProjectId IS NULL AND other.projectId IS NULL) OR other.projectId = memoryProjectId)
            WITH m, other,
                 CASE WHEN coalesce(m.object, '') = coalesce(other.object, '') THEN 'reinforces' ELSE 'contradicts' END AS relation
            FOREACH (_ IN CASE WHEN relation = 'contradicts' THEN [1] ELSE [] END |
              MERGE (m)-[:CONTRADICTS]->(other)
              SET m.contradictionStatus = 'contested', other.contradictionStatus = 'contested')
            FOREACH (_ IN CASE WHEN relation = 'reinforces' THEN [1] ELSE [] END |
              MERGE (m)-[:REINFORCES]->(other))
            RETURN collect({id: other.id, relation: relation}) AS links
            """, P(("memoryId", memoryId))));
        return rows.FirstOrDefault()?["links"]?.DeepClone() as JsonArray ?? [];
    }

    // ---- recall ----

    private const string MemoryIsCurrent = """
        NOT node.status IN ['retracted', 'archived']
          AND ($includeHistorical OR node.status = 'active')
          AND ($includeHistorical OR (node.validFrom IS NULL OR datetime(node.validFrom) <= datetime()) AND (node.validUntil IS NULL OR datetime(node.validUntil) > datetime()))
          AND ($userId IS NULL OR node.userId = $userId)
          AND ($projectId IS NULL OR node.projectId = $projectId)
          AND ($sessionId IS NULL OR node.sessionId = $sessionId)
          AND ($includeTestData OR NOT EXISTS { MATCH (node)-[:SUPPORTED_BY]->(:Event {source: 'test'}) })
        """;

    private const string MemoryRow = """
        coalesce(node.reinforcementCount, 0) AS reinforcementCount,
        toFloat(coalesce(node.utilitySuccesses, 0)) / CASE WHEN coalesce(node.utilityUses, 0) = 0 THEN 1 ELSE node.utilityUses END AS utilityScore,
        [(node)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
        [(node)-[:SUPPORTED_BY]->(source:Event) | {id: source.id, content: source.content}][0..10] AS evidence, score
        """;

    private const string ChunkRow = """
        RETURN 'chunk' AS type, node.id AS id, node.content AS content,
               'source_chunk' AS kind, null AS occurredAt, 'document' AS source,
               node.projectId AS projectId, 'active' AS status, null AS confidence,
               node.id AS sourceChunkId, [{id: node.id, content: node.content}] AS evidence, score
        ORDER BY score DESC LIMIT $limit
        """;

    /// Finds evidence for a question three ways (words, meaning, and the
    /// graph around the entities it names), lets each way vote by rank, and
    /// orders the result for the kind of question it is.
    public async Task<List<JsonObject>> Recall(string query, MemoryScope? scope = null, int limit = 12, string route = "semantic")
    {
        limit = (int)Clamp(limit == 0 ? 12 : limit, 1, 50);
        var plan = Retrieval.Plan(query, route);
        var methods = (scope?.RetrievalMethods ?? Vocabulary.RetrievalMethods).ToHashSet();
        var candidateLimit = (long)Math.Max(limit * plan.CandidateMultiplier, 20);
        var parameters = With(ScopeOf(scope), ("query", query), ("limit", candidateLimit));

        await using var session = Session();
        var lexical = new List<JsonObject>();
        lexical.AddRange(await Rows(session, """
            CALL db.index.fulltext.queryNodes('event_fulltext', $query) YIELD node, score
            WHERE ($userId IS NULL OR node.userId = $userId)
              AND ($projectId IS NULL OR node.projectId = $projectId)
              AND ($sessionId IS NULL OR node.sessionId = $sessionId)
              AND ($includeTestData OR coalesce(node.source, '') <> 'test')
            RETURN 'event' AS type, node.id AS id, node.content AS content, node.kind AS kind,
                   node.occurredAt AS occurredAt, node.source AS source, node.projectId AS projectId,
                   null AS status, null AS confidence,
                   [{id: node.id, content: node.content}] AS evidence, score
            ORDER BY score DESC LIMIT $limit
            """, parameters));
        lexical.AddRange(await Rows(session, $"""
            CALL db.index.fulltext.queryNodes('memory_fulltext', $query) YIELD node, score
            WHERE {MemoryIsCurrent}
            RETURN 'memory' AS type, node.id AS id, node.content AS content, node.category AS kind,
                   node.validFrom AS occurredAt, 'curated' AS source, node.projectId AS projectId,
                   node.status AS status, node.confidence AS confidence,
                   {MemoryRow}
            ORDER BY score DESC LIMIT $limit
            """, parameters));
        lexical.AddRange(await Rows(session, """
            CALL db.index.fulltext.queryNodes('procedure_fulltext_v2', $query) YIELD node, score
            WHERE ($userId IS NULL OR node.userId = $userId)
              AND ($projectId IS NULL OR node.projectId = $projectId)
              AND ($sessionId IS NULL OR node.sessionId = $sessionId)
              AND ($includeTestData OR NOT EXISTS { MATCH (node)-[:SUPPORTED_BY]->(:Event {source: 'test'}) })
            RETURN 'procedure' AS type, node.id AS id,
                   node.title + ': ' + node.goal + ' ' + coalesce(node.steps, '') AS content, 'procedure' AS kind,
                   null AS occurredAt, 'procedural' AS source, node.projectId AS projectId,
                   'active' AS status, node.confidence AS confidence,
                   [(node)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
                   [(node)-[:SUPPORTED_BY]->(source:Event) | {id: source.id, content: source.content}][0..10] AS evidence, score
            ORDER BY score DESC LIMIT $limit
            """, parameters));
        lexical.AddRange(await Rows(session, """
            CALL db.index.fulltext.queryNodes('claim_fulltext', $query) YIELD node, score
            WHERE ($userId IS NULL OR node.userId = $userId)
              AND ($projectId IS NULL OR node.projectId = $projectId)
              AND ($sessionId IS NULL OR node.sessionId = $sessionId)
              AND ($includeTestData OR (NOT EXISTS { MATCH (node)-[:SUPPORTED_BY]->(:Event {source: 'test'}) } AND NOT EXISTS { MATCH (node)-[:SUPPORTED_BY]->(:Chunk)<-[:HAS_CHUNK]-(:Document {source: 'test'}) }))
            RETURN 'claim' AS type, node.id AS id, node.statement AS content,
                   node.predicate AS kind, null AS occurredAt, 'claim' AS source,
                   node.projectId AS projectId, 'active' AS status,
                   node.confidence AS confidence,
                   [(node)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
                   [(node)-[:SUPPORTED_BY]->(source:Chunk) | source.id][0..10] AS sourceChunkIds,
                   ([(node)-[:SUPPORTED_BY]->(eventSource:Event) | {id: eventSource.id, content: eventSource.content}] + [(node)-[:SUPPORTED_BY]->(chunkSource:Chunk) | {id: chunkSource.id, content: chunkSource.content}])[0..10] AS evidence, score
            ORDER BY score DESC LIMIT $limit
            """, parameters));
        lexical.AddRange(await Rows(session, $"""
            CALL db.index.fulltext.queryNodes('chunk_fulltext', $query) YIELD node, score
            WHERE ($userId IS NULL OR node.userId = $userId)
              AND ($projectId IS NULL OR node.projectId = $projectId)
              AND ($includeTestData OR NOT EXISTS {"{ MATCH (node)<-[:HAS_CHUNK]-(:Document {source: 'test'}) }"})
            {ChunkRow}
            """, parameters));
        foreach (var candidate in lexical) candidate["retrievalMethod"] = "lexical";

        var graph = methods.Contains("graph") ? await GraphRecall(query, scope, Math.Min(plan.GraphLimit, limit * 2)) : [];

        var vector = new List<JsonObject>();
        // A simple question that words already answer well does not need the embedding call.
        var earlyStop = plan.Complexity == "simple" && lexical.Count >= limit && Json.Num(lexical.FirstOrDefault()?["score"]) >= 0.8;
        if (methods.Contains("vector") && embeddings.Enabled && !earlyStop)
        {
            var search = Env.Get("NEO4J_VECTOR_QUERY_MODE") == "search";
            string Nearest(string label, string index) => search
                ? $"""
                  MATCH (node:{label})
                  SEARCH node IN (VECTOR INDEX {index} FOR $embedding LIMIT $candidateLimit)
                  SCORE AS score
                  WHERE
                  """
                : $"""
                  CALL db.index.vector.queryNodes('{index}', $candidateLimit, $embedding) YIELD node, score
                  WHERE
                  """;
            var nearest = With(ScopeOf(scope), ("embedding", (await embeddings.EmbedQuery(query)).ToList()),
                ("candidateLimit", candidateLimit), ("limit", (long)limit));
            vector.AddRange(await Rows(session, $"""
                {Nearest("Memory", "memory_embedding")} {MemoryIsCurrent}
                RETURN 'memory' AS type, node.id AS id, node.content AS content,
                       node.category AS kind, node.validFrom AS occurredAt,
                       'vector' AS source, node.projectId AS projectId,
                       node.status AS status, node.confidence AS confidence,
                       {MemoryRow}
                ORDER BY score DESC LIMIT $limit
                """, nearest));
            vector.AddRange(await Rows(session, $"""
                {Nearest("Chunk", "chunk_embedding")} ($userId IS NULL OR node.userId = $userId)
                  AND ($projectId IS NULL OR node.projectId = $projectId)
                  AND ($includeTestData OR NOT EXISTS {"{ MATCH (node)<-[:HAS_CHUNK]-(:Document {source: 'test'}) }"})
                {ChunkRow}
                """, nearest));
            foreach (var candidate in vector) candidate["retrievalMethod"] = "vector";
        }

        static bool Identified(JsonObject candidate) =>
            candidate["type"]?.GetValueKind() == System.Text.Json.JsonValueKind.String && candidate["id"]?.GetValueKind() == System.Text.Json.JsonValueKind.String;
        IReadOnlyList<JsonObject> Use(string method, List<JsonObject> found) => methods.Contains(method) ? [.. found.Where(Identified)] : [];

        var candidates = RankFusion.Fuse(new Dictionary<string, IReadOnlyList<JsonObject>>
            {
                ["lexical"] = Use("lexical", lexical), ["vector"] = Use("vector", vector), ["graph"] = Use("graph", graph),
            }, RankFusion.Weights.For(route))
            .OrderByDescending(candidate => Retrieval.RouteScore(candidate, route))
            .Take(limit * 4)
            .ToList();

        // Benchmark data never leaks into a recall that did not ask for it.
        var benchmarkProjectId = Env.Get("BENCHMARK_PROJECT_ID") ?? "benchmark-suite";
        if (scope?.IncludeTestData != true && scope?.ProjectId is null)
            candidates = [.. candidates.Where(candidate => Json.Str(candidate["projectId"]) != benchmarkProjectId)];
        if (!reranker.Enabled) return [.. candidates.Take(limit)];

        var reranked = await reranker.Rerank(query, [.. candidates
            .Where(candidate => candidate["content"]?.GetValueKind() == System.Text.Json.JsonValueKind.String)
            .Select(candidate => new RerankCandidate($"{Json.Str(candidate["type"])}:{Json.Str(candidate["id"])}", Json.Str(candidate["content"]), Json.Num(candidate["score"])))]);
        return [.. reranked.Take(limit).Select(ranked =>
        {
            var original = candidates.Find(candidate => $"{Json.Str(candidate["type"])}:{Json.Str(candidate["id"])}" == ranked.Id);
            var result = original is null ? [] : Json.Clone(original);
            result["score"] = ranked.Score;
            result["retrievalMethod"] = "reranked";
            return result;
        })];
    }

    [GeneratedRegex("[^a-z0-9_-]+")]
    private static partial Regex NotTerm();

    /// Memories and claims connected to the entities a question names.
    private async Task<List<JsonObject>> GraphRecall(string query, MemoryScope? scope, int limit)
    {
        var terms = NotTerm().Split(query.ToLowerInvariant()).Where(term => term.Length >= 3).Take(8).ToList();
        if (terms.Count == 0) return [];
        var parameters = With(ScopeOf(scope), ("terms", terms), ("limit", (long)limit));
        await using var session = Session();
        var personalized = new List<JsonObject>();
        try
        {
            var graphName = Env.Get("NEO4J_GDS_GRAPH") ?? "personal-memory-context";
            var exists = await Rows(session, "CALL gds.graph.exists($graphName) YIELD exists RETURN exists", P(("graphName", graphName)));
            if (exists.Count > 0 && Json.Flag(exists[0]["exists"]))
            {
                var seeds = await Rows(session, """
                    UNWIND $terms AS term
                    MATCH (e:Entity)
                    WHERE (toLower(e.name) CONTAINS term OR any(alias IN coalesce(e.aliases, []) WHERE toLower(alias) CONTAINS term))
                      AND ($userId IS NULL OR e.userId IS NULL OR e.userId = $userId)
                      AND ($projectId IS NULL OR e.projectId IS NULL OR e.projectId = $projectId)
                    RETURN collect(DISTINCT id(e)) AS sourceNodes
                    """, parameters);
                if (seeds.FirstOrDefault()?["sourceNodes"] is JsonArray { Count: > 0 } sourceNodes)
                    personalized = await Rows(session, $"""
                        CALL gds.pageRank.stream($graphName, {"{sourceNodes: $sourceNodes, dampingFactor: 0.85, maxIterations: 20}"})
                        YIELD nodeId, score
                        WITH gds.util.asNode(nodeId) AS node, score
                        WHERE node:Memory
                          AND NOT node.status IN ['retracted', 'archived']
                          AND ($includeHistorical OR node.status = 'active')
                          AND ($includeHistorical OR (node.validFrom IS NULL OR datetime(node.validFrom) <= datetime()) AND (node.validUntil IS NULL OR datetime(node.validUntil) > datetime()))
                          AND ($userId IS NULL OR node.userId = $userId)
                          AND ($projectId IS NULL OR node.projectId = $projectId)
                          AND ($sessionId IS NULL OR node.sessionId = $sessionId)
                        RETURN 'memory' AS type, node.id AS id, node.content AS content, node.category AS kind,
                               node.validFrom AS occurredAt, 'ppr' AS source, node.projectId AS projectId,
                               node.status AS status, node.confidence AS confidence,
                               coalesce(node.reinforcementCount, 0) AS reinforcementCount,
                               toFloat(coalesce(node.utilitySuccesses, 0)) / CASE WHEN coalesce(node.utilityUses, 0) = 0 THEN 1 ELSE node.utilityUses END AS utilityScore,
                               [(node)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
                               [] AS sourceChunkIds,
                               [(node)-[:SUPPORTED_BY]->(source:Event) | {"{id: source.id, content: source.content}"}][0..10] AS evidence,
                               score
                        ORDER BY score DESC LIMIT $limit
                        """, With(new(parameters), ("graphName", graphName), ("sourceNodes", FromJson(sourceNodes))));
            }
        }
        catch (Neo4jException)
        {
            // GDS is optional. The weighted, bounded traversal below is the fallback.
        }
        var traversed = await Rows(session, """
            UNWIND $terms AS term
            MATCH (e:Entity)
            WHERE (toLower(e.name) CONTAINS term OR any(alias IN coalesce(e.aliases, []) WHERE toLower(alias) CONTAINS term))
              AND ($includeTestData OR coalesce(e.source, '') <> 'test')
              AND ($userId IS NULL OR e.userId IS NULL OR e.userId = $userId)
              AND ($projectId IS NULL OR e.projectId IS NULL OR e.projectId = $projectId)
              AND ($sessionId IS NULL OR e.sessionId IS NULL OR e.sessionId = $sessionId)
            MATCH path=(m:Memory)-[:ABOUT|SAME_AS|RELATED_TO*1..2]-(e)
            WHERE NOT m.status IN ['retracted', 'archived']
              AND ($includeTestData OR NOT EXISTS { MATCH (m)-[:SUPPORTED_BY]->(:Event {source: 'test'}) })
              AND ($includeHistorical OR m.status = 'active')
              AND ($includeHistorical OR (m.validFrom IS NULL OR datetime(m.validFrom) <= datetime()) AND (m.validUntil IS NULL OR datetime(m.validUntil) > datetime()))
              AND ($userId IS NULL OR m.userId = $userId)
              AND ($projectId IS NULL OR m.projectId = $projectId)
              AND ($sessionId IS NULL OR m.sessionId = $sessionId)
            WITH m, min(length(path)) AS distance,
                 max(reduce(product = 1.0, rel IN relationships(path) | product * coalesce(rel.weight, 1.0))) AS propagationWeight
            RETURN DISTINCT 'memory' AS type, m.id AS id, m.content AS content,
                   m.category AS kind, m.validFrom AS occurredAt, 'graph' AS source,
                   m.projectId AS projectId, m.status AS status, m.confidence AS confidence,
                   coalesce(m.reinforcementCount, 0) AS reinforcementCount,
                   toFloat(coalesce(m.utilitySuccesses, 0)) / CASE WHEN coalesce(m.utilityUses, 0) = 0 THEN 1 ELSE m.utilityUses END AS utilityScore,
                   [(m)-[:SUPPORTED_BY]->(source:Event) | source.id][0..10] AS sourceEventIds,
                   [] AS sourceChunkIds,
                   [(m)-[:SUPPORTED_BY]->(source:Event) | {id: source.id, content: source.content}][0..10] AS evidence,
                   (0.8 * propagationWeight) / distance AS score
            UNION ALL
            UNWIND $terms AS claimTerm
            MATCH (claimEntity:Entity)
            WHERE (toLower(claimEntity.name) CONTAINS claimTerm OR any(alias IN coalesce(claimEntity.aliases, []) WHERE toLower(alias) CONTAINS claimTerm))
              AND ($includeTestData OR coalesce(claimEntity.source, '') <> 'test')
              AND ($userId IS NULL OR claimEntity.userId IS NULL OR claimEntity.userId = $userId)
              AND ($projectId IS NULL OR claimEntity.projectId IS NULL OR claimEntity.projectId = $projectId)
              AND ($sessionId IS NULL OR claimEntity.sessionId IS NULL OR claimEntity.sessionId = $sessionId)
            MATCH (c:Claim)-[:ABOUT]->(claimEntity)
            WHERE ($userId IS NULL OR c.userId = $userId)
              AND ($projectId IS NULL OR c.projectId = $projectId)
              AND ($includeTestData OR (NOT EXISTS { MATCH (c)-[:SUPPORTED_BY]->(:Event {source: 'test'}) } AND NOT EXISTS { MATCH (c)-[:SUPPORTED_BY]->(:Chunk)<-[:HAS_CHUNK]-(:Document {source: 'test'}) }))
            RETURN DISTINCT 'claim' AS type, c.id AS id, c.statement AS content,
                   c.predicate AS kind, null AS occurredAt, 'graph' AS source,
                   c.projectId AS projectId, 'active' AS status, c.confidence AS confidence,
                   0 AS reinforcementCount,
                   0.0 AS utilityScore,
                   [] AS sourceEventIds,
                   [(c)-[:SUPPORTED_BY]->(sourceChunk:Chunk) | sourceChunk.id][0..10] AS sourceChunkIds,
                   [(c)-[:SUPPORTED_BY]->(sourceChunk:Chunk) | {id: sourceChunk.id, content: sourceChunk.content}][0..10] AS evidence,
                   0.6 AS score
            ORDER BY score DESC LIMIT $limit
            """, parameters);
        List<JsonObject> found = [.. personalized, .. traversed];
        foreach (var candidate in found) candidate["retrievalMethod"] = "graph";
        return found;
    }

    // ---- entities, documents, claims, procedures ----

    [GeneratedRegex("[^a-z0-9]+")]
    private static partial Regex NotIdentifier();

    /// An entity with no id gets one from its scope, type and name, so the
    /// same name in two projects is two entities. Entities that share an
    /// alias are linked as the same thing.
    public async Task<JsonObject> UpsertEntity(Entity input)
    {
        var entity = new Dictionary<string, object?>
        {
            ["id"] = input.Id ?? $"{input.UserId ?? "global"}:{input.ProjectId ?? "global"}:{input.Type}:{NotIdentifier().Replace(input.Name.ToLowerInvariant(), "-")}",
            ["name"] = input.Name,
            ["type"] = input.Type,
            ["aliases"] = (input.Aliases ?? []).ToList(),
            ["description"] = input.Description,
            ["userId"] = input.UserId,
            ["projectId"] = input.ProjectId,
            ["sessionId"] = input.SessionId,
        };
        await Rows("""
            MERGE (e:Entity {id: $entity.id})
            ON CREATE SET e += $entity
            ON MATCH SET e.name = $entity.name, e.type = $entity.type,
                         e.aliases = $entity.aliases,
                         e.description = coalesce($entity.description, e.description)
            WITH e
            UNWIND $entity.aliases AS alias
            MATCH (other:Entity)
            WHERE other.id <> e.id
              AND (toLower(other.name) = toLower(alias)
                OR any(existing IN coalesce(other.aliases, []) WHERE toLower(existing) = toLower(alias)))
            MERGE (e)-[r:SAME_AS]->(other)
            SET r.weight = 0.9
            RETURN e
            """, P(("entity", entity)));
        return new JsonObject(entity.Select(field => KeyValuePair.Create(field.Key, ToJson(field.Value))));
    }

    public async Task<JsonObject> UpsertDocument(DocumentInput input)
    {
        var document = new Dictionary<string, object?>
        {
            ["id"] = input.Id ?? NewId(),
            ["title"] = input.Title,
            ["source"] = input.Source,
            ["content"] = input.Content,
            ["metadata"] = Metadata(input.Metadata),
            ["userId"] = input.UserId,
            ["projectId"] = input.ProjectId,
            ["sessionId"] = input.SessionId,
        };
        await Rows("""
            MERGE (d:Document {id: $document.id})
            ON CREATE SET d += $document
            ON MATCH SET d.title = $document.title, d.source = $document.source,
                         d.content = coalesce($document.content, d.content), d.metadata = $document.metadata
            RETURN d
            """, P(("document", document)));
        return new JsonObject(document.Select(field => KeyValuePair.Create(field.Key, ToJson(field.Value))));
    }

    public async Task<JsonObject> UpsertChunk(ChunkInput input)
    {
        var embedding = input.Embedding?.ToList() ?? (embeddings.Enabled ? (await embeddings.EmbedDocuments([input.Content]))[0].ToList() : null);
        var chunk = new Dictionary<string, object?>
        {
            ["id"] = input.Id ?? NewId(),
            ["content"] = input.Content,
            ["ordinal"] = (long)input.Ordinal,
            ["embedding"] = embedding,
            ["embeddingModel"] = embedding is null ? null : embeddings.Model,
            ["userId"] = input.UserId,
            ["projectId"] = input.ProjectId,
            ["sessionId"] = input.SessionId,
        };
        await Write(async tx =>
        {
            await Rows(tx, """
                MERGE (c:Chunk {id: $chunk.id})
                ON CREATE SET c += $chunk
                ON MATCH SET c.content = $chunk.content, c.ordinal = $chunk.ordinal, c.embedding = $chunk.embedding, c.embeddingModel = $chunk.embeddingModel
                WITH c
                MATCH (d:Document {id: $documentId})
                MERGE (d)-[:HAS_CHUNK]->(c)
                """, P(("chunk", chunk), ("documentId", input.DocumentId)));
            await Rows(tx, """
                MATCH (c:Chunk {id: $chunkId})
                UNWIND $entityIds AS entityId
                MATCH (e:Entity {id: entityId})
                MERGE (c)-[r:MENTIONS]->(e)
                SET r.weight = 0.7
                """, P(("chunkId", chunk["id"]), ("entityIds", (input.EntityIds ?? []).ToList())));
            return true;
        });
        return new JsonObject(chunk.Select(field => KeyValuePair.Create(field.Key, ToJson(field.Value))));
    }

    public async Task<JsonObject> CreateClaim(ClaimInput input)
    {
        var claim = new Dictionary<string, object?>
        {
            ["id"] = input.Id ?? NewId(),
            ["statement"] = input.Statement,
            ["predicate"] = input.Predicate,
            ["confidence"] = input.Confidence ?? 1,
            ["userId"] = input.UserId,
            ["projectId"] = input.ProjectId,
            ["sessionId"] = input.SessionId,
        };
        await Write(async tx =>
        {
            await Rows(tx, """
                MERGE (c:Claim {id: $claim.id})
                ON CREATE SET c += $claim
                ON MATCH SET c += $claim
                WITH c
                MATCH (s:Entity {id: $subjectEntityId})
                MERGE (c)-[subjectRelation:ABOUT]->(s)
                SET subjectRelation.weight = 0.8
                WITH c
                OPTIONAL MATCH (o:Entity {id: $objectEntityId})
                FOREACH (_ IN CASE WHEN o IS NULL THEN [] ELSE [1] END |
                  MERGE (c)-[objectRelation:ABOUT]->(o)
                  SET objectRelation.weight = 0.8)
                """, P(("claim", claim), ("subjectEntityId", input.SubjectEntityId), ("objectEntityId", input.ObjectEntityId)));
            await Rows(tx, """
                MATCH (c:Claim {id: $claimId})
                UNWIND $sourceChunkIds AS chunkId
                MATCH (chunk:Chunk {id: chunkId})
                MERGE (c)-[:SUPPORTED_BY]->(chunk)
                """, P(("claimId", claim["id"]), ("sourceChunkIds", (input.SourceChunkIds ?? []).ToList())));
            await Rows(tx, """
                MATCH (c:Claim {id: $claimId})
                UNWIND $sourceEventIds AS eventId
                MATCH (event:Event {id: eventId})
                MERGE (c)-[:SUPPORTED_BY]->(event)
                """, P(("claimId", claim["id"]), ("sourceEventIds", (input.SourceEventIds ?? []).ToList())));
            return true;
        });
        return new JsonObject(claim.Select(field => KeyValuePair.Create(field.Key, ToJson(field.Value))));
    }

    public async Task<JsonObject> UpsertProcedure(ProcedureInput input)
    {
        static string List(IReadOnlyList<string>? items) => new JsonArray([.. (items ?? []).Select(item => (JsonNode)item)]).ToJsonString(Json.Options);
        var procedure = new Dictionary<string, object?>
        {
            ["id"] = input.Id ?? NewId(),
            ["title"] = input.Title,
            ["goal"] = input.Goal,
            ["steps"] = List(input.Steps),
            ["preconditions"] = List(input.Preconditions),
            ["knownFailures"] = List(input.KnownFailures),
            ["confidence"] = input.Confidence ?? 1,
            ["executable"] = input.Executable ?? false,
            ["command"] = input.Command,
            ["parameters"] = Metadata(input.Parameters),
            ["safety"] = input.Safety ?? "approval-required",
            ["userId"] = input.UserId,
            ["projectId"] = input.ProjectId,
            ["sessionId"] = input.SessionId,
        };
        await Write(async tx =>
        {
            await Rows(tx, """
                MERGE (p:Procedure {id: $procedure.id})
                ON CREATE SET p += $procedure
                ON MATCH SET p += $procedure
                """, P(("procedure", procedure)));
            await Rows(tx, """
                UNWIND $sourceEventIds AS eventId
                MATCH (p:Procedure {id: $procedureId}), (e:Event {id: eventId})
                MERGE (p)-[:SUPPORTED_BY]->(e)
                """, P(("procedureId", procedure["id"]), ("sourceEventIds", (input.SourceEventIds ?? []).ToList())));
            return true;
        });
        return new JsonObject(procedure.Select(field => KeyValuePair.Create(field.Key, ToJson(field.Value))));
    }
}
