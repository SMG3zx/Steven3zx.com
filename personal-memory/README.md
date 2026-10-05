# Personal Memory

See [UX-RESEARCH.md](./UX-RESEARCH.md) for the evidence-backed service blueprint, data-entry paths, usability scenarios, and product priorities.

See [USABILITY-TEST-PLAN.md](./USABILITY-TEST-PLAN.md) for the moderated study protocol, synthetic-data safeguards, tasks, and publish-readiness thresholds.

The `/memories` review page lists scoped memories with provenance and lifecycle status, and provides in-context correction, archival, and retraction dialogs. Archival excludes a memory from future retrieval while preserving provenance; retraction shows source/replacement impact before confirmation and returns an audit trace receipt.

The `/jobs` page exposes queued, running, completed, and failed consolidation/reconciliation jobs, including attempts, source context, errors, bounded retry controls, and in-context trace receipts for preview, approval, rejection, and retry actions.

The `/status` page provides redacted health, ChatGPT/Codex authentication, and provider recovery guidance.

The `/metrics` page provides redacted retrieval latency/error, answer-validation, uptime, route, and method aggregates without exposing prompts, answers, or memory content.

The `/chat` page provides session/project/global scope selection, explicit per-turn capture, visible working/authentication states, retry guidance, citation/evidence feedback, accessible in-context correction/retraction dialogs, and direct links to Memory Review, Metrics, and the Audit Console. The `/ingest` page is the browser capture surface for source events, documents, and ordered conversations; it keeps scope visible, previews the payload before an explicit confirmation, uses stable identifiers for safe event/document/conversation retries, returns receipt/trace links, and uses the same authenticated API access control as the review and audit pages. The `/sessions` page provides the matching reviewed-capture flow for durable session summaries and reflections, including lessons and failures. The `/imports` page reads supported local text-like files in the browser, previews content, requires explicit confirmation before creating a traceable document, and rejects files above the server-safe request limit before reading or sending them.

Run `bun run ux:smoke` against the local API to verify the main user journeys: status, chat scope, memory/job review, ingress receipts, source provenance, scope isolation, and audit discoverability.

Run `bun run mcp:smoke` to exercise the newline-delimited MCP protocol, enumerate all 15 exposed tools, and run isolated synthetic paths for events, batches, conversations, reflections, recall/context, entities, documents/chunks, claims, procedures, and memory utility/archive/retraction. Every exercised path must return a trace receipt.

Set `CONSOLIDATION_REQUIRE_APPROVAL=1` to hold new consolidation and reflection jobs in `pending` approval until an administrator approves them from `/jobs`; rejected jobs are marked failed with the review reason.

For offline UX/lifecycle testing, set `CONSOLIDATION_MODE=heuristic`. This mode only proposes an event when its metadata explicitly contains `remember: true`; otherwise it produces no durable candidate. The normal `model` mode remains the default.

Standalone, durable memory and GraphRAG service for Codex and other agents.

The service records immutable events first, then exposes searchable memories and graph relationships. It is intentionally independent of MeinFactory and any other project.

Open `http://127.0.0.1:4781/chat` while the API is running for the private memory-aware chat UI, `/ingest` for browser-based event/document/conversation capture, `/sessions` for summaries and reflections, `/imports` for local document imports, or `/` for the local audit console. The chat UI uses the local Codex App Server when enabled; click `Connect ChatGPT` to start the supported device-code login. The UI shows the verification code, waits for completion, and reports when authentication is ready or still pending. Retrieval requests emit redacted hierarchical traces; administrators can query them with `GET /v1/admin/audit/runs`, inspect a waterfall with `GET /v1/admin/audit/traces/:traceId`, replay retrieval inline, or export a bounded JSON report with `GET /v1/admin/audit/export`. The consoles are intentionally local and do not expose credentials.

Chat also provides a per-turn `Save this turn to memory` control. It is checked by default for compatibility, but unchecking it still allows the assistant to answer while skipping durable user/assistant conversation events; the response and trace record whether the turn was captured.

