# Memory for agents

`vinci memory` is a durable memory for AI agents, kept apart from the journal because it needs a database and runs as long-lived processes. Agents save what happened as **events**, and a worker turns events into **memories**, **entities**, **claims** and **procedures** that all point back at the events they came from. Nothing is deleted: a memory is superseded, archived or retracted, and recall hides it. It replaced the separate `personal-memory` project, rewritten in C#.

```sh
docker compose -f deploy/memory/docker-compose.yml up -d    # Neo4j
export NEO4J_URI=neo4j://127.0.0.1:7687 NEO4J_USERNAME=neo4j NEO4J_PASSWORD=change-me

vinci memory serve        # REST API and status pages on http://127.0.0.1:4781
vinci memory worker       # turns queued events into memories
vinci memory mcp          # the tools for an agent, over standard input and output
vinci memory backup       # a snapshot of the whole graph
vinci memory restore <snapshot.json>
```

`vinci memory help` lists the rest, and `deploy/memory/memory.env.example` lists every setting.

**Recall** looks a question up three ways at once (words, meaning through an embedding service if you run one, and the graph around the entities it names), lets each vote by rank, and orders the result for the kind of question it is: *when* questions favour recent and historical memories, *how* questions favour procedures, *who* and *which* questions favour the graph. It answers `abstain` when the evidence does not contain the question's distinctive words, instead of guessing. `build_context` packs the evidence into a text block with a `[type:id]` marker on every line, and `/v1/answers/validate` checks that an answer cites only what was retrieved.

**Connecting an agent.** The MCP server offers 15 tools (`remember_event`, `recall_memory`, `build_context`, `create_memory`, `retract_memory`, and so on). For Claude Code:

```sh
claude mcp add vinci-memory -- vinci memory mcp
```

**Keeping it running:**

```sh
cp deploy/memory.service deploy/memory-worker.service ~/.config/systemd/user/
mkdir -p ~/.config/vinci && cp deploy/memory/memory.env.example ~/.config/vinci/memory.env
systemctl --user enable --now memory memory-worker
```

**Measuring it.** `vinci memory seed` writes a small fixed world and `vinci memory benchmark` asks it 15 questions (`BENCHMARK_STRICT=1` fails on any miss, so it can gate a change). `ablation` repeats that with each way of retrieving on its own, `simulate` plays an invented history that includes a deleted secret and an instruction hidden in an untrusted document, and `longmemeval` runs a LongMemEval file you have downloaded. All of them talk to the running API.

**What changed in the port.** Cypher and routes are as they were. A clash with an earlier event of the same id now answers 409 instead of 500. The status pages are the originals, embedded in the program. Chat is the terminal `vinci` command, which talks to the llama-server model; the memory service no longer runs a web chat.

## Baseline for mark 2 (M1, 2026-11-16)

The gate for mark 2's memory work. Both sets were run on 2026-11-16, with the memory service and llama-server up. The numbers come from `~/.vinci/eval/results/personal-baseline-2026-11-16.json` and `lme-baseline-2026-11-16.json`.

### What the gate is

Two sets. Recall is scored, not answers: a question passes when the right evidence comes back, not when a model writes a good answer.

- **Personal set.** `~/.vinci/eval/journal-questions-v2.json`: 60 questions written from the journal alone. No service was queried while writing them. The mix is 26 single, 12 temporal, 10 multihop and 12 abstain. It keeps the old 26 questions unchanged, which the baseline calls `v1-26`. sha256 `51e7735024eabc38d32f2994f1fe0c3b4dbe5d3d92ad9a6b5e55592fd1bb2f95` (old 26-question set `fe970c1b8e16095e726fbeb75bf09dc517b6976cf80d1276e2cc4d3774edb702`). It holds personal data, so it stays outside the repo. A hit means every expected string appears in the top 10 results. Abstention is right when the abstain flag matches.
- **LongMemEval subset.** 100 questions from `longmemeval_s_cleaned.json` (Hugging Face `xiaowu0162/longmemeval-cleaned`, 277,383,467 bytes, sha256 `d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442`), drawn with seed 20261117. The mix is 30 temporal-reasoning, 30 multi-session, 15 knowledge-update, 5 each of the three single-session types, and 10 abstention. The ids are in `mojo/eval/longmemeval-subset.json` (sha256 `75dce89e49ae16c8d26ef26fc2fbc6b0f76be36c50108a7a441500fe090449a7`). `cd mojo/py && uv run python -m vinci_eval.lme_subset --check` regenerates the subset and compares it.

