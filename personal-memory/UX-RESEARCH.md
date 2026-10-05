# Personal Memory UX research and service blueprint

## Purpose

Personal Memory is a private, memory-aware assistant. The experience must make three things obvious at every step:

1. what data entered the system and under which scope;
2. what the system remembered, retrieved, or declined to use;
3. what the user can inspect, correct, retract, or replay.

This document turns current research and the existing implementation into a product plan. It is intentionally written as a working blueprint rather than a claim that the system has completed formal human-subject usability testing.

The web interface is a first-class part of the experience goal. Chat, memory review, consolidation jobs, system status, and the audit console form one connected web flow: each capture or answer should show its current state, scope, provenance, and trace receipt, with a clear path to inspect, correct, retract, retry, or recover.

## Evidence that shapes the design

| Evidence | Finding | Product implication |
| --- | --- | --- |
| [MemoAnalyzer](https://arxiv.org/abs/2410.14931) | Interviews and a field evaluation found low awareness of what assistants retain and value in proactive privacy controls. | Show capture status, memory scope, and retention controls at the moment data enters the system. |
| [RAG memory privacy study](https://arxiv.org/abs/2508.07664) | Users reported incomplete mental models and wanted granular review, editing, deletion, categorization, and transparency. | Every memory should have provenance, status, scope, and a direct correction/forget path. |
| [Conversational LLM privacy walkthrough](https://arxiv.org/abs/2602.10684) | Natural-language privacy controls can be ambiguous, especially for derived data and shared ownership. | Treat “forget this” as an explicit operation with a confirmation and show what source and derived records are affected. |
| [Human-centered XAI survey](https://arxiv.org/abs/2210.11584) | Explanations serve different goals: understanding, trust, usability, and collaboration. | Do not expose raw traces alone; pair a readable explanation with an expandable technical trace. |
| [Effects of explanations in AI-assisted decisions](https://doi.org/10.1145/3519266) | Good explanations help users understand uncertainty and calibrate trust. | Explain why evidence was used, show uncertainty/no-evidence states, and avoid implying that retrieval equals truth. |
| [LangSmith observability](https://www.langchain.com/langsmith/observability) | Commercial agent tooling makes end-to-end traces, child tool calls, latency, cost, errors, and feedback first-class. | The audit console should be a searchable waterfall with child runs, redaction, replay, and feedback attached to the relevant run. |
| [LangSmith trace-to-evaluation workflow](https://www.langchain.com/resources/agent-evals) | Commercial agent teams increasingly reuse production traces as evaluation examples, combining trace inspection, human feedback, offline regression sets, and online evaluators. | Keep trace receipts and redacted run metadata structured so a reviewed failure can later become a benchmark/regression case; this remains a publish-readiness enhancement beyond the current export/replay flow. |
| [Contextual privacy in conversational memory](https://arxiv.org/abs/2609.22720) | Recent user research on sensitive disclosures emphasizes control over scope, provenance, retention, and access, with users doing “boundary work” after information moves contexts. | Continue prioritizing scope-first capture, source/derived separation, explicit retention semantics, and reviewable correction/forget flows over relying only on per-message toggles. |
| [OpenAI Memory controls](https://help.openai.com/en/articles/8590148-memory-faq%23.pdf) | Memory and chat history are separate; deletion of one does not necessarily delete the other. | Distinguish source conversations, derived memories, and audit records, and explain deletion semantics. |
| [Gemini memory controls](https://support.google.com/gemini/answer/16598469?co=GENIE.Platform%3DDesktop&hl=en) | Users can ask whether past chats were used, correct memories directly, and delete source chats. | Provide “why was this used?” and source-level controls from the answer surface. |
| [Claude Projects](https://support.anthropic.com/en/articles/9517075-what-are-projects) | Scoped project knowledge creates a clear boundary around what chats can use. | Make session, project, and private-global scope visible before ingestion and retrieval. |

## End-to-end service blueprint

| Stage | User goal | System behavior | User-visible state | Failure/recovery |
| --- | --- | --- | --- | --- |
| Capture | Share a message, document, tool result, or import intentionally | Classify source, scope, sensitivity, and consent | “Captured from Chat / MCP / REST / document”; preview and scope | Reject unsafe content with a reason; allow retry or discard |
| Ingest | Know whether data arrived | Store an immutable event and assign an ID | Receipt with source, timestamp, scope, and processing status | Idempotent retry; never silently duplicate |
| Consolidate | Understand what became memory | Extract candidates, link provenance, detect conflicts, queue review | Proposed memory cards: type, confidence, sources, “save / edit / ignore” | Keep raw event even when extraction fails; show failed job and retry |
| Store | Control persistence | Store active/superseded/archived/retracted records with retention policy | Status, scope, retention, source links | Retraction propagates to derived records where possible and reports exceptions |
| Retrieve | Know why something influenced an answer | Run lexical/vector/graph retrieval, rank, deduplicate, and apply scope filters | “Memory used” panel with evidence, source IDs, and relevance explanation | Empty state: “No private-memory evidence”; never fabricate from absence |
| Answer | Get a useful response without losing control | Generate with citation lock and validate citations | Answer, citation chips, confidence/uncertainty, trace link | Partial result clearly labeled; retry and inspect trace |
| Correct | Repair a wrong or stale memory | Record correction/supersession and preserve provenance | Edit, correct, mark outdated, or “don’t use for this topic” | Show old/new values and affected future retrievals |
| Forget | Remove information intentionally | Retract source and/or derived memory according to selected scope | Preview impact, confirm, completion receipt | Explain records that cannot be removed immediately and why |
| Audit | Diagnose behavior | Persist redacted root/child runs and metrics | Searchable waterfall; the run filter accepts a run name or copied trace ID, with inputs/outputs, errors, replay, and export | Redaction indicator and safe local-only mode |
| Recover | Resume after auth, provider, Neo4j, or network failure | Keep event/job state and retry safely | Specific next action, not a generic error | Backoff, idempotency key, health checks, and downloadable diagnostic |

## Data-entry paths to support explicitly

- **Private chat:** user message → session event → retrieval → answer event; memory capture should be opt-in or clearly indicated.
- **Codex/App Server:** thread and turn events → tool calls/results → assistant response; preserve provider metadata without storing credentials.
- **MCP and REST:** authenticated event envelope → validation → idempotent ingest; return a receipt and trace ID.
- **Documents and imports:** source manifest → chunking/extraction → candidate memories → provenance graph; show progress and partial failures.
- **Benchmarks and simulations:** synthetic scope by default; make it impossible for test data to appear in private retrieval without an explicit import.

### Web-interface flow

- **Chat:** show immediate working feedback, the selected memory scope, evidence/no-evidence state, citations, a trace link, and an explicit per-turn “Save this turn to memory” choice.
- **Memory review:** show what is stored, where it came from, its lifecycle status, impact before forgetting, and a receipt after correction or retraction.
- **Jobs:** show ingestion/consolidation progress, candidate previews, approval state, failures, retry, and the source trace.
- **Audit console:** show searchable redacted traces with a readable summary first and the technical waterfall on demand.
- **Status and recovery:** show dependency/auth/provider state without secrets and give the next safe action.

## Highest-value UX changes

### P0: trust and feedback loop

- Show a working state immediately after Send, including the current phase and elapsed time.
- Show the trace link in the answer and deep-link into the audit console.
- Show a “Memory used” panel with each evidence item, source IDs, and useful/not-useful feedback.
- Make “no private-memory evidence” a positive, explicit state.
- Surface provider/auth/database failures with a recovery action.

### P1: control and provenance

- Add scope and memory-mode controls to chat: session-only, project, or private-global; remember on/off.
- Implemented for browser ingestion: `/ingest` previews the scoped event/document/conversation payload and requires explicit in-context confirmation before saving. Proposed-memory review for consolidation remains a separate job-review path.
- Add correction, supersession, archive, and forget flows with impact previews.
- Link audit child runs to the user-facing evidence and feedback event.
- Keep chat prompts, answers, retrieved context, and Codex tool payloads out of persisted diagnostic traces; retain counts, IDs, citations, timing, and redacted operational metadata instead.
- Treat benchmark and simulator output as an observability surface too: console reports default to counts rather than dumping candidate memory IDs, while explicit local debug output and persisted reports remain opt-in.

### P2: scale and learning

- Implemented in the current slice: Audit Console name/trace-ID/status/kind filters, one-click trace-ID copying, and a locally saved filter preference, plus a redacted `/metrics` dashboard for retrieval latency/error, answer-validation rejections, uptime, routes, methods, bounded minute trends, configurable retrieval alerts, and optional estimated retrieval cost. The audit export path is already available.
- Implemented: benchmark comparison by retrieval method through the ablation runner; outstanding: repeat it across external corpora and model versions.
- Implemented in the current slice: accessibility labels/live regions, keyboard navigation, reduced-motion mode, responsive layout, and in-context action feedback. Outstanding: formal accessibility review across the target device/browser matrix.

## Usability-oriented acceptance scenarios

1. A user sends a question and can tell within one second that work started; they can distinguish memory retrieval from provider generation.
2. A user can answer “what memory did you use?” without opening the audit console.
3. A user can open the trace from the chat and see the root run, retrieval children, model run, validation, duration, and errors.
4. A user can mark one retrieved memory not useful; the feedback is attached to that memory and trace, with no raw secret exposed.
5. A user can correct or retract a memory and see its status and source provenance change.
6. A provider failure leaves a readable error, preserves the user event exactly once, and offers retry.
7. A restart does not erase retained audit records; retention and redaction remain enforced.
8. Synthetic benchmark/simulation data is visibly separated from private user data.

## Measurement plan

Track task-level measures, not only model scores:

- time to first visible working state;
- completion rate for “find why this was used” and “forget this memory”;
- correction success and accidental-retention rate;
- percentage of answers with understandable evidence/no-evidence state;
- trace discoverability from chat;
- duplicate ingest rate, stale-memory rate, citation validation rate;
- p50/p95 latency, provider error rate, retry success, and audit persistence across restart;
- benchmark hit rate, abstention accuracy, provenance/attribution, citation validity, and isolation.

The latest 15-case ablation reinforces the hybrid default: lexical-only and hybrid retrieval both scored 100% hit rate and abstention accuracy, while graph-only scored 40% hit rate and 60% abstention accuracy. The latest combined run scored 100% hit rate, attribution, faithfulness, reasoning support, and abstention accuracy, with zero estimated provider cost; its mean latency was 99.36 ms and p95 was 167.05 ms. The latest lexical/graph/hybrid ablations measured p95 latencies of 90.62/104.03/136.9 ms respectively. These are local regression signals, not evidence of generalization: the benchmark corpus is synthetic, and its faithfulness checks used the lexical fallback rather than an independent semantic judge. The product should present graph retrieval as one contributing explanation/source, not as a standalone truth path; live latency remains a separate optimization target.

## External benchmark readiness

| Public source | What it measures | Current readiness and boundary |
|---|---|---|
| [LongMemEval](https://arxiv.org/abs/2410.10813) / [official LongMemEval-V2 repository](https://github.com/xiaowu0162/LongMemEval-V2) | Long-term interactive memory, including extraction, multi-session and temporal reasoning, updates, and abstention; V2 additionally evaluates multimodal web-agent trajectories and latency. | The repository includes a LongMemEval adapter that isolates sessions/projects and reports retrieval recall, evidence coverage, abstention, latency, and cost. It does not claim parity with the upstream end-to-end judge or V2 trajectory protocol yet. The CLI now gives an explicit authorized-download path when no local dataset is supplied. |
| [MemoryAgentBench](https://github.com/HUST-AI-HYZ/MemoryAgentBench) | Accurate retrieval, test-time learning, long-range understanding, and conflict resolution in incremental multi-turn interactions. | The internal simulator and benchmark cover related update/conflict/forgetting paths; the official dataset, model judge, and full four-competency protocol still require an external-data run. |
| [BRIGHT](https://brightbenchmark.github.io/) | Reasoning-intensive retrieval across 1,385 realistic queries where surface lexical matching is insufficient. | The benchmark loader accepts external JSON/JSONL cases, but BRIGHT’s document corpus and nDCG@10 evaluation have not been run through this service. This is a retrieval stress test, not a memory-specific end-to-end score. |

External results must record dataset/version/hash, scope policy, retrieval mode, embedding/reranker, judge, latency, cost, and whether the score is retrieval-only or end-to-end. This prevents synthetic internal scores from being presented as public benchmark parity.

## Data-entry and import matrix

| Entry path | Durable first step | User-visible receipt | Scope/idempotency boundary | Recovery and review path |
|---|---|---|---|---|
| Web chat | Session/user and optional project conversation events | Working state, capture choice, evidence, citation state, trace link | Session/project/private-global retrieval scope; capture can be disabled per turn | Retry, stale-thread replacement, Memory Review, Audit Console |
| Codex/App Server | Chat turn through the local App Server; conversation capture follows the selected scope | Assistant result, memory evidence, trace, auth/recovery state | Local subscription credentials stay in the App Server; stale thread retries once | Connect ChatGPT, retry, Status, trace inspection |
| MCP tools | Tool-specific REST-backed event/memory/document operation | Tool result includes durable ID and trace ID | Caller-provided project/session/user scope; event IDs and idempotency keys are preserved | MCP caller retries safely; Audit Console and source lookup |
| REST events and batches | Immutable event or atomic batch transaction | HTTP receipt with event IDs and trace ID, including validation failures | Explicit IDs/idempotency keys; conflicting replays fail without partial writes | Retry identical payload, inspect trace, review source event |
| Conversations, summaries, reflections | Ordered message/event projection, then queued consolidation/reflection job | Batch trace and job lifecycle | Stable session/project provenance; worker leases and bounded retries | Jobs preview/approve/reject/retry; source provenance |
| Documents, chunks, claims, procedures, entities | Source-backed graph/document nodes and relationships | Entity/document/chunk/claim/procedure ID plus trace ID | Project/session/user scope and provenance links | Memory/graph review, retrieval trace, corrective replacement |
| Benchmarks and simulations | Isolated test project or deterministic in-memory world | Benchmark report, simulator report, optional trace envelope | Test source/project excluded from normal global recall | Re-run by seed/corpus hash; compare baseline/ablation |
| Snapshot import/restore | Offline JSON validation before any graph write | Dry-run node/relationship counts and validation errors | Admin credential plus explicit `RESTORE_CONFIRM=RESTORE_PERSONAL_MEMORY`; no implicit web restore | Review snapshot, use dry-run, then perform guarded CLI restore; native Neo4j backup remains the disaster-recovery path |

The snapshot restore boundary is intentionally not an unguarded browser action: it can write a broad graph state and is difficult to undo. The web interface should expose status, export, validation results, and recovery guidance, while the actual restore remains an explicit, reviewable administrative operation.

## Current implementation and evidence map

| Requirement or path | Current behavior | Evidence |
|---|---|---|
| Web interface and primary flow | `/chat`, `/ingest`, `/sessions`, `/imports`, `/memories`, `/jobs`, `/status`, `/metrics`, and the landing route expose the primary user journey: choose scope, capture or ask, observe working state, inspect evidence, correct/forget, recover, and follow trace links. `/ingest` accepts source events, documents, and ordered conversations, `/sessions` accepts reviewed summaries and reflections with lessons/failures, and `/imports` reads supported local text-like files before an explicit document confirmation. These flows use scoped previews, trace receipts, safe failure rendering, retry feedback, and a client-side file-size guard derived from the server request limit; stable identifiers prevent duplicate source or derived memories on retries. ChatGPT device-code login shows a waiting state, polls for completion, times out with recovery guidance, and links its auth receipt to the audit console. Audit replay uses inline confirmation/progress/results rather than native dialogs. Live regions, labels, keyboard-accessible audit rows, responsive layout, and reduced-motion behavior are included. The shell now renders the same nine-item primary navigation on every route, with one active item and safe wrapping on narrow screens. Trace receipts stay same-tab so embedded browsers can follow them into the selected audit waterfall and return with normal back navigation. Recovery command tokens wrap safely on phone widths. | Live browser matrix: 36 route/viewport checks at 320×900, 390×900, 768×900, and 1280×900 with zero horizontal overflow, exactly nine primary-nav links, and exactly one active item; mobile keyboard focus traversal; `bun run ux:smoke` (capture/session/import routes, preview/confirmation, size guard, retry, and receipts), build pass |
| Data entering the system | REST events, batches, conversations, entities, documents, chunks, claims, procedures, summaries, reflections, and local file imports return trace receipts on success and validation failure. Browser capture/import now previews before saving and keeps stable event/document/message/session-memory identifiers across retries so a network failure does not silently duplicate a source or derived memory. | Ingress/session/import smoke scenarios, idempotency coverage, and redacted `ingest.*` audit runs |
| MCP and App Server | MCP exposes 15 tools; the smoke path now exercises every tool across event/batch/conversation/reflection, retrieval/context, graph/document/procedure, and memory lifecycle flows, requiring trace receipts from each. Codex/App Server chat retries once with a fresh thread when a stale thread is detected. | Elevated `bun run mcp:smoke` with 15/15 tool paths; live stale-thread recovery check |
| Retrieval and answers | Hybrid retrieval is the default; recall/context traces redact raw queries; chat shows evidence or a clear no-private-memory state; citation validation returns an auditable receipt. Chat retrieval is now nested under the originating `chat.turn` trace, so the user can inspect one coherent waterfall from chat through retrieval, model/tool calls, and validation. | `bun run evaluate`; retrieval and answer-validation smoke scenarios; live chat trace waterfall |
| Privacy and security | Audit inputs retain bounded counts/keys rather than raw queries, feedback, reasons, or provider content. Auth and admin failures return security trace IDs. Production web access now exposes a public shell with explicit session-scoped API-key entry, bearer injection for API calls, authenticated local source/export navigation, clear-on-demand behavior, and visible 401/403 feedback; keys are not placed in URLs or persisted beyond the browser session. The production route check confirms all nine web pages return 200; protected admin APIs return 401 without credentials, 403 with a normal API key, and 200 with the separate admin key. | Sentinel redaction checks; auth/error-path tests; web-shell API access smoke coverage; production-mode runtime route/auth check |
| Review and correction | Memory review shows scope, lifecycle status, validity bounds, confidence, source event IDs, and supersession links, plus corrective/archive/retraction controls. Correction, archival, and forgetting use in-context accessible confirmation flows; forgetting shows impact before retraction, preserves focus on cancel, and surfaces the resulting trace receipt. Jobs expose lifecycle, failures, retry, collapsed source-event previews, candidate counts/details, optional approval gates, and in-context action receipts/errors; extraction approval now also requires an explicit in-context confirmation. Local provenance and trace links stay same-tab so the user keeps a coherent review path in embedded browsers. | Live browser checks of memory and jobs surfaces; supersession/archive/retraction UX smoke scenarios |
| Persistence and recovery | Audit storage survives API restart; status exposes redacted dependency health plus guarded snapshot export/dry-run restore guidance; metrics exposes bounded operational aggregates; chat and action errors provide retry or in-context recovery. | Restart persistence check, `/status` and `/metrics` checks, UX smoke |
| Verification baseline | TypeScript build passes; unit suite passes 34 tests with 12 optional Neo4j skips; Neo4j integration passes 11/11 when enabled; simulator and 15-case benchmark pass. | `bun run build`, `bun test`, Neo4j integration, `bun run evaluate` |

Remaining publish-readiness work is deliberately explicit: execute the moderated study in [USABILITY-TEST-PLAN.md](./USABILITY-TEST-PLAN.md), run upstream LongMemEval/BRIGHT/MemoryAgentBench data where licensing and adapters permit, and complete production authentication/deployment review. The responsive matrix now covers phone, mobile, tablet, and desktop widths; broader visual QA still needs target browser-family coverage and human review. These are not yet complete.
