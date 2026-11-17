# Plan: one journal, one index

Status: proposed, 2026-11-16. Nothing here is built yet.

This plan takes two ideas from outside vinci and changes how vinci keeps memory. The ideas come from [atlas](https://github.com/bloodfeast/atlas) (keep big text out of the context, behind a short handle) and from LangChain's [deep agents RAG guide](https://docs.langchain.com/oss/python/deepagents/rag) (retrieve, save the results aside, read only what is needed). Neither project's code is used. Section 2 is the first draft. Section 3 is a second round of questioning, with measurements and research. Section 4 is the plan that came out of it.

## 1. The goal

A small local model with a 32k context answers from your journal and your sources. Every claim cites evidence that exists, and it says "I don't know" when the evidence is not there.

Measured by:

- recall hit rate and abstention accuracy on the evaluation sets (section 4, phase 0), not lower than today;
- tokens put into the model's context per answer, lower than today;
- one store of record, and nothing to run besides the two llama-server units.

## 2. First draft

1. The journal is the only store of record. Chat exchanges, `remember` and `read` become journal facts.
2. Memory becomes a SQLite index (FTS5 and vectors) that is rebuilt from the journal. It replaces Neo4j, the REST API, the worker and the job queue.
3. Recall returns pointers within a budget. A new `open` tool reads a slice by id. Large tool results are kept under a handle instead of being cut at 4,000 characters (`src/Chat/Tools.cs`).
4. A summarize step runs one model call per chunk, but only when the evidence does not fit the budget.
5. The citation check and abstention stay as they are.
6. Before deleting the graph, measure what it adds.

## 3. Second round: question it again, with evidence

### What was measured (2026-11-16, live memory service)

Each recall route was run alone and together on two sets: the bundled benchmark (`vinci memory ablation`, 15 questions, the `benchmark-suite` project) and the personal set in `~/.vinci/eval/journal-questions.json` (26 questions about the real journal, 6 of them should abstain).

| Routes | Bundled: hit, abstention | Personal: hit, abstention |
| --- | --- | --- |
| lexical, vector, graph (today's default) | 15/15, 15/15 | 19/20, 24/26 |
| lexical alone | 15/15, 15/15 | **20/20**, 23/26 |
| lexical and vector | not run | **20/20, 24/26** |
| vector alone | 7/15, 9/15 | 11/20, 16/26 |
| graph alone | 9/15, 12/15 | 15/20, 22/26 |

What this says:

- **The graph adds nothing measurable, and costs one hit.** Lexical with vector matches or beats the default on every number. The miss the default makes (j-09, "which model turns text into numbers") is one that lexical alone finds.
- **Vector is weak alone but earns a vote.** It adds one correct abstention (23 to 24 of 26).
- **The bundled benchmark cannot gate anything.** Lexical alone already scores 100%. A change that breaks multi-hop or temporal recall would not show there.
- Both sets are small (41 questions). They show direction, not proof. Phase 0 makes the gate stronger before anything is deleted.

Two things turned up along the way:

- `AUDIT_FILE` in `~/.config/vinci/memory.env` points into a Claude Code scratchpad under `/tmp`. A reboot erases the audit log.
- `vinci memory seed` writes its test world into the live database, as its own project. The evaluation data and real memory share one store.

### What the research says, and what it changes

| Question | Evidence | Change to the plan |
| --- | --- | --- |
| Does memory need a graph? | Zep's lead on LongMemEval (71.2% against 60.2% full context, GPT-4o) is largest on temporal questions, and comes from validity windows on every fact. Vendor numbers between graph systems conflict and are not matched. | Keep **time**, not the graph. The journal already dates every fact, and a correction is a later fact. The index derives `valid_from` and `valid_until` from that. |
| What should be indexed? | LongMemEval: adding extracted facts to the **key** (the text being searched) raised recall by 9.4% and accuracy by 5.4% on average. Replacing the **value** (what the model reads) with summaries or facts lowered accuracy, except on multi-session questions. | Consolidation writes extra search keys for a journal line. It never replaces the line. Recall always returns the original words with their id. |
| Should the model rewrite queries for time? | LongMemEval: time-aware query expansion helped with GPT-4o (up to +17.2 recall points) but **hurt** with Llama 3.1 8B (0.639 to 0.624), which was too weak to extract time ranges. | Find dates in the query **in code** ("last June", "in October", ISO dates), not with the model. |
| How much retrieved text can a small model use? | LongMemEval: Llama 3.1 8B "drops sharply" past about 3k retrieved tokens, while GPT-4o keeps improving past 20k. Anthropic's context engineering note: context rot is gradual and affects every model. | Default recall budget of about 2k tokens, given as pointers past the first one or two records. Find the right number for Qwen 27B with a sweep (phase 3) instead of guessing. |
| Should the reader take notes first (chain-of-note)? | LongMemEval: chain-of-note helped GPT-4o (0.606 to 0.640) and hurt Llama 70B, Llama 8B and Phi-3 14B. | Do not add it. Plain reading with ids stays. A sweep can test it later. |
| Is parallel delegation, as deep agents does it, worth it? | llama-server keeps a prompt cache per slot. A request with another prefix on the same slot loses the cache, and requests on different slots may not share it. One 12 GB GPU runs one model. | A summary call runs on its **own slot** (`--parallel 2`), so the chat's cached prefix survives. It runs only when evidence is over budget, and one at a time. Measure the prefill cost before keeping it. |
| Just-in-time or loaded up front? | Anthropic recommends a hybrid: a little loaded up front, the rest pulled through light identifiers when needed. That is what atlas's handles and the deep agents offload both do. | Recall puts in one or two records whole and the rest as one-line pointers. `open` pulls the rest. The `[type:id]` citation id and the handle id are the same id. |
| What about prompt injection from `read`? | Both the deep agents guide and CaMeL say prompts alone do not stop it. CaMeL's approach: the model that has tools does not see untrusted text, and an interpreter tracks where values came from. It is not complete either. | A cheap version of the same rule: in a turn where untrusted text (a read page or file) entered the context, a tool call that **writes** (journal, remember) is shown and needs a yes. The summarize step has no tools. |
| Which vector store? | sqlite-vec is pre-v1 ("expect breaking changes") and lists no .NET binding. Microsoft.Data.Sqlite's default bundle includes FTS5. | FTS5 for words. A plain BLOB column and a brute-force scan with `TensorPrimitives.CosineSimilarity` for vectors. At thousands of facts that takes milliseconds. One new package (`Microsoft.Data.Sqlite`), one removed (`Neo4j.Driver`). |
| Is 2,560 dimensions needed? | Qwen3-Embedding supports Matryoshka truncation to fewer dimensions. Sources say to measure the loss on your own data. | Keep 2,560 for now (10 KB a vector). Test 1,024 only if the index grows large. |

### What the second round removed from the first draft

- "Keep the graph's who and which questions with SQL joins": not needed. Nothing measured depends on the graph.
- "Delegate to sub-agents": cut down to a single summarize step that runs only when evidence is over budget.
- "Run the benchmark before and after": not enough. The bundled benchmark is saturated, so a harder gate comes first.

## 4. The plan

Each phase ends with a check. If the check fails, the phase stops there, and the old path keeps working until the new one passes.

### Phase 0: a gate that can fail (no code change)

1. Grow the personal set from 26 to about 60 questions taken from the real journal, with at least 10 temporal ("what did I think about X before I changed it"), 10 multi-hop and 10 abstention questions.
2. Download LongMemEval and pick a fixed subset of 100 questions, weighted toward temporal and multi-session. Save the question ids so later runs are the same.
3. Record a baseline for each route mix on both sets, and the tokens recall returns per question.
4. Move `AUDIT_FILE` out of `/tmp`.

Done when the baseline is written in `docs/memory.md` with the dataset hashes.

### Phase 1: the journal is the only store of record

1. New journal kinds: `remember`, `read` (title, source, date read, a hash, and the body saved as `~/.vinci/docs/<sha>.md`), and `exchange`.
2. A one-time import: `vinci memory backup`, then every memory, document and chunk becomes a journal line with its original date and source event ids.
3. `JournalSync` and its cursor are no longer needed once the index reads the journal directly.

Done when the import's counts match the snapshot and a second import adds nothing.

### Phase 2: the index, beside the old store

1. `src/Index/Index.cs`: SQLite at `~/.vinci/index.sqlite`, deleted and rebuilt at any time. Tables: `facts` (journal id, kind, text, valid_from, valid_until, idea, tags, source), `keys` (extra search text per fact from consolidation), an FTS5 table over text and keys, and `vectors` (fact id, BLOB). Built incrementally from the journal's byte offset.
2. Consolidation writes keys, cached by the hash of the journal line, so a rebuild does not call the model again for lines it has seen.
3. `src/Index/Recall.cs`: today's `Retrieval` and `RankFusion` with lexical and vector routes, abstention, the corrective query, and date ranges found in code.
4. `VINCI_RECALL=index` or `neo4j` picks the backend, so both can be run on the same questions.
5. Evaluation runs against the library, not over HTTP. Its data lives in its own index file.

Done when the index scores at least the phase 0 baseline on both sets, and a full rebuild time is recorded.

### Phase 3: handles and the chat loop

1. `src/Index/Handles.cs`: a tool result or document over the budget is saved under an id with a short digest. `open id lines=A-B` or `open id grep=word` reads part of it. This replaces the 4,000-character cut.
2. Recall returns the best one or two records whole and the rest as one line each, within the budget.
3. A budget sweep (1k, 2k, 3k and 5k tokens) on LongMemEval answers, read by Qwen 27B. Keep the best.
4. The summarize step on a second llama-server slot, used only when the needed evidence is over budget.
5. The write confirmation after untrusted text, described in section 3.

Done when answer accuracy, not only recall, is at least the baseline, and tokens per answer are lower.

### Phase 4: delete

Remove `Neo4jStore.cs`, `Service.cs`, `Worker.cs`, `Embedded.cs`, the job queue, `Pages/`, `JournalSync.cs`, `deploy/memory/`, the two memory systemd units and the `Neo4j.Driver` package. Keep `vinci memory mcp` only if Claude Code should read the index too. If so, point it at the library, read-only.

Done when `dotnet test tests` passes, `vinci` runs with Neo4j stopped, and the docs describe what is left.

## 5. What would change this plan

- **The phase 0 set shows the graph winning** on multi-hop or temporal questions. Then keep a small relations table in SQLite for those queries before deleting anything.
- **Rebuild time grows too long.** Then snapshot the index and replay only from the last offset, which is how atlas handles it.
- **The budget sweep shows Qwen 27B reading well past 5k tokens.** Then recall can return more whole records, and the summarize step may not be needed at all.
- **Writes needing a yes become annoying.** Then limit it to writes whose text came from the untrusted content.

## 6. Sources

- atlas: [README and wiki](https://github.com/bloodfeast/atlas)
- LangChain: [RAG with deep agents](https://docs.langchain.com/oss/python/deepagents/rag)
- Wu et al., [LongMemEval](https://arxiv.org/abs/2410.10813) (ICLR 2025): numbers from the [HTML version](https://arxiv.org/html/2410.10813v2)
- Anthropic, [Effective context engineering for AI agents](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)
- Zep vs full context and Mem0: [Zep paper summary](https://signals.aktagon.com/articles/2026/07/zep-a-temporal-knowledge-graph-architecture-for-agent-memory/), [Atlan on unmatched vendor numbers](https://atlan.com/know/mem0-alternatives/)
- Debenedetti et al., [CaMeL: Defeating Prompt Injections by Design](https://arxiv.org/abs/2503.18813); [Simon Willison's summary](https://simonwillison.net/2025/Apr/11/camel/)
- [sqlite-vec](https://github.com/asg017/sqlite-vec); Microsoft, [SQLite custom versions](https://learn.microsoft.com/en-us/dotnet/standard/data/sqlite/custom-versions) (FTS5 in the default bundle)
- llama-server prompt cache per slot: [llama.cpp discussion #13606](https://github.com/ggml-org/llama.cpp/discussions/13606)
- Qwen3-Embedding dimensions: [Superlinked guide](https://superlinked.com/blog/qwen3-embedding-reranker-guide)