Tokens are counted with the chat model's tokenizer (llama-server `/tokenize`).

### Today's system on the personal set

Neo4j memory API, recall limit 10. Hits are counted over the answerable questions (48 in v2, 20 in v1-26). Abstention is counted over all questions. Tokens are the tokens in the returned text per question, as mean and median.

| Route mix | v2 hit | v2 abstention | v2 tokens mean / median | v1-26 hit | v1-26 abstention | v1-26 tokens mean / median |
| --- | --- | --- | --- | --- | --- | --- |
| lexical | 34/48 | 44/60 | 240.6 / 245.0 | 20/20 | 23/26 | 202.2 / 224.0 |
| vector | 20/48 | 37/60 | 245.5 / 297.5 | 11/20 | 16/26 | 203.0 / 244.0 |
| graph | 25/48 | 44/60 | 128.8 / 125.0 | 15/20 | 22/26 | 114.3 / 120.5 |
| lexical + vector | 36/48 | 48/60 | 287.5 / 280.5 | 20/20 | 24/26 | 271.9 / 259.0 |
| **lexical + vector + graph (default)** | **35/48** | **48/60** | **244.0 / 246.5** | 19/20 | 24/26 | 231.9 / 233.5 |

The old 26 numbers match the memory plan's section 3 table on every row.

The default mix on v2, per type:

| Type | Questions | Hit | Abstention |
| --- | --- | --- | --- |
| single | 26 | 25/26 | 26/26 |
| temporal | 12 | 8/12 | 8/12 |
| multihop | 10 | 2/10 | 8/10 |
| abstain | 12 | not scored | 6/12 |

For the answerable types, the abstention column counts questions the system answered. For the abstain type, it counts questions the system abstained on.

### LongMemEval, reference retriever

Today's system was not run on LongMemEval. That would write about 50,000 turns into the live Neo4j store. Instead, the reference retriever (`mojo/py/vinci_eval/lme_baseline.py`) runs SQLite FTS5 BM25 over each question's own haystack, one row per turn. Sessions rank by their best turn. The vector baseline comes in M3, when the new index pays the embedding cost once.

Recall columns use the 90 answerable questions. The 10 abstention questions appear only in the last count column. Tokens cover the 10 returned turns for every question in the row, abstention questions included.

| Type | Answerable | Abstention | any@5 | all@5 | any@10 | all@10 | turn@10 | Tokens median | Tokens mean |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| temporal-reasoning | 30 | 1 | 96.7% | 83.3% | 96.7% | 93.3% | 93.3% | 2349 | 2429.2 |
| multi-session | 30 | 5 | 96.7% | 60.0% | 96.7% | 73.3% | 90.0% | 2642 | 2746.9 |
| knowledge-update | 15 | 2 | 100% | 100% | 100% | 100% | 100% | 2519 | 2518.1 |
| single-session-user | 5 | 2 | 100% | 100% | 100% | 100% | 60.0% | 2011 | 2128.0 |
| single-session-assistant | 5 | 0 | 100% | 100% | 100% | 100% | 80.0% | 2151 | 2153.2 |
| single-session-preference | 5 | 0 | 80.0% | 80.0% | 100% | 100% | 40.0% | 1829 | 1947.0 |
| **overall** | **90** | **10** | **96.7%** | **80.0%** | **97.8%** | **88.9%** | **87.8%** | **2487.5** | **2496.5** |

