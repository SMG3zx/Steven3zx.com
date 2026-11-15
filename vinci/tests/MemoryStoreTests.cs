using System.Text.Json.Nodes;
using Vinci.Memory;

namespace Vinci.Tests;

/// Runs only against a real database: set RUN_NEO4J_TESTS=1 with NEO4J_URI,
/// NEO4J_USERNAME and NEO4J_PASSWORD. Everything is written under a project
/// id made up for the run, so runs do not see each other's data.
public sealed class Neo4jFactAttribute : FactAttribute
{
    public Neo4jFactAttribute()
    {
        if (Environment.GetEnvironmentVariable("RUN_NEO4J_TESTS") != "1")
            Skip = "set RUN_NEO4J_TESTS=1 to run against Neo4j";
    }
}

/// Tests here change environment settings, so they never run beside each other.
[CollectionDefinition("neo4j", DisableParallelization = true)]
public sealed class Neo4jCollection;

[Collection("neo4j")]
public sealed class MemoryStoreTests : IAsyncLifetime
{
    private readonly string suffix = Guid.NewGuid().ToString();
    private Neo4jStore store = null!;

    private string EventId => $"integration-event-{suffix}";

    private MemoryScope Scoped(
        bool historical = false,
        string? session = null,
        IReadOnlyList<string>? methods = null
    ) =>
        new()
        {
            ProjectId = suffix,
            IncludeTestData = true,
            IncludeHistorical = historical ? true : null,
            SessionId = session,
            RetrievalMethods = methods,
        };

    public async Task InitializeAsync()
    {
        store = new Neo4jStore();
        await store.EnsureSchema();
        await store.AppendEvent(
            Event(EventId, "decision", $"Neo4j integration decision {suffix}", source: "test")
        );
    }

    public async Task DisposeAsync() => await store.DisposeAsync();

    private MemoryEvent Event(
        string? id,
        string kind,
        string content,
        string? source = null,
        string? session = null,
        string? project = null,
        string? key = null
    ) =>
        new()
        {
            Id = id,
            IdempotencyKey = key,
            Kind = kind,
            Content = content,
            Source = source,
            ProjectId = project ?? suffix,
            SessionId = session,
        };

    private static bool Has(IEnumerable<JsonObject> results, string id) =>
        results.Any(r => Json.Str(r["id"]) == id);

    /// Changes a setting for the length of a test and puts it back.
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

    private sealed class Scripted(
        Func<MemoryEvent, Extraction>? extract = null,
        SessionReflection? reflection = null
    ) : IConsolidator
    {
        public Task<Extraction> Extract(MemoryEvent e) =>
            Task.FromResult((extract ?? (_ => throw new InvalidOperationException("not used")))(e));

        public Task<SessionReflection> Reflect(IReadOnlyList<MemoryEvent> events) =>
            Task.FromResult(reflection ?? new SessionReflection("none", [], []));
    }

    [Neo4jFact]
    public async Task Persists_an_event_graph_and_supports_idempotent_replay()
    {
        var schema = await store.SchemaStatus();
        Assert.True(Json.Flag(schema["initialized"]));
        Assert.Equal(Json.Num(schema["currentVersion"]), Json.Num(schema["storedVersion"]));
        Assert.Contains("v2-content-index-safety", Json.Strings(schema["migrations"]));

        var replay = await store.AppendEvent(
            Event(EventId, "decision", $"Neo4j integration decision {suffix}", source: "test")
        );
        Assert.Equal(EventId, Json.Str(replay["id"]));
        Assert.True(Json.Flag(replay["idempotent"]));
        var keyed = await store.AppendEvent(
            Event(
                null,
                "decision",
                $"Keyed idempotent event {suffix}",
                key: $"keyed-event-{suffix}"
            )
        );
        var keyedReplay = await store.AppendEvent(
            Event(
                null,
                "decision",
                $"Keyed idempotent event {suffix}",
                key: $"keyed-event-{suffix}"
            )
        );
        Assert.True(Json.Flag(keyed["idempotent"]));
        Assert.Equal(Json.Str(keyed["id"]), Json.Str(keyedReplay["id"]));
        var conflict = await Assert.ThrowsAsync<InvalidOperationException>(() =>
            store.AppendEvent(Event(EventId, "error", $"Conflicting content {suffix}"))
        );
        Assert.Contains("idempotency conflict", conflict.Message);

        // A batch with one conflict writes nothing at all.
        var (atomicConflictId, atomicNewId) = ($"atomic-conflict-{suffix}", $"atomic-new-{suffix}");
        await store.AppendEvent(Event(atomicConflictId, "decision", $"Atomic original {suffix}"));
        SnapshotNode? Find(MemorySnapshot snapshot, string label, string id) =>
            snapshot.Nodes.FirstOrDefault(n =>
                n.Labels.Contains(label) && Json.Str(n.Properties["id"]) == id
            );
        var before = Find(await store.ExportSnapshot(), "Event", atomicConflictId);
        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            store.AppendEvents([
                Event(atomicConflictId, "error", $"Atomic conflicting update {suffix}"),
                Event(atomicNewId, "decision", $"Should not be written {suffix}"),
            ])
        );
        var after = await store.ExportSnapshot();
        Assert.Null(Find(after, "Event", atomicNewId));
        Assert.Equal(
            Json.Num(before!.Properties["ingestCount"]),
            Json.Num(Find(after, "Event", atomicConflictId)!.Properties["ingestCount"])
        );

