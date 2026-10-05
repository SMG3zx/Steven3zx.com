# Personal Memory Architecture

This service is intentionally standalone from MeinFactory. It provides a model/provider-agnostic memory substrate that Codex, MCP clients, and other agents can use through REST or MCP.

## Design principles

1. Events are the source of truth. User messages, agent messages, tool calls/results, decisions, artifacts, and errors are appended idempotently before any model-based extraction occurs.
2. Memory is layered. Working context is short-lived operational state; fact, episode, belief, reflection, failure, summary, and semantic memories have distinct purposes; procedures describe repeatable actions and may carry executable, safety-gated metadata. Consolidation creates views over events and never rewrites the original event.
3. Evidence is first-class. Documents, chunks, entities, claims, memories, and procedures retain links to source events or source chunks. Retrieval returns provenance identifiers.
4. Retrieval is hybrid and inspectable. Full-text, weighted graph propagation, vector similarity, and optional reranking produce candidates with retrieval-method metadata. Query complexity selects an adaptive budget and strong simple lexical evidence can early-stop optional vector work. Weak results trigger a corrective query; unsupported results abstain.
5. Contradictions are preserved. Reconsolidation links competing memories with `CONTRADICTS` and matching memories with `REINFORCES`; explicit supersession changes validity without deleting evidence.

## Complete research-to-code traceability

The following is the complete list of papers pulled from or discussed during this upgrade. Each row records the idea taken from the paper, its current status, and the source-code file/line anchors. `Partial` means the relevant substrate exists but the paper's full method is not implemented; `Research only` means it is documented as a candidate direction without a current implementation.