### What M3 must beat

- **Personal set.** At least the default mix's v2 numbers: 35/48 hits, 48/60 abstentions, and 244.0 mean tokens (246.5 median). The plan's success criteria apply: no lower hit or abstention, and fewer tokens per answer.
- **LongMemEval.** At least the reference retriever above. The room is in `all@10` for multi-session, at 73.3%. `any@10` is near the ceiling (96.7% for multi-session, 97.8% overall), so it cannot show a gain.

What the baseline shows:

- **The graph still costs a hit.** Lexical with vector gets 36/48. The default gets 35/48.
- **Multihop is nearly unrecalled by every route.** The best is 2/10.
- **Abstention on the abstain type is 6/12** with the default mix.
- **Vector alone is weak.** It gets 20/48 hits on v2, and 1/10 on multihop.

### How to rerun

- **LongMemEval reference baseline.** `cd mojo/py && uv run python -m vinci_eval.lme_baseline`. It takes minutes. It needs no service except llama-server for token counts. Without llama-server, the token counts fall back to length divided by 4, and the output records that.
- **Personal baseline.** Start the memory service with `systemctl --user start memory.service`. The journal sync runs when `vinci chat` starts, so run `timeout 180 vinci chat < /dev/null` first to sync the journal. Then `cd mojo/py && uv run python -m vinci_eval.personal_baseline`. The script takes no options. Stop the service afterwards with `systemctl --user stop memory.service`.
- **Audit log.** `AUDIT_FILE` now points at `~/.vinci/memory-audit.jsonl`, out of `/tmp`, so the audit trail survives a reboot.

## Journal kinds for memory (mark 2)

Phase 1 of [the one-journal plan](plan-one-journal-one-index.md) (M3 step 1). The journal becomes the store of record for what memory holds. This section is the mapping. It uses only the existing Event fields (`at`, `kind`, `id`, `text`, `refs`, `value`, `hi`, `who`, `why`, `url`, `metric`, `tags`, `where`), so the C# binary still reads the journal: it skips these kinds, and the fold is kept for recall in `state.memory_records`.

### The four kinds

| Kind | Id | From (Neo4j) | `text` | `at` | `refs` | `value` | `who` | `why` | `url` | `where` | `tags` |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `remember` | `k` | a Memory with no supporting Event (saved by `remember`) | content | validFrom | the Memories it supersedes (`k` or `m` ids) | confidence | | | | `neo4j:<Memory.id>` | category, and status when not active |
| `memory` | `m` | a Memory that a consolidation derived from Events | content | the earliest `occurredAt` of its Events, else validFrom | journal ids of the copies it came from (never idea or requirement ids, see below), the `e` ids of chat exchanges it came from, the Memories it supersedes | confidence | | | | `neo4j:<Memory.id>` | category, status when not active, and `from:<id>` for each idea or requirement it came from |
| `read` | `d` | a Document (one `read` of a page or file) | title | the read date, at 00:00 UTC | | | | | the locator (the source without its ` (read <date>)` part) | `docs/<sha256 of body>.md` | |
| `exchange` | `e` | a Message (one chat turn) | content | occurredAt | | | role (`user` or `assistant`) | sessionId (the conversation) | | `neo4j:<Message.id>` | the message kind (`user_message`, `agent_message`, `error`, `decision`) |

- **`read` body.** The body is the document's passages in chunk order, separated by a blank line, saved under `$VINCI_HOME/docs/<sha256>.md`. The hash is of those bytes, so the file name is its digest. The page itself was not kept, so the body is the passages as the chunker cut them, not the original page.
- **Ids and clashes.** `k`, `m`, `d` and `e` are new. `state.mojo`'s `next_id` covers `i r n s x p h b`, and the journal holds only `i` and `b` ids today. Each new prefix counts up from its own highest number, so a second import continues the numbering.
- **Idea and requirement ids are tags.** The fold moves an idea's last touch to the `at` of any event whose `refs` name it (`Touched`, as in `src/State.cs`). Imported lines are appended later in the file, and their `at` is in the past, so an idea id in `refs` would move that idea's last touch backwards. The import writes those ids as `from:<id>` tags instead. Both binaries then give the same `review` output as before the import. This was measured: with idea refs in `refs`, `review` lost its "untouched for 20 days" list.
- **Why `at` is the earliest source date.** A consolidated memory's `validFrom` is when consolidation ran, not when the fact happened. The earliest date of its Events is closer to the original.