Audit retention is bounded by `AUDIT_MAX_RUNS` (default 5,000) and `AUDIT_RETENTION_HOURS` (default 24; set to `0` for no age-based expiry). Set `AUDIT_FILE` to persist the redacted buffer across restarts; Docker stores it at `/data/codex/audit.json` on the `codex-data` volume. Audit data is diagnostic and is not a durable replacement for conversation/memory provenance.

Event and memory text is searched with Neo4j full-text indexes; the service does not create range indexes on large content fields. `MAX_CONTENT_BYTES` defaults to 1,000,000 UTF-8 bytes and rejects oversized event batches before opening a Neo4j write transaction. Schema initialization automatically removes legacy `event_content` and `memory_content` range indexes from older databases.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the research-to-implementation traceability map, retrieval flow, and explicit limitations.

Memories are explicitly typed as working context, facts, episodes, beliefs, reflections, failure lessons, semantic knowledge, procedural runbooks, or supporting profile/preference/decision/claim/summary records. Events remain the immutable source of truth; consolidation promotes them into these durable views.

## Run

1. Start Neo4j (Aura or local Neo4j 5.x). For local development, run `docker compose up -d` and wait for its health check before starting the API.
2. Copy `.env.example` to `.env` and set credentials.
3. Install dependencies: `bun install`.
4. Start the HTTP API: `bun run dev`.
5. Start the consolidation worker in a second process: `bun run worker`.
6. Create a JSON snapshot backup with `bun run backup`.
7. Validate a snapshot without writing with `RESTORE_DRY_RUN=1 bun run restore -- path/to/snapshot.json`. Validation checks node identity uniqueness, labels, relationship types, and dangling relationship references. Restore it only after review and with `RESTORE_CONFIRM=RESTORE_PERSONAL_MEMORY`.

For a fully containerized standalone deployment, copy `.env.example` to `.env`, adjust credentials, and run `docker compose up -d --build`. Compose starts Neo4j, the API, and the consolidation worker; the API is available on port 4781. The API image includes the Codex CLI, and the `codex-data` volume preserves its subscription authentication state. After startup, open `/chat` and use `Connect ChatGPT` once to complete device-code login inside the container.

To run local embeddings and reranking, set `EMBEDDING_BASE_URL=http://embedding:8080` and `RERANKER_BASE_URL=http://reranker:8080` in `.env`, then run `docker compose --profile local-models up -d`. The profile uses Hugging Face Text Embeddings Inference: Qwen/Qwen3-Embedding-0.6B for 1,024-dimensional embeddings and BAAI/bge-reranker-base for reranking. The model weights are cached in the `model-cache` volume. Host ports default to 18080/18081 and can be changed with `EMBEDDING_HOST_PORT` and `RERANKER_HOST_PORT`. For NVIDIA hosts, set `TEI_IMAGE=ghcr.io/huggingface/text-embeddings-inference:cuda-1.9` and launch with `docker compose -f docker-compose.yml -f docker-compose.gpu.yml --profile local-models up -d`; the override reserves one GPU for each inference service. The model services deliberately do not auto-restart because CPU inference can be resource-intensive; start the profile explicitly when needed.

The MCP adapter is started separately with `bun run mcp`. Configure Codex to launch that command as a local MCP server. MCP exposes event, conversation capture, recall, bounded context-pack, memory, entity, document, chunk, claim, and procedure operations; memory creation supports the same structured fields as REST, including session scope, entities, temporal validity, and subject/predicate/object claims. For real Codex history, call `remember_conversation` once per turn or batch, passing the stable session ID and ordered user/agent/tool messages. Test and benchmark data should continue using `source: "test"` or a dedicated benchmark source.

Example MCP configuration:

```json
{
  "mcpServers": {
    "personal-memory": {
      "command": "bun",
      "args": ["run", "mcp"],
      "cwd": "D:/steven3zx.com/personal-memory",
      "env": {
        "MEMORY_API_URL": "http://127.0.0.1:4781"
      }
    }
  }
}
```

## Initial API