| Paper | What we took from it | Status and source-code anchors |
| --- | --- | --- |
| [MemGPT](https://arxiv.org/abs/2310.08560) | Separate working context from durable archival memory | Implemented: `src/types.ts:1-5`, `src/neo4j-store.ts:158-218`, `src/worker.ts:29-88` |
| [Mem0](https://arxiv.org/abs/2504.19413) | Extract, update, and retrieve structured memories | Implemented: `src/consolidation.ts:4-57`, `src/worker.ts:55-88`, `src/neo4j-store.ts:323-377` |
| [A-MEM](https://arxiv.org/abs/2502.12110) | Evolving memories with reinforcement, contradiction, and supersession links | Implemented: `src/neo4j-store.ts:366-371`, `src/neo4j-store.ts:450-458` |
| [RAPTOR](https://arxiv.org/abs/2401.18059) | Multiple levels of abstraction for retrieval | Partial: summary/reflection memory types and endpoints in `src/types.ts:1-5`, `src/server.ts:138-164`; hierarchical recursive summarization remains open |
| [LightRAG](https://arxiv.org/abs/2410.05779) | Combine text retrieval with graph retrieval | Implemented: `src/neo4j-store.ts:656-816` |
| [CRAG](https://arxiv.org/abs/2401.15884) | Assess evidence, correct weak retrieval, and abstain | Implemented: `src/retrieval.ts:54-91`, `src/server.ts:41-62` |
| [LongMemEval](https://arxiv.org/abs/2410.10813) | Test multi-session, temporal updates, and abstention | Implemented: `benchmark/questions.json`, `src/benchmark.ts:1-71`, `tests/neo4j.integration.test.ts:89-143` |
| [RAGChecker](https://arxiv.org/abs/2408.08067) | Separate retrieval, attribution, and faithfulness evaluation | Implemented: `src/faithfulness.ts:1-47`, `src/benchmark.ts:25-57` |
| [MemoryBank](https://arxiv.org/abs/2305.10250) | Time-based forgetting and reinforcement | Implemented: `src/retrieval.ts:25-41`, `src/neo4j-store.ts:347-349`, `src/neo4j-store.ts:900-916` |
| [MemoryOS](https://arxiv.org/abs/2506.06326) | Short-, mid-, and long-term memory promotion | Partial: categories and session summaries in `src/types.ts:1-5`, `src/server.ts:138-149`; automatic page promotion remains open |
| [Hindsight](https://arxiv.org/abs/2512.12818) | Distinguish facts, experiences, summaries, beliefs, and provenance | Implemented: typed memories, session reflection writes, and citation-locked context in `src/types.ts`, `src/server.ts`, `src/context.ts`, `src/citations.ts` |
| [HippoRAG](https://arxiv.org/abs/2405.14831) | Associative multi-hop graph propagation | Implemented: GDS Personalized PageRank from matched entities plus bounded weighted two-hop `ABOUT`/`SAME_AS`/`RELATED_TO` fallback in `src/neo4j-store.ts` |
| [Adaptive-RAG](https://arxiv.org/abs/2403.14403) | Match retrieval effort to question complexity | Implemented: complexity-specific candidate/graph budgets and lexical early stopping in `src/retrieval.ts`, `src/neo4j-store.ts` |
| [Generative Agents](https://arxiv.org/abs/2304.03442) | Memory stream, reflection, and higher-level summaries | Implemented: durable session summary/reflection, lesson/belief, and failure endpoints in `src/server.ts` |
| [Reflexion](https://arxiv.org/abs/2303.11366) | Store lessons from failed actions as episodic feedback | Implemented: `src/worker.ts:5-23`, `src/worker.ts:48-51`, `tests/worker.test.ts:4-20` |
| [Lost in the Middle](https://arxiv.org/abs/2307.03172) | Place strongest evidence at context edges and avoid redundant context | Implemented: `src/context.ts:10-22`, `src/context.ts:25-48`, `tests/context.test.ts:16-25` |
| [MemoryAgentBench](https://arxiv.org/abs/2507.05257) | Test retrieval, test-time learning, long-range understanding, and selective forgetting | Implemented locally: expanded reflection/failure/skill/reasoning cases, utility, archival, contradiction, and abstention metrics in `benchmark/questions.json`, `src/benchmark.ts`, and Neo4j integration tests |
| [Position: Episodic Memory is the Missing Piece for Long-Term LLM Agents](https://arxiv.org/abs/2502.06975) | Treat episodic memory as a first-class capability | Partial: immutable events and episode category in `src/types.ts:1-5`, `src/neo4j-store.ts:158-218`; richer episodic replay remains open |
| [MOSAIC](https://arxiv.org/abs/2607.16211) | Conflict-aware structured memory and efficient entity retrieval | Partial: typed graph conflict handling in `src/neo4j-store.ts:450-458`, entity aliases in `src/neo4j-store.ts:474-493`; hash-accelerated lookup remains open |
| [Agent Zero Memory](https://arxiv.org/abs/2608.29606) | Parallel event, graph, and citation-locked documentary memory | Implemented: event/entity/memory provenance, citation-locked context, and identifier validation in `src/context.ts`, `src/citations.ts` |
| [G-Retriever](https://arxiv.org/abs/2402.07630) | Retrieve explanatory graph neighborhoods for graph QA | Partial: graph candidates and provenance in `src/neo4j-store.ts:816-866`; Steiner-tree subgraph selection remains open |
| [ECHO: Sample-Efficient Online Learning via Hindsight Trajectory Rewriting](https://arxiv.org/abs/2510.10304) | Learn from failed trajectories by rewriting them toward successful goals | Research only: no counterfactual trajectory rewriter currently exists |
| [Hindsight Memory-PRM](https://arxiv.org/abs/2608.29605) | Measure memory utility from retrieval, citations, and answer interventions | Research only: no causal memory-utility critic or deletion/re-answer probe exists |
| [DEVIL’S ADVOCATE](https://arxiv.org/abs/2405.16334) | Anticipatory reflection, post-action evaluation, and plan revision | Research only: no anticipatory action-review loop exists |
| [OSWorld](https://arxiv.org/abs/2404.07972) | Evaluate memory/reflection in multimodal computer-use agents | Research only: this service currently stores text events and tool records, not screenshots or multimodal observations |
| [Dynamic Memory-Based Curiosity](https://arxiv.org/abs/2208.11349) | Grow memory when the current memory cannot explain a state | Research only: no exploration environment or curiosity signal is connected to the service |
| [Zoology](https://arxiv.org/abs/2312.04927) | Content-addressable associative recall | Partial: vector retrieval in `src/neo4j-store.ts:735-785`; no learned associative-memory architecture exists |
| [Voyager](https://arxiv.org/abs/2305.16291) | Store, retrieve, compose, and validate executable skills | Implemented metadata layer: procedures carry executable command, parameters, and safety policy fields in `src/types.ts`, `src/neo4j-store.ts`, and `src/mcp.ts`; execution remains approval-gated outside this service |
| [Memoria](https://arxiv.org/abs/2512.12686) | Combine session summaries with weighted user/entity graphs | Partial: session summaries/reflections in `src/server.ts:138-164`, entity graph in `src/neo4j-store.ts:474-493`; weighted personalization remains open |
| [Lost in the Middle, and In-Between](https://arxiv.org/abs/2412.10079) | Account for positional and inter-evidence effects in multi-hop context | Partial: edge-aware ordering in `src/context.ts:10-22`; multi-hop positional benchmark cases remain open |
| [The Rise of Verbal Reinforcement Learning](https://arxiv.org/abs/2609.01597) | Treat natural-language feedback as a learning signal | Partial: failure feedback memories in `src/worker.ts:5-23`; broader verbal-reward policy learning remains open |
| [AB-RAG](https://arxiv.org/abs/2606.29090) | Stop or retrieve more based on confidence and a retrieval budget | Implemented bounded adaptive budgets and lexical early stopping in `src/retrieval.ts` and `src/neo4j-store.ts`; answer-time iterative retrieval remains open |
| [MBA-RAG](https://arxiv.org/abs/2412.01572) | Use bandit-style strategy selection to balance quality and cost | Research only: no learned bandit or online retrieval policy exists |

| [τ-bench](https://arxiv.org/abs/2406.12045) | Simulated user conversations against stateful APIs with ground-truth final state | Implemented locally: `src/simulator.ts` generates seeded sessions, probes, and an independent oracle; live execution uses the real event, memory, archival, and recall APIs |
| [AgentDojo](https://arxiv.org/abs/2406.13352) | Dynamic prompt-injection and untrusted-tool-data evaluation | Implemented locally: malicious-memory and secret-leak probes in `src/simulator.ts`; broader tool-domain attack suites remain deferred |
| [EMemBench](https://openreview.net/pdf?id=dFQLfagXEK) | Interactive and multimodal long-term memory evaluation | Partial: deterministic interactive text scenarios are implemented; multimodal observations remain deferred |

The LongMemEval adapter in `src/longmemeval.ts` and `src/run-longmemeval.ts` accepts the upstream `haystack_sessions`/`answer_session_ids` schema, preserves timestamps and evidence-session labels, and reports retrieval-layer metrics without treating an LLM judge as ground truth.
| [BRIGHT](https://arxiv.org/abs/2407.12883) | Test reasoning-intensive retrieval beyond lexical similarity | Implemented local reasoning-intensive cases plus JSON/JSONL external case loading with `BENCHMARK_CASES_FILE` in `src/benchmark-cases.ts`, `src/benchmark.ts`, and `benchmark/questions.json` |

## Runtime flows

### Write path

`conversation -> Conversation/Message nodes -> durable Event nodes -> consolidate_event Job -> model extraction -> Entity/Memory/Claim/Procedure nodes -> embedding and reconsolidation jobs`

Conversation messages remain immutable `Event` source records while also receiving a graph-native `Message` projection. Decision events additionally receive a `ReasoningTrace` projection; this stores concise decision records, not hidden chain-of-thought. Conversation batches are written with one Neo4j transaction and are safe to replay through idempotency keys.

The event write is idempotent by explicit ID or idempotency key. A provider failure marks the job failed while leaving the event available for retry or inspection.
Batch event writes preflight existing IDs inside the write transaction; conflicts fail before graph mutations, preserving crash-safe all-or-nothing ingestion.

### Read path

`query -> complexity/adaptive budget -> full-text + weighted graph propagation + vector candidates -> reciprocal-rank fusion -> optional reranker -> Lost-in-the-Middle-aware citation-locked context -> corrective retrieval if weak -> abstention decision`

## Simulation and evaluation harness

`src/simulator.ts` provides a deterministic personal-world generator, seeded event timeline, independent truth oracle, and metrics for retrieval, temporal updates, provenance, abstention, isolation, forgetting, and prompt-injection resistance. `src/simulate.ts` runs an offline self-check by default and can run the same generated world through the live API with `SIMULATION_LIVE=1`. Each live run uses a unique project ID so simulation data cannot contaminate benchmark fixtures. The simulator intentionally separates generated wording from authoritative truth: an optional future LLM user simulator may make dialogue more natural, but it must not define the expected answer.

Temporal routes include historical memories; normal routes restrict results to active, currently valid memories. Vector providers are optional, so the service remains useful with Neo4j full-text and graph retrieval alone.

Hybrid retrieval ranks lexical, vector, and graph sources independently before combining them. This avoids comparing incompatible raw scores. Set `NEO4J_VECTOR_QUERY_MODE=search` on Neo4j versions supporting the native `SEARCH` clause; the default `procedure` mode retains compatibility with older Neo4j 5 deployments.

## Operational guarantees and limits

- Neo4j stores the durable graph; JSON snapshots and Neo4j native dumps provide recovery options.
- Job leases allow another worker to reclaim work after a worker crash.
- API keys, admin keys, body limits, CORS configuration, and admin-only job/export endpoints protect the service boundary.
- Admin metrics expose bounded retrieval latency, route/method usage, retrieval errors, and answer-validation rejection rates without storing request content.
- Startup records and checks a `SchemaMetadata` version marker; a newer unsupported database version fails closed, while future migrations can advance the marker explicitly instead of silently changing data.
- Snapshot restore is explicit-confirmation only and supports a dry-run validation mode.
- Utility feedback is persisted on memories and contributes a bounded ranking signal; selective archival changes durable-memory visibility while leaving immutable events and provenance intact.
- Context packs require `[type:id]` citations, and citation validation rejects identifiers not present in retrieved evidence.
- Answer validation is an explicit boundary at `POST /v1/answers/validate`: every factual answer segment must cite retrieved evidence, cited identifiers must belong to that evidence set, and deployments may require an independent faithfulness judge before accepting an answer.
- Context-hub analytics use GDS PageRank when a named projection exists and a portable degree fallback otherwise; graph propagation uses weighted `ABOUT`, `SAME_AS`, and `MENTIONS` edges.
- The benchmark measures retrieval hit rate, multi-session/temporal/multi-hop behavior, abstention, attribution, latency, and configurable estimated cost. Offline runs use a conservative evidence-linkage/term proxy; deployments can configure an independent `POST /judge` entailment service and require it in strict mode.
- `bun run ablation` runs the same benchmark cases through lexical-only, vector-only, graph-only, and hybrid method selections, reporting hit rate, abstention accuracy, mean latency, p95 latency, and per-case measurements so retrieval improvements are supported by component-level comparisons.
- `bun run simulate` is a versioned (`sim-v1`), seeded regression harness. Its user policy is deterministic/template-based; an LLM-driven user model, external LongMemEval/MemoryAgentBench execution, multimodal episodes, and Langfuse/Phoenix exporters remain intentionally deferred. Those additions must retain the independent oracle rather than using a simulated model to define its own ground truth.