### What is not imported, and why

| Records | Count | Why |
| --- | --- | --- |
| Journal copies (`source` `vinci-journal`) | 128 | The journal already holds each fact. Re-importing them would duplicate them. |
| Test and benchmark records: Memories, Messages, Documents from the `test`, `benchmark` and `benchmark-fixture` sources, or in the `benchmark-suite`, `paraphrase-suite` and `benchmark-isolation-*` projects | 59 (48 Memories, 4 Messages, 7 Documents) | The evaluation seeds write there. The plan's problem: "the evaluation data and real memory share one store". |
| Events not from a journal copy and not carrying a chat message (`unknown` decisions, errors and tool results, 3 of them tagged `remember`) | 27 | No kind in the table fits them. Left for a decision, see below. |
| Job | 474 | The job queue. Gone with the service. |
| Entity, Claim, Procedure | 214, 111, 78 | Derived by consolidation. Regenerated by the index in phase 2, not kept. |
| ReasoningTrace | 62 | Model traces that point at Events. Not facts. |
| Project | 49 | Scopes. The journal has no field for one, so a line's project is lost. |
| Conversation | 15 | Its id is kept as the `why` of each exchange, so the grouping survives. |
| Chunk | 8 | 1 is in the `read` body. The other 7 are chunks of test documents. |
| SchemaMetadata | 1 | Migration bookkeeping. |

### What the journal could not hold

- **Retraction and supersession are tags and refs, not state.** `status` is kept as a tag (`archived`, `retracted`, `superseded`). The journal has no way to say that a line is void. Recall must read the tag, and must not show a retracted memory as active.
- **Subject, predicate and object** have no field. 19 Memories carry a predicate (15 of them `VALUE`). The `text` keeps the sentence.
- **`validUntil`, `reinforcementCount`, `projectId` and `sessionId` on Memories**, and `projectId` on Messages, have no field. The project scope of a line is lost.
- **Embeddings** are regenerated by the index, so they are not kept.
- **The 27 unmapped Events** are the open question. They are chat decisions and errors that have no chat message to attach to. Either map them to `exchange` with a `decision` or `error` tag, or leave them.

### The import

`vinci memory import <snapshot.json>` in the Mojo binary (`mojo/src/memory_import.mojo`). The snapshot comes from `vinci memory backup`, which only reads Neo4j. Writing goes through the journal serializer. Each record is keyed by its `where`. A record whose `where` is already in the journal is skipped, so a second run appends nothing. Refs are written only when the id they name is in the journal now, which drops a copy's reference to an id from an older journal (one case in the snapshot).

Counts for the snapshot `personal-memory-2026-11-17T04-27-18-882Z.json` (1,512 nodes, 1,175 relationships), on a copy of the journal in staging:

| Kind | Imported | Second run |
| --- | --- | --- |
| remember | 28 | 0 |
| memory | 160 | 0 |
| read | 1 | 0 |
| exchange | 25 | 0 |

The journal went from 128 to 342 lines. The 81 refs in the new lines all resolve. The output of `ideas`, `pipeline`, `review`, `notes`, `stats` and `show i1` is the same before and after the import, and the C# oracle and the Mojo binary print the same bytes for each of them.

### Next

Phase 2 reads these lines into the index. The index reads `remember`, `memory`, `read` and `exchange` from `state.memory_records`, and nothing else in the fold needs to change.