- `GET /chat` — serve the private memory-aware chat UI; it keeps ChatGPT credentials inside the local Codex App Server.
- `GET /ingest` — serve the browser capture UI for events, documents, and ordered conversations, with scope and receipt/trace feedback.
- `POST /v1/chat` — retrieve bounded memory context, send a turn through Codex App Server, persist the conversation, and return trace/citation metadata.
- `GET /v1/chat/auth` and `POST /v1/chat/auth/start` — inspect or begin the local ChatGPT device-code authentication flow.
- `POST /v1/events` — append an immutable user, agent, tool, decision, or artifact event.
- `POST /v1/events/batch` — append 1–100 ordered immutable events with per-event idempotency.
- `POST /v1/conversations` — append an ordered Codex conversation batch with stable session/project provenance and `source: "codex"` defaults. Batches are committed in one Neo4j transaction and project to `Conversation`/`Message` nodes; decision messages also create `ReasoningTrace` nodes.
- `POST /v1/sessions/:sessionId/summary` — persist a durable `summary` memory for a completed session, optionally linked to its source event IDs.
- `POST /v1/sessions/:sessionId/reflection` — persist a reflection plus lesson/belief and failure memories from a session.
- `POST /v1/recall` — retrieve relevant event and memory context.
- `GET /v1/admin/context-hubs` — rank connected entities and durable knowledge by graph degree; this is a safe fallback for deployments where Neo4j GDS is not installed.
- `POST /v1/memories/:id/utility` — record whether retrieved memory was useful; utility feeds ranking.
- `POST /v1/memories/:id/archive` — selectively archive a durable memory without deleting its immutable events.
- `POST /v1/context` — retrieve a bounded, newline-delimited context pack with provenance labels for direct agent prompt injection.
- `POST /v1/answers/validate` — reject answers with missing/invalid citations or failed external faithfulness judgment.
- `POST /v1/memories` — create a curated memory with provenance.
- `POST /v1/memories/:id/retract` — retract a curated memory while preserving its event and provenance history.
- `POST /v1/entities` — upsert a canonical entity for graph linking.
- `POST /v1/documents` — upsert a source document.
- `POST /v1/chunks` — attach an embedded or embeddable chunk to a document.
- `POST /v1/claims` — create a source-backed claim between entities.
- `POST /v1/procedures` — create or update a source-backed procedural runbook.
- `POST /v1/jobs/next` — atomically claim the next queued enrichment job; admin credential required.
- `POST /v1/jobs/:id/complete` — complete or fail an enrichment job; admin credential required.
- `POST /v1/jobs/:id/retry` — requeue an exhausted failed job; admin credential required.
- `GET /v1/admin/export` — export an authenticated graph snapshot for backup.
- `GET /v1/admin/stats` — inspect authenticated node, relationship, and job counts without exporting content.
- `GET /v1/admin/schema` — inspect the stored schema version and applied migration identifiers.
- `GET /v1/admin/metrics` — inspect bounded retrieval latency/error/method metrics and answer-validation rejection counts without exposing content.
- `GET /v1/admin/audit/runs` — list redacted trace roots with optional status, kind, name, trace ID, and limit filters.
- `GET /v1/admin/audit/traces/:traceId` — inspect the hierarchical child runs for one trace.
- `GET /v1/admin/audit/export` — export the bounded in-memory audit buffer for local review or regression artifacts.
- `GET /v1/admin/audit/stats` — inspect audit count, configured maximum, and retention window.
- `POST /v1/admin/audit/traces/:traceId` with `{ "confirm": "replay" }` — safely replay a memory-recall trace.
- `POST /v1/admin/audit/clear` with `{ "confirm": "clear-audit" }` — clear the local audit buffer.
- `GET /v1/admin/providers` — inspect which model, embedding, reranking, and faithfulness providers are configured and reachable without exposing credentials. States are `disabled`, `ready`, `reachable`, or `unreachable`.
- `GET /health` — verify service and Neo4j connectivity.

Reusing an event ID or `Idempotency-Key` with different immutable content returns HTTP `409`; identical retries return the original event identity.

Recall responses include the selected route, route-aware evidence ranking, abstention status, retrieval methods, and provenance IDs. Procedural routes prioritize runbooks, graph routes prioritize claims/entity paths, temporal routes include historical evidence, and semantic routes prioritize memory/chunk candidates. If the initial query is empty or weak, the API performs a normalized corrective retrieval pass and reports `corrected` plus `correctionQuery`.

