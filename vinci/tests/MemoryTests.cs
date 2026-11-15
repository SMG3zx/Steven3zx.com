using System.Net;
using System.Text;
using System.Text.Json.Nodes;
using Vinci.Memory;

namespace Vinci.Tests;

/// The memory logic that needs no database: how a question is routed, how
/// evidence is ranked, packed and cited, and how a model's answer is checked.
public sealed class MemoryTests
{
    private static JsonObject Item(string json) => (JsonObject)JsonNode.Parse(json)!;

    private static List<JsonObject> Items(params string[] json) => [.. json.Select(Item)];

    /// Stands in for a model service: answers every request with one reply and remembers what was asked.
    private sealed class Service(Func<HttpRequestMessage, Task<HttpResponseMessage>> answer)
        : HttpMessageHandler
    {
        public string? LastBody { get; private set; }

        public static Service Replying(string json, HttpStatusCode status = HttpStatusCode.OK) =>
            new(_ =>
                Task.FromResult(
                    new HttpResponseMessage(status)
                    {
                        Content = new StringContent(json, Encoding.UTF8, "application/json"),
                    }
                )
            );

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken stop
        )
        {
            if (request.Content is not null)
                LastBody = await request.Content.ReadAsStringAsync(stop);
            return await answer(request).WaitAsync(stop);
        }
    }

    // ---- rank fusion ----

    private static readonly RankFusion.Weights Weights = new(0.4, 0.4, 0.2);

    private static Dictionary<string, IReadOnlyList<JsonObject>> Sources(
        List<JsonObject> lexical,
        List<JsonObject> vector,
        List<JsonObject> graph
    ) =>
        new()
        {
            ["lexical"] = lexical,
            ["vector"] = vector,
            ["graph"] = graph,
        };

    [Fact]
    public void Rank_fusion_keeps_incompatible_raw_index_scores_separate()
    {
        var results = RankFusion.Fuse(
            Sources(
                Items("""{"type":"memory","id":"lexical","content":"lexical","score":100}"""),
                Items("""{"type":"memory","id":"vector","content":"vector","score":0.99}"""),
                []
            ),
            Weights
        );
        Assert.Equal("lexical", Json.Str(results[0]["id"]));
        Assert.Equal(0.4 / 61, Json.Num(results[0]["score"]), 10);
    }

    [Fact]
    public void Rank_fusion_rewards_agreement_across_retrieval_methods()
    {
        var results = RankFusion.Fuse(
            Sources(
                Items(
                    """{"type":"memory","id":"shared","content":"shared","score":5}""",
                    """{"type":"memory","id":"lexical-only","content":"lexical-only","score":4}"""
                ),
                Items("""{"type":"memory","id":"shared","content":"shared","score":0.8}"""),
                Items("""{"type":"memory","id":"shared","content":"shared","score":0.5}""")
            ),
            Weights
        );
        Assert.Equal("shared", Json.Str(results[0]["id"]));
        Assert.Equal("hybrid", Json.Str(results[0]["retrievalMethod"]));
        Assert.Equal(["lexical", "vector", "graph"], Json.Strings(results[0]["retrievalMethods"]));
    }

    // ---- retrieval ----

    [Fact]
    public void Routes_temporal_procedural_graph_and_semantic_queries()
    {
        Assert.Equal("temporal", Retrieval.RouteQuery("What did we use before Neo4j?"));
        Assert.Equal("procedural", Retrieval.RouteQuery("How do I recover the deployment?"));
        Assert.Equal("graph", Retrieval.RouteQuery("Which projects depend on Ollama?"));
        Assert.Equal("semantic", Retrieval.RouteQuery("What did we decide about embeddings?"));
    }

    [Fact]
    public void Abstains_when_evidence_is_absent_or_weak()
    {
        Assert.True(Retrieval.ShouldAbstain([]).Abstain);
        Assert.True(Retrieval.ShouldAbstain(Items("""{"score":0.1}""")).Abstain);
        Assert.False(Retrieval.ShouldAbstain(Items("""{"score":0.8}""")).Abstain);
        Assert.True(
            Retrieval
                .ShouldAbstain(
                    Items(
                        """{"score":1.2,"content":"unrelated evidence","retrievalMethod":"lexical"}"""
                    ),
                    "Zyx-Not-Real-991"
                )
                .Abstain
        );
        Assert.True(
            Retrieval
                .ShouldAbstain(
                    Items(
                        """{"score":1.2,"content":"The benchmark provider is Qwen3","retrievalMethod":"lexical"}"""
                    ),
                    "Archived token alpha-991"
                )
                .Abstain
        );
    }

    [Fact]
    public void Builds_a_corrective_query_and_assesses_retrieval_evidence()
    {
        Assert.Equal(
            "decide qwen embeddings",
            Retrieval.CorrectiveQuery("What did we decide about Qwen embeddings?")
        );
        Assert.Null(Retrieval.CorrectiveQuery("qwen embeddings"));
        Assert.True(Retrieval.AssessEvidence(Items("""{"score":0.1}""")).NeedsCorrection);
        Assert.False(Retrieval.AssessEvidence(Items("""{"score":0.8}""")).NeedsCorrection);
    }

    [Fact]
    public void Retention_decays_by_category_and_reinforcement_slows_forgetting()
    {
        var now = DateTimeOffset.Parse("2026-01-31T00:00:00.000Z");
        double Score(string at, string category, double confidence, int reinforced = 0) =>
            Retrieval.RetentionScore(at, confidence, category, reinforced, now);
        var recent = Score("2026-01-30T00:00:00.000Z", "fact", 1);
        var oldEpisode = Score("2025-01-01T00:00:00.000Z", "episode", 1);
        Assert.True(recent > oldEpisode);
        Assert.True(Score("2025-01-01T00:00:00.000Z", "episode", 1, reinforced: 5) > oldEpisode);
        Assert.True(Score("2026-01-30T00:00:00.000Z", "fact", 0.4) < recent);
    }

    [Fact]
    public void Retrieval_budgets_scale_with_query_complexity()
    {
        Assert.Equal("simple", Retrieval.ClassifyQueryComplexity("What is the deployment status?"));
        Assert.Equal(
            "multi-hop",
            Retrieval.ClassifyQueryComplexity(
                "Which projects depend on Ollama and affect embeddings?"
            )
        );
        Assert.True(
            Retrieval
                .Plan("Which projects depend on Ollama and affect embeddings?")
                .CandidateMultiplier
                > Retrieval.Plan("What is the deployment status?").CandidateMultiplier
        );
    }

    [Fact]
    public void A_route_boosts_the_kind_of_evidence_it_wants()
    {
        var procedure = Item("""{"type":"procedure","id":"p","score":0.01}""");
        var memory = Item("""{"type":"memory","id":"m","score":0.01}""");
        Assert.True(
            Retrieval.RouteScore(procedure, "procedural")
                > Retrieval.RouteScore(memory, "procedural")
        );
        Assert.True(
            Retrieval.RouteScore(memory, "semantic") > Retrieval.RouteScore(procedure, "semantic")
        );
    }

    // ---- context and citations ----

    [Fact]
    public void Context_packs_preserve_provenance_and_enforce_a_character_budget()
    {
        var pack = ContextBuilder.Build(
            Items(
                """{"id":"m1","type":"memory","content":"first","sourceEventIds":["e1"]}""",
                """{"id":"m2","type":"memory","content":"second","sourceChunkIds":["c2"]}"""
            ),
            50
        );
        Assert.Contains("memory:m1", pack.Context);
        Assert.Contains("sources: e1", pack.Context);
        Assert.True(pack.Truncated);
        Assert.Equal(1, pack.OmittedResults);
        Assert.True(pack.CharacterCount <= 50);
    }

    [Fact]
    public void Context_ordering_keeps_top_evidence_at_the_edges_and_removes_duplicates()
    {
        var ordered = ContextBuilder.Order(
            Items(
                """{"type":"memory","id":"one","content":"one"}""",
                """{"type":"memory","id":"two","content":"two"}""",
                """{"type":"memory","id":"three","content":"three"}""",
                """{"type":"memory","id":"two","content":"duplicate"}"""
            )
        );
        Assert.Equal(["one", "three", "two"], ordered.Select(item => Json.Str(item["id"])));
    }

    [Fact]
    public void Citation_locked_answers_require_citations_from_retrieved_evidence()
    {
        var results = Items("""{"type":"memory","id":"m1","content":"Neo4j is used."}""");
        Assert.True(Citations.Validate("Neo4j is used. [memory:m1]", results).Valid);
        Assert.False(Citations.Validate("Neo4j is used.", results).Valid);
        Assert.False(Citations.Validate("Neo4j is used. [memory:unknown]", results).Valid);
    }

    [Fact]
    public void Citation_locking_rejects_an_answer_with_an_unsupported_second_claim()
    {
        var results = Items("""{"type":"memory","id":"m1","content":"Neo4j is used."}""");
        var validation = Citations.Validate(
            "Neo4j is used. [memory:m1] The system is production-ready.",
            results
        );
        Assert.False(validation.Valid);
        Assert.Single(validation.UncitedSegments!);
    }

    [Fact]
    public void Content_limits_use_utf8_bytes_and_reject_oversized_values()
    {
        Assert.Equal(4, ContentLimits.Validate("🙂"));
        Assert.Throws<ContentTooLargeException>(() =>
            ContentLimits.Validate(new string('x', ContentLimits.DefaultMaxBytes + 1))
        );
    }

    // ---- model services ----

    [Fact]
    public void Lexical_faithfulness_requires_evidence_support_and_a_non_empty_answer()
    {
        Assert.True(FaithfulnessJudge.Lexical("answer", ["Qwen3 is used"], ["qwen3"]));
        Assert.False(FaithfulnessJudge.Lexical("answer", ["unrelated"], ["qwen3"]));
        Assert.False(FaithfulnessJudge.Lexical("", ["qwen3"], ["qwen3"]));
    }

    [Fact]
    public async Task Faithfulness_judge_accepts_supported_verdict_responses()
    {
        var judge = new FaithfulnessJudge(
            "http://judge.test",
            Service.Replying("""{"verdict":"supported","score":0.91}""")
        );
        var verdict = await judge.Judge("q", "a", ["e"]);
        Assert.True(verdict!.Faithful);
        Assert.Equal("external", verdict.JudgedBy);
        Assert.Equal(0.91, verdict.Score);
        Assert.Null(await new FaithfulnessJudge("").Judge("q", "a", ["e"]));
    }

    [Fact]
    public async Task Reranker_reorders_candidates_using_provider_scores()
    {
        var service = Service.Replying("""[{"index":1,"score":0.9},{"index":0,"score":0.2}]""");
        var results = await new RerankerClient("http://reranker.test", service).Rerank(
            "why",
            [new("a", "first"), new("b", "second")]
        );
        Assert.Equal("""{"query":"why","texts":["first","second"],"top_n":2}""", service.LastBody);
        Assert.Equal(["b", "a"], results.Select(r => r.Id));
    }

    [Fact]
    public async Task Embedding_client_uses_the_openai_shape_with_an_instruction_only_on_queries()
    {
        var service = Service.Replying("""{"data":[{"index":0,"embedding":[0.1,0.2,0.3]}]}""");
        var client = new EmbeddingClient("http://embedding.test", service);
        Assert.True(client.Enabled);
        Assert.Equal("qwen3-embedding:4b", client.Model);
        var vector = await client.EmbedQuery("what did we decide?");
        Assert.Equal([0.1, 0.2, 0.3], vector);
        Assert.Equal(
            """{"model":"qwen3-embedding:4b","input":["Instruct: Retrieve memories and source events relevant to the user's query\nQuery: what did we decide?"]}""",
            service.LastBody
        );

        // Documents go in without an instruction, and come back in input order even if the server reorders them.
        var documents = Service.Replying(
            """{"data":[{"index":1,"embedding":[3,4]},{"index":0,"embedding":[1,2]}]}"""
        );
        var vectors = await new EmbeddingClient("http://embedding.test", documents).EmbedDocuments([
            "first",
            "second",
        ]);
        Assert.Equal(
            [
                [1, 2],
                [3, 4],
            ],
            vectors
        );
        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            new EmbeddingClient(
                "http://embedding.test",
                Service.Replying("""{"data":[]}""")
            ).EmbedDocuments(["hello"])
        );
        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            new EmbeddingClient(
                "http://embedding.test",
                Service.Replying("""{"data":[{"index":0,"embedding":[1]}]}""")
            ).EmbedDocuments(["a", "b"])
        );
    }

    [Fact]
    public void The_extraction_schema_has_no_null_entries_and_offers_every_vocabulary_value()
    {
        static bool HasNull(JsonNode? node) =>
            node switch
            {
                null => true,
                JsonObject o => o.Any(p => HasNull(p.Value)),
                JsonArray a => a.Any(HasNull),
                _ => false,
            };
        var schema = Consolidation.ExtractionSchema();
        Assert.False(HasNull(schema));
        var category = schema["properties"]!["memories"]!["items"]!["properties"]!["category"]![
            "enum"
        ]!
            .AsArray()
            .Select(n => n!.GetValue<string>());
        Assert.Equal(Vocabulary.MemoryCategories, category);
    }

    [Fact]
    public void The_same_memory_text_in_the_same_scope_gets_the_same_id()
    {
        var scope = new MemoryScope { ProjectId = "p" };
        var a = Neo4jStore.ContentMemoryId(scope, "The  worker   restarts\nafter a failure.");
        Assert.Equal(a, Neo4jStore.ContentMemoryId(scope, "the worker restarts after a failure."));
        Assert.NotEqual(
            a,
            Neo4jStore.ContentMemoryId(
                new MemoryScope { ProjectId = "other" },
                "the worker restarts after a failure."
            )
        );
        Assert.NotEqual(a, Neo4jStore.ContentMemoryId(scope, "a different fact"));
    }

    [Fact]
    public void Keyword_search_looks_for_content_words_not_the_function_words_of_a_question()
    {
        Assert.Equal(
            "bread culture refresh weekend",
            Retrieval.LexicalQuery("What bread culture did we refresh over the weekend?")
        );
        Assert.Equal("the do", Retrieval.LexicalQuery("the do"));
    }

    [Fact]
    public async Task Provider_status_tells_disabled_ready_and_unreachable_apart()
    {
        Assert.Equal(new ProviderStatus(false, "disabled"), await ProviderStatus.Probe(null));
        Assert.Equal(
            new ProviderStatus(true, "ready", 200),
            await ProviderStatus.Probe("http://provider.test", handler: Service.Replying("{}"))
        );
        Assert.Equal(
            new ProviderStatus(true, "ready", 200),
            await ProviderStatus.Probe(
                "http://provider.test",
                handler: Service.Replying("{}"),
                path: "/v1/models"
            )
        );
        Assert.Equal(
            new ProviderStatus(true, "reachable", 503),
            await ProviderStatus.Probe(
                "http://provider.test",
                handler: Service.Replying("{}", HttpStatusCode.ServiceUnavailable)
            )
        );
        var never = new Service(_ => new TaskCompletionSource<HttpResponseMessage>().Task);
        Assert.Equal(
            "unreachable",
            (await ProviderStatus.Probe("http://provider.test", timeoutMs: 1, handler: never)).State
        );
    }

    [Fact]
    public void Metrics_retain_bounded_retrieval_telemetry_without_content()
    {
        var metrics = new MetricsRegistry();
        metrics.RecordRetrieval(
            "semantic",
            ["lexical", "graph"],
            12,
            ok: true,
            estimatedCost: 0.0025
        );
        metrics.RecordRetrieval("temporal", ["lexical"], 30, ok: false);
        metrics.RecordAnswerValidation(true);
        metrics.RecordAnswerValidation(false);
        var snapshot = metrics.Snapshot();
        var retrievals = snapshot["retrievals"]!;
        Assert.Equal(2, Json.Num(retrievals["count"]));
        Assert.Equal(1, Json.Num(retrievals["errors"]));
        Assert.Equal(1, Json.Num(retrievals["routes"]!["temporal"]));
        Assert.Equal(1, Json.Num(retrievals["methods"]!["graph"]));
        Assert.Equal(30, Json.Num(retrievals["p95LatencyMs"]));
        Assert.Equal(0.0025, Json.Num(retrievals["estimatedCostUsd"]));
        Assert.Equal(1, Json.Num(snapshot["answerValidation"]!["rejected"]));
        Assert.Single(snapshot["trend"]!.AsArray());
        Assert.DoesNotContain("Neo4j", snapshot.ToJsonString());
    }

    // ---- consolidation ----

    [Fact]
    public void A_models_extraction_is_checked_and_given_defaults()
    {
        var extraction = Consolidation.ParseExtraction(
            Consolidation.ParseReply(
                """
                ```json
                {
                  "memories": [{ "content": "Neo4j stores the graph" }],
                  "entities": [{ "name": "Neo4j", "type": "technology", "aliases": ["neo"] }],
                  "procedures": [{ "title": "Restore", "goal": "Bring the graph back", "steps": ["stop", "load"] }]
                }
                ```
                """
            )
        );
        var memory = Assert.Single(extraction.Memories);
        Assert.Equal(("fact", 0.7), (memory.Category, memory.Confidence));
        Assert.Equal("technology", extraction.Entities[0].Type);
        Assert.Empty(extraction.Claims);
        Assert.Equal(["stop", "load"], extraction.Procedures[0].Steps);

        Assert.Throws<FormatException>(() =>
            Consolidation.ParseExtraction(JsonNode.Parse("""{"memories":[{"content":""}]}"""))
        );
        Assert.Throws<FormatException>(() =>
            Consolidation.ParseExtraction(
                JsonNode.Parse("""{"memories":[{"content":"x","category":"rumour"}]}""")
            )
        );
        Assert.Throws<FormatException>(() =>
            Consolidation.ParseExtraction(
                JsonNode.Parse("""{"memories":[{"content":"x","confidence":2}]}""")
            )
        );
        Assert.Throws<FormatException>(() =>
            Consolidation.ParseExtraction(
                JsonNode.Parse("""{"procedures":[{"title":"t","goal":"g","steps":[]}]}""")
            )
        );
        Assert.Throws<FormatException>(() =>
            Consolidation.ParseReflection(JsonNode.Parse("""{"lessons":[]}"""))
        );
    }

    [Fact]
    public async Task The_heuristic_consolidator_remembers_only_what_was_marked()
    {
        var consolidator = new HeuristicConsolidator();
        var plain = new MemoryEvent { Kind = "user_message", Content = "just chatting" };
        var marked = new MemoryEvent
        {
            Kind = "user_message",
            Content = "The deploy key lives in the vault",
            OccurredAt = "2026-01-01T00:00:00Z",
            Metadata = Item("""{"remember":true}"""),
        };
        Assert.Empty((await consolidator.Extract(plain)).Memories);
        var memory = Assert.Single((await consolidator.Extract(marked)).Memories);
        Assert.Equal(
            ("The deploy key lives in the vault", "fact", 0.5, "2026-01-01T00:00:00Z"),
            (memory.Content, memory.Category, memory.Confidence, memory.ValidFrom)
        );
        Assert.Equal(
            "Explicitly remembered 1 event(s).",
            (await consolidator.Reflect([plain, marked])).Reflection
        );
    }

    [Fact]
    public void Failure_events_become_episode_memories_that_point_at_their_event()
    {
        var memory = ConsolidationWorker.FailureMemoryFromEvent(
            new MemoryEvent
            {
                Id = "event-1",
                Kind = "tool_result",
                Content = "Deployment timed out after 30 seconds",
                ProjectId = "project-1",
                SessionId = "session-1",
                Metadata = Item("""{"command":"deploy","status":"failed"}"""),
            }
        );
        Assert.Equal("episode", memory!.Category);
        Assert.Equal(["event-1"], memory.SourceEventIds);
        Assert.Equal("reflexion_failure", Json.Str(memory.Metadata!["memoryKind"]));
        Assert.Equal("deploy", Json.Str(memory.Metadata["originalMetadata"]!["command"]));

        Assert.Null(
            ConsolidationWorker.FailureMemoryFromEvent(
                new MemoryEvent
                {
                    Kind = "tool_result",
                    Content = "Deployment completed",
                    Metadata = Item("""{"status":"success"}"""),
                }
            )
        );
    }

    // ---- snapshots ----

    private static SnapshotNode Node(string key) =>
        new(key, ["Event"], Item($$"""{"id":"{{key}}"}"""));

    [Fact]
    public void Snapshot_validation_accepts_linked_nodes_and_rejects_dangling_relationships()
    {
        var linked = new MemorySnapshot(
            MemorySnapshot.FormatName,
            1,
            "2026-01-01T00:00:00Z",
            [Node("a"), Node("b")],
            [new("a", "RELATED_TO", "b", [])]
        );
        Assert.Equal((2, 1), linked.Validate());

        var dangling = linked with
        {
            Nodes = [Node("a")],
            Relationships = [new("a", "RELATED_TO", "missing", [])],
        };
        Assert.Contains(
            "missing node",
            Assert.Throws<FormatException>(() => dangling.Validate()).Message
        );
        Assert.Throws<FormatException>(() => (linked with { Version = 2 }).Validate());
        Assert.Throws<FormatException>(() =>
            (linked with { Nodes = [Node("a"), Node("a")] }).Validate()
        );
        Assert.Throws<FormatException>(() =>
            (linked with { Relationships = [new("a", "BAD TYPE", "b", [])] }).Validate()
        );
    }
}