        var job = await store.ClaimNextJob(EventId);
        Assert.Equal(EventId, job!.TargetId);
        Assert.Equal("completed", Json.Str((await store.CompleteJob(job.Id))["status"]));

        // A job whose lease ran out can be claimed again.
        var leaseEventId = $"lease-event-{suffix}";
        await store.AppendEvent(Event(leaseEventId, "tool_result", $"Lease recovery {suffix}"));
        using (new Setting("JOB_LEASE_SECONDS", "0"))
        {
            var claimed = await store.ClaimNextJob(leaseEventId);
            var reclaimed = await store.ClaimNextJob(leaseEventId);
            Assert.Equal(claimed!.Id, reclaimed!.Id);
            Assert.Equal(2, reclaimed.Attempts);
        }

        var sessionEventId = $"session-two-{suffix}";
        await store.AppendEvent(
            Event(
                sessionEventId,
                "user_message",
                $"Cross-session memory {suffix}",
                session: "session-two"
            )
        );
        var query = $"Cross-session memory {suffix}";
        Assert.True(Has(await store.Recall(query, Scoped(), 10), sessionEventId));
        Assert.True(
            Has(await store.Recall(query, Scoped(session: "session-two"), 10), sessionEventId)
        );
        Assert.False(
            Has(await store.Recall(query, Scoped(session: "session-one"), 10), sessionEventId)
        );
        var snapshot = await store.ExportSnapshot();
        Assert.NotNull(Find(snapshot, "Conversation", "session-two"));
        Assert.NotNull(Find(snapshot, "Message", sessionEventId));
        Assert.NotNull(Find(snapshot, "ReasoningTrace", EventId));
        Assert.Equal(EventId, (await store.GetEvent(EventId))!.Id);
    }

    [Neo4jFact]
    public async Task Links_entity_document_chunk_claim_and_memory_provenance()
    {
        var (entityId, documentId, chunkId) = (
            $"integration-entity-{suffix}",
            $"integration-document-{suffix}",
            $"integration-chunk-{suffix}"
        );
        await store.UpsertEntity(
            new Entity
            {
                Id = entityId,
                Name = $"Integration Project {suffix}",
                Type = "project",
                ProjectId = suffix,
            }
        );
        await store.UpsertDocument(
            new DocumentInput
            {
                Id = documentId,
                Title = "Integration source",
                Source = "test",
                Content = "Source document",
                ProjectId = suffix,
            }
        );
        await store.UpsertChunk(
            new ChunkInput
            {
                Id = chunkId,
                DocumentId = documentId,
                Content = $"Neo4j graph source {suffix}",
                Ordinal = 0,
                EntityIds = [entityId],
                ProjectId = suffix,
            }
        );
        var claim = await store.CreateClaim(
            new ClaimInput
            {
                Statement = $"The integration project uses Neo4j {suffix}",
                Predicate = "USES",
                SubjectEntityId = entityId,
                SourceChunkIds = [chunkId],
                SourceEventIds = [EventId],
                ProjectId = suffix,
            }
        );
        var memory = await store.CreateMemory(
            new CuratedMemory
            {
                Content = $"Remember the Neo4j decision {suffix}",
                Category = "decision",
                ProjectId = suffix,
                SourceEventIds = [EventId],
                EntityIds = [entityId],
            }
        );
        Assert.Equal("active", Json.Str(memory["status"]));
        Assert.Equal("USES", Json.Str(claim["predicate"]));

        var results = await store.Recall($"integration decision {suffix}", Scoped(), 10);
        Assert.True(Has(results, EventId));
        var recalled = results.First(r => Json.Str(r["id"]) == Json.Str(memory["id"]));
        Assert.Contains(EventId, Json.Strings(recalled["sourceEventIds"]));

        var graphResults = await store.Recall($"Integration Project {suffix}", Scoped(), 10);
        Assert.Contains(graphResults, r =>
            Json.Str(r["retrievalMethod"]) == "graph"
            || Json.Strings(r["retrievalMethods"]).Contains("graph")
        );

        var sourceResults = await store.Recall($"Neo4j graph source {suffix}", Scoped(), 10);
        Assert.Contains(sourceResults, r =>
            Json.Str(r["type"]) == "chunk" && Json.Str(r["id"]) == chunkId
        );
        Assert.Contains(sourceResults, r =>
            Json.Str(r["type"]) == "claim" && Json.Strings(r["sourceEventIds"]).Contains(EventId)
        );
    }

    [Neo4jFact]
    public async Task Retrieval_method_ablations_isolate_lexical_and_graph_candidates()
    {
        var id = $"ablation-event-{suffix}";
        await store.AppendEvent(Event(id, "decision", $"Ablation lexical evidence {suffix}"));
        var lexicalOnly = await store.Recall(
            $"Ablation lexical evidence {suffix}",
            Scoped(methods: ["lexical"]),
            10
        );
        var graphOnly = await store.Recall(
            $"Ablation lexical evidence {suffix}",
            Scoped(methods: ["graph"]),
            10
        );
        Assert.Contains(lexicalOnly, r =>
            Json.Str(r["id"]) == id && Json.Str(r["retrievalMethod"]) == "lexical"
        );
        Assert.False(Has(graphOnly, id));
    }

    [Neo4jFact]
    public async Task Benchmark_project_data_is_isolated_from_global_recall()
    {
        var (id, project) = (
            $"benchmark-isolation-{suffix}",
            $"benchmark-isolation-project-{suffix}"
        );
        using var setting = new Setting("BENCHMARK_PROJECT_ID", project);
        await store.AppendEvent(
            Event(
                id,
                "decision",
                $"Benchmark-only isolation marker {suffix}",
                source: "benchmark",
                project: project
            )
        );
        Assert.False(
            Has(
                await store.Recall(
                    $"Benchmark-only isolation marker {suffix}",
                    new MemoryScope(),
                    10
                ),
                id
            )
        );
        Assert.True(
            Has(
                await store.Recall(
                    $"Benchmark-only isolation marker {suffix}",
                    new MemoryScope { ProjectId = project },
                    10
                ),
                id
            )
        );
    }

    [Neo4jFact]
    public async Task The_worker_promotes_a_queued_event_into_durable_knowledge()
    {
        var id = $"worker-event-{suffix}";
        await store.AppendEvent(Event(id, "decision", $"Worker chose Qwen embeddings {suffix}"));
        var worker = new ConsolidationWorker(
            store,
            new Scripted(_ => new Extraction(
                [
                    new ExtractedMemory(
                        $"The worker chose Qwen embeddings {suffix}",
                        "decision",
                        0.95,
                        [$"Memory Project {suffix}"],
                        null,
                        null
                    ),
                ],
                [new ExtractedEntity($"Memory Project {suffix}", "project", [], null)],
                [],
                [
                    new ExtractedProcedure(
                        "Verify memory worker",
                        "Confirm consolidation works",
                        ["Append event", "Run worker"],
                        ["Neo4j is reachable"],
                        ["Model endpoint unavailable"],
                        0.8
                    ),
                ]
            ))
        );
        var result = await worker.ProcessOnce(id);
        Assert.Null(result!["error"]);
        Assert.Contains(
            await store.Recall($"worker chose Qwen embeddings {suffix}", Scoped(), 10),
            r => Json.Str(r["kind"]) == "decision"
        );
        Assert.Contains(await store.Recall("model endpoint unavailable", Scoped(), 10), r =>
            Json.Str(r["type"]) == "procedure"
        );
    }

    [Neo4jFact]
    public async Task Retries_transient_failures_and_then_fails_bounded_jobs()
    {
        var id = $"retry-event-{suffix}";
        await store.AppendEvent(Event(id, "error", $"Retry provider timeout {suffix}"));
        using var attempts = new Setting("JOB_MAX_ATTEMPTS", "2");
        using var backoff = new Setting("JOB_RETRY_BACKOFF_SECONDS", "0");
        var worker = new ConsolidationWorker(
            store,
            new Scripted(_ => throw new InvalidOperationException("provider timeout"))
        );
        Assert.Equal("queued", Json.Str((await worker.ProcessOnce(id))!["retry"]!["status"]));
        Assert.Equal("failed", Json.Str((await worker.ProcessOnce(id))!["retry"]!["status"]));
        Assert.Equal(
            "queued",
            Json.Str((await store.RequeueFailedJob($"consolidate-event:{id}"))!["status"])
        );
    }

    [Neo4jFact]
    public async Task Reconsolidation_links_conflicting_memories_without_deleting_evidence()
    {
        CuratedMemory Fact(
            string id,
            string content,
            string subject,
            string value,
            string? project = null,
            string? from = null,
            string? supersedes = null
        ) =>
            new()
            {
                Id = $"{id}-{suffix}",
                Content = $"{content} {suffix}",
                Category = "fact",
                Subject = $"{subject}-{suffix}",
                Predicate = "VALUE",
                Object = value,
                ProjectId = project ?? suffix,
                ValidFrom = from,
                SupersedesMemoryId = supersedes,
            };
        static bool Linked(JsonNode? links, JsonObject to, string? relation = null) =>
            (links as JsonArray ?? []).Any(l =>
                Json.Str(l!["id"]) == Json.Str(to["id"])
                && (relation is null || Json.Str(l["relation"]) == relation)
            );

        var first = await store.CreateMemory(
            Fact("memory-old", "The integration mode is batch", "integration-mode", "batch")
        );
        var second = await store.CreateMemory(
            Fact("memory-new", "The integration mode is streaming", "integration-mode", "streaming")
        );
        Assert.True(Linked(second["conflicts"], first, "contradicts"));
        var worker = new ConsolidationWorker(store, new Scripted());
        var result = await worker.ProcessOnce(Json.Str(second["id"]));
        Assert.Null(result!["error"]);
        Assert.True(Linked(result["links"], first, "contradicts"));

        // The same subject in another project is a different fact.
        var other = await store.CreateMemory(
            Fact(
                "memory-other-project",
                "Other project value",
                "integration-mode",
                "other",
                project: $"other-{suffix}"
            )
        );
        Assert.False(Linked((await worker.ProcessOnce(Json.Str(other["id"])))!["links"], first));

        var historical = await store.CreateMemory(
            Fact(
                "memory-historical",
                "The historical integration mode was batch",
                "temporal-mode",
                "batch",
                from: "2020-01-01T00:00:00.000Z"
            )
        );
        var current = await store.CreateMemory(
            Fact(
                "memory-current",
                "The current integration mode is streaming",
                "temporal-mode",
                "streaming",
                from: "2021-01-01T00:00:00.000Z",
                supersedes: Json.Str(historical["id"])
            )
        );
        Assert.Equal("active", Json.Str(current["status"]));
        Assert.True(
            Has(
                await store.Recall(
                    $"historical integration mode {suffix}",
                    Scoped(historical: true),
                    20
                ),
                Json.Str(historical["id"])
            )
        );
        var superseded = (
            await store.ListMemories(new MemoryScope { ProjectId = suffix }, 50, "superseded")
        ).First(m => Json.Str(m["id"]) == Json.Str(historical["id"]));
        Assert.Contains(Json.Str(current["id"]), Json.Strings(superseded["supersededByIds"]));
    }

    [Neo4jFact]
    public async Task Retraction_hides_a_memory_while_preserving_its_node()
    {
        var id = $"memory-retract-{suffix}";
        await store.CreateMemory(
            new CuratedMemory
            {
                Id = id,
                Content = $"Sensitive memory to retract {suffix}",
                Category = "working",
                ProjectId = suffix,
            }
        );
        Assert.Equal(
            "retracted",
            Json.Str((await store.RetractMemory(id, "test cleanup"))!["status"])
        );
        Assert.False(
            Has(await store.Recall($"Sensitive memory to retract {suffix}", Scoped(), 10), id)
        );
        Assert.Equal("retracted", Json.Str((await store.MemoryImpact(id))!["status"]));
    }

    [Neo4jFact]
    public async Task Tracks_scope_alias_identity_memory_utility_and_context_hubs()
    {
        var first = await store.UpsertEntity(
            new Entity
            {
                Name = $"Shared Entity {suffix}",
                Type = "project",
                ProjectId = suffix,
            }
        );
        var second = await store.UpsertEntity(
            new Entity
            {
                Name = $"Shared Entity {suffix}",
                Type = "project",
                ProjectId = $"other-{suffix}",
            }
        );
        Assert.NotEqual(Json.Str(first["id"]), Json.Str(second["id"]));

        var canonical = await store.UpsertEntity(
            new Entity
            {
                Id = $"canonical-{suffix}",
                Name = $"Qwen Embedding {suffix}",
                Type = "technology",
                Aliases = [$"Qwen3 {suffix}"],
                ProjectId = suffix,
            }
        );
        await store.UpsertEntity(
            new Entity
            {
                Id = $"alias-{suffix}",
                Name = $"Qwen3 {suffix}",
                Type = "technology",
                ProjectId = suffix,
            }
        );
        var memoryId = $"utility-memory-{suffix}";
        await store.CreateMemory(
            new CuratedMemory
            {
                Id = memoryId,
                Content = $"Useful memory {suffix}",
                Category = "fact",
                ProjectId = suffix,
                EntityIds = [Json.Str(canonical["id"])],
            }
        );
        Assert.Equal(
            1,
            Json.Num(
                (await store.RecordMemoryUtility(memoryId, true, "used in answer"))!["utilityScore"]
            )
        );
        var hubs = await store.ContextHubs(10, Scoped());
        Assert.Contains(hubs["hubs"]!.AsArray(), hub =>
            Json.Str(hub!["id"]) == Json.Str(canonical["id"]) || Json.Str(hub["id"]) == memoryId
        );

        Assert.Equal(
            "archived",
            Json.Str((await store.ArchiveMemory(memoryId, "no longer current"))!["status"])
        );
        Assert.False(
            Has(
                await store.Recall($"Useful memory {suffix}", Scoped(historical: true), 10),
                memoryId
            )
        );
        Assert.False(Has(await store.Recall($"Useful memory {suffix}", Scoped(), 10), memoryId));
    }

    [Neo4jFact]
    public async Task Schedules_and_persists_a_session_reflection()
    {
        using var delay = new Setting("REFLECTION_DELAY_SECONDS", "0");
        var session = $"reflection-session-{suffix}";
        await store.AppendEvents([
            Event(
                $"reflection-user-{suffix}",
                "user_message",
                $"The user asked about reflection {suffix}",
                session: session
            ),
            Event(
                $"reflection-error-{suffix}",
                "error",
                $"The first attempt failed {suffix}",
                session: session
            ),
        ]);
        var worker = new ConsolidationWorker(
            store,
            new Scripted(
                _ => Extraction.Empty,
                new SessionReflection(
                    $"Reflection for {suffix}",
                    [$"Lesson from {suffix}"],
                    [$"Failure lesson {suffix}"]
                )
            )
        );
        Assert.Null((await worker.ProcessOnce(session))!["error"]);
        Assert.Contains(
            await store.Recall($"Reflection for {suffix}", Scoped(session: session), 10),
            r => Json.Str(r["kind"]) == "reflection"
        );
    }

    [Neo4jFact]
    public async Task A_snapshot_can_be_restored_over_the_data_it_came_from()
    {
        var snapshot = await store.ExportSnapshot();
        var mine = snapshot
            .Nodes.Where(n => Json.Str(n.Properties["projectId"]) == suffix)
            .ToList();
        Assert.NotEmpty(mine);
        var keys = mine.Select(n => n.Key).ToHashSet();
        await store.Restore(
            snapshot with
            {
                Nodes = mine,
                Relationships =
                [
                    .. snapshot.Relationships.Where(r =>
                        keys.Contains(r.SourceKey) && keys.Contains(r.TargetKey)
                    ),
                ],
            }
        );
        // Restoring matches nodes by id, so nothing is doubled.
        var after = await store.ExportSnapshot();
        Assert.Equal(
            mine.Count,
            after.Nodes.Count(n => Json.Str(n.Properties["projectId"]) == suffix)
        );
    }
}