When `EMBEDDING_BASE_URL` is configured, the service calls a local `/embed` endpoint (for example, Hugging Face Text Embeddings Inference running Qwen3-Embedding) when writing memories and during recall. Neo4j creates 1,024-dimensional vector indexes by default; set `EMBEDDING_DIMENSIONS` before the first schema initialization if using another model. Recall excludes `source: "test"` evidence by default; pass `includeTestData: true` only for diagnostics and benchmarks.
The configured benchmark project (default `benchmark-suite`) is also excluded from unscoped global recall; benchmark and diagnostic queries should use an explicit project scope.

Hybrid retrieval uses reciprocal-rank fusion across lexical, vector, and graph candidates instead of comparing raw scores from different indexes. On Neo4j versions that support the native Cypher `SEARCH` clause, set `NEO4J_VECTOR_QUERY_MODE=search`; leave it as `procedure` for older Neo4j 5 deployments.

For a self-hosted Qwen3 setup, point `EMBEDDING_BASE_URL` at the embedding server and keep `EMBEDDING_DIMENSIONS=1024` for Qwen3-Embedding-0.6B. The client sends `{ "inputs": [...] }` to `/embed` and accepts either a raw vector list or an `{ "embeddings": [...] }` response. Set `RERANKER_BASE_URL` to a compatible `/rerank` service when reranking is desired; both integrations are optional and do not change the Neo4j data model.

Events are idempotent when the same explicit event ID is submitted. Curated memories can supersede earlier memories while preserving the original evidence and relationship.

Each event also creates a durable `consolidate_event` job. The worker claims jobs, extracts memories/entities/claims/procedures, calls the embedding service, and marks the job completed without changing the original event record.

Conversation sessions also create one debounced `reflect_session` job. With `MODEL_BASE_URL` configured, the worker reflects over the session and persists source-linked reflection, belief/lesson, and failure memories. `REFLECTION_DELAY_SECONDS` controls the quiet period; without a model provider, reflection jobs remain queued rather than inventing content.

The worker uses an OpenAI-compatible `/chat/completions` endpoint configured with `MODEL_BASE_URL`, `MODEL_NAME`, and optionally `MODEL_API_KEY`. This can point to a local Qwen/Ollama-compatible service or a hosted provider without changing the memory data model.

Run `bun run benchmark:seed` once while the API is running, then `bun run benchmark` to execute the reproducible retrieval benchmark in `benchmark/questions.json`. It covers fact, information extraction, temporal reasoning, knowledge updates/supersession, multi-hop, multi-session, reflection, failure, executable-skill, contradiction, selective forgetting, attribution, faithfulness, and abstention cases, and reports hit rate, attribution rate, abstention accuracy, reasoning-support rate, latency, estimated cost, and per-case measurements. External BRIGHT- or MemoryAgentBench-derived JSON/JSONL cases can be supplied with `BENCHMARK_CASES_FILE=path/to/cases.json` and optional `BENCHMARK_CASES_FORMAT=jsonl`; the loader accepts `query`/`question` plus `expectedContains`/`expected`/`gold` or an answer string. Offline faithfulness uses a conservative evidence-linkage/term proxy. Set `FAITHFULNESS_JUDGE_BASE_URL` to use an independent provider exposing `POST /judge` with `{query,answer,evidence}` and a response containing `{faithful:boolean}`; set `FAITHFULNESS_REQUIRE_JUDGE=1` with strict mode when semantic judging is required. Set `MEMORY_RECALL_COST_USD` when a deployment wants to account for per-request provider cost.

Benchmark and ablation reports include the input dataset source, SHA-256 hash, format, retrieval configuration, judge mode, and optional `BENCHMARK_MODEL_ID`, `BENCHMARK_EMBEDDING_ID`, and `BENCHMARK_RERANKER_ID` identifiers so results can be compared without confusing synthetic, external, retrieval-only, or end-to-end evaluations. The LongMemEval adapter labels itself retrieval-only and does not claim parity with the upstream judge.
Set `BENCHMARK_STRICT=1` to make the command fail unless all quality gates are 100%; optionally set `BENCHMARK_MAX_COST_USD` for a cost ceiling.
Set `BENCHMARK_OUTPUT_FILE=path/to/report.json` to persist a run. Set `BENCHMARK_BASELINE_FILE=path/to/prior-report.json` to include baseline metrics and per-metric deltas in the new report.
Run `bun run ablation` to compare lexical-only, graph-only, and hybrid retrieval on the same cases; set `ABLATION_MODES=lexical,vector,graph,hybrid` and `ABLATION_OUTPUT_FILE=path/to/report.json` to customize and persist the comparison.

Run `bun run evaluate` for the combined release evaluation: the existing benchmark, retrieval ablation, and deterministic simulator in sequence.

For the upstream LongMemEval dataset, download the desired authorized JSON release separately from the [official LongMemEval-V2 repository](https://github.com/xiaowu0162/LongMemEval-V2) (its repository documents `data/download_data.py`), then run `LONGMEMEVAL_FILE=path/to/longmemeval_s.json LONGMEMEVAL_LIMIT=10 bun run benchmark:longmemeval`. The adapter preserves session IDs and timestamps, ingests each item into an isolated project, and reports session Recall@any/Recall@all, answer-term coverage, abstention, latency, cost, and per-question evidence. It intentionally measures retrieval separately from the official LLM-judge QA score; use the upstream evaluation script for end-to-end answer scoring.

The Metrics page keeps a bounded redacted minute trend (up to 60 buckets) and reports actionable retrieval alerts. Set `METRICS_ERROR_RATE_ALERT` (default `0.1`) or `METRICS_P95_ALERT_MS` (default `1000`) to tune thresholds; no prompt, answer, memory, or query content is retained in the trend.

Run `bun run simulate` to execute the deterministic research-backed personal-world simulator. It generates seeded multi-session events, stateful simulated-user turns (including corrections and follow-up questions), and probes for current facts, temporal updates, failures, selective forgetting, provenance, and malicious instructions, then scores them with an independent oracle. Set `SIMULATION_SEED` for reproducibility, `SIMULATION_SEEDS=1,2,3` for a repeatable regression matrix, and `SIMULATION_OUTPUT_FILE` to persist the world and full report. Reports include hit rate, abstention, provenance, citation, isolation, security, category-level quality, mean/p95 latency, and estimated cost. Console output redacts expected and observed memory IDs by default; set `SIMULATION_VERBOSE=1` when detailed IDs are intentionally needed for local debugging. Set `SIMULATION_LIVE=1` to ingest the generated events and memories through the running API, exercise archival and retrieval, validate citations, and evaluate the live system; this uses an isolated project scope. Set `SIMULATION_TRACE_FILE=path/to/traces.json` during a live run to export an OpenTelemetry-shaped trace envelope for Phoenix/Langfuse-compatible ingestion or inspection without adding either platform as a dependency. The simulator is inspired by LongMemEval, MemoryAgentBench, EMemBench, Generative Agents, tau-bench, and AgentDojo. It is a deterministic regression harness, not a replacement for human evaluation or the full external datasets.

In production, set `NODE_ENV=production` and provide `MEMORY_API_KEY`; use `MEMORY_ADMIN_KEY` for backup/export and job-administration credentials. The web shell remains reachable so a user can open the interface, then use its `API access` control to enter a bearer key for the current browser session. The key is stored only in `sessionStorage`, attached to API requests, and can be cleared from the same control; do not paste keys into URLs or persist them in shared browser profiles. The JSON snapshot preserves graph nodes and relationships, while Neo4j's native dump tooling remains the recommended disaster-recovery backup for a complete database restore.

Jobs use a lease. If a worker crashes after claiming a job, another worker can reclaim it after `JOB_LEASE_SECONDS`; completed jobs remain idempotent and original events are never modified.
Batch event ingestion performs an idempotency preflight inside the same Neo4j transaction, so a conflicting replay aborts without partial event, message, job, or ingest-counter writes.
Transient consolidation errors are retried with bounded exponential backoff using `JOB_MAX_ATTEMPTS` and `JOB_RETRY_BACKOFF_SECONDS`; exhausted jobs become `failed` while the source event remains intact.
The API and consolidation worker handle `SIGINT`/`SIGTERM` and close Neo4j connections during shutdown.
