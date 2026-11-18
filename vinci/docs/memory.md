# Memory for agents

Mark 2 removed the memory service. `vinci memory` now stops with a message that it was removed. What vinci remembers is now three things:

- **The journal**, `$VINCI_HOME/journal.jsonl` (default `~/.vinci`), is the store of record. Every fact has a time and a kind. Chat exchanges, facts it was asked to remember, and documents it read are journal lines too (see [the journal kinds](#journal-kinds-for-memory-mark-2)).
- **The index**, `index.sqlite` beside it, is derived from the journal. `vinci index update` brings it up to date, and `recall` searches it. Words are always searched. Meaning is searched too when the embedding server is up (`EMBEDDING_BASE_URL`, default `http://127.0.0.1:8081/v1`), and the answer says so when it is not. The index is rebuilt from the journal at any time, so deleting it loses nothing.
- **Chat** (`vinci`) answers from recall, with citations, and writes facts only when the person agrees (see [Chat](#chat-mark-2-m4a)). `open <id>` reads one fact or passage by its id.

What was removed in M4e, and where it is now: the Neo4j store, the REST API and status pages, the worker, the MCP server, the audit log, the `seed`, `benchmark`, `ablation`, `simulate` and `longmemeval` commands, `vinci memory import`, the units `deploy/memory.service` and `deploy/memory-worker.service`, and the Neo4j compose file and settings example. The C# code is in git history before that commit, under `vinci/src/Memory/`. Mark 2 measured the new index against the old service: the baseline and the design are in the sections below.

The sections below are the mark 2 record. Where they name the C# binary, Neo4j or `vinci memory`, they describe what existed when they were written.

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

### How to rerun (the personal baseline needs the removed memory service)

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

`vinci memory import <snapshot.json>` in the Mojo binary (`mojo/src/memory_import.mojo`, run once; the command and the module were removed in M4e). The snapshot comes from `vinci memory backup`, which only reads Neo4j. Writing goes through the journal serializer. Each record is keyed by its `where`. A record whose `where` is already in the journal is skipped, so a second run appends nothing. Refs are written only when the id they name is in the journal now, which drops a copy's reference to an id from an older journal (one case in the snapshot).

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

## The index (mark 2, M3 step 2a)

Step 2a of [the one-journal plan](plan-one-journal-one-index.md), phase 2: lexical recall in Mojo. Vectors, fusion, abstention, the corrective query and date ranges are step 2b. The index is derived data: delete it and `vinci index rebuild` makes it again.

```sh
vinci index rebuild                       # delete and rebuild from the journal
vinci index update                        # add the journal lines after the stored offset
vinci index stats                        # facts per kind, file size, last build
vinci index load records.jsonl --index P  # external records (one JSON object per line)
vinci index embed [--label L] [--base-url U] [--cache PATH]   # vectors for facts without one (step 2b)
vinci recall [--json] [--limit N] [--routes lexical,vector] [--index P] [--label L] [--base-url U] [--cache PATH] <query...>
```

The index is `$VINCI_HOME/index.sqlite`, or `--index PATH`. None of these commands writes the journal. Code: `mojo/src/index_store.mojo` (schema, versioning), `index_build.mojo` (the mapping, the incremental build, the loader), `recall.mojo` (tokens, FTS5 query, routes), `cmd_index.mojo` (the commands). Tests: `mojo/tests/test_index_store.mojo`, `test_index_build.mojo`, `test_index_recall.mojo`.

### Schema (version 1)

| Table | Holds |
| --- | --- |
| `meta` | `schema_version`, `journal_offset` (bytes of complete lines read), `journal_lines`, `last_build_seconds` |
| `facts` | one row per version. The rowid is the fact id. `source`, `record_id`, `version_hash`, `kind`, `text`, `cite` (the journal id, e.g. `m12`), `at`, `valid_from`, `valid_until`, `idea`, `tags` and `refs` (JSON arrays), `trusted`, `status` (`active`, `archived`, `retracted`, `superseded`), `line` (journal line, or 0 for loaded records) |
| `keys` | `(fact, key)`: extra search keys. Empty until consolidation (step 3) |
| `fts` | FTS5 over `text` and `keys`, rowid = `facts.rowid`. Every version is indexed, and search filters |
| `vectors` | `(fact, label, dims, vec)`: packed float32, one row per fact and label. Filled by `vinci index embed` (step 2b) |

A version is **current** when `valid_until` is NULL. Recall returns current versions with `status = 'active'`.

### Which lines become facts

One fact per line that carries text a person would recall. The kinds, from `State` in `state.mojo`:

| Kind | Fact? | Content | Idea |
| --- | --- | --- | --- |
| idea, for, goal, req, cut, work, ship, retire, home, issue, diagnose, principle, predict, result, commit, missed, kept, note, source | yes | `text`. A work line adds `commit <where>`. A source adds its `url` | its id (idea kinds), else the first `i` ref, else a `from:i…` tag |
| bookmark | yes | `text`, `why`, `url` on separate lines | as above |
| remember, memory | yes | `text` | as above |
| read | yes: the title line, plus one fact per body chunk | title and `url`. Chunks are the body text | none |
| exchange | yes | `text` | none |
| stage, watch, unwatch, unbookmark | no | configuration, not recall text | |
| link, log, spec, spent, sync, used, review, tend | no | no text a person would recall (metrics, time, sync cursor) | |
| unknown kinds | no | skipped, as the fold skips them | |

`trusted` is true for the user's own lines and the read title. It is false for read body chunks, and for an `exchange` whose `who` is not `user`. `at` and `valid_from` are the line's `at`, in the journal's format.

### Record keys and versions

A record is keyed by `(source, record_id)`. For the journal, `source` is `journal`:

- `record_id = kind:id` for a line with an id. The kind keeps an idea `i19` and a work line `i19` apart.
- A repeat of the same kind and id is a **new fact** with `kind:id#L<line>`, for appended kinds (work, spent, note …). Work lines are events, so a second work line does not correct the first.
- **Versioned kinds** (`for`, `goal`, `cut`, `home`, `diagnose`, `result`, `remember`, `memory`) keep one key: a later line with the same id is a new version of the earlier one, as the fold keeps only the last value.
- A line with no id: `kind:L<line>`. A read chunk: `<title record>/<n>`.
- `cite` is the journal id (`m12`), `L<line>` when there is none, and `d1/2` for a read chunk. Two kinds can share an id, so a result carries its `kind` too.

### Versioning (`IndexStore.put`)

| Case | Result |
| --- | --- |
| Record is new | stored, returns 1 |
| Same hash as the current version | nothing changes, returns 0 |
| New hash | the current version ends (`valid_until` = the new line's `at`), the new one is stored, returns 2 |
| An older hash comes back (A, B, A) | a new version, like any new hash. The older versions are never compared |

The hash of a journal line is the SHA-256 of the stripped line. A read chunk's hash is the SHA-256 of its text. A loaded record's hash is its `hash` field, or the SHA-256 of its text.

### valid_until

1. **Supersession.** A `remember` or `memory` line whose `refs` name `k` ids ends the `remember:k…` records, and `m` ids end `memory:m…`, at its `at`. This is the only correction from the journal for memory.
2. **Status tags.** `archived`, `retracted` and `superseded` on a memory line are stored in `status`, so the version is not active and recall does not show it. The history stays.
3. **Idea retire is not a correction.** `retire` is a fact about the idea, and the idea's text stays true. It ends nothing.
4. **Journal order decides what is current, not dates.** Imported lines carry earlier `at` values, so `valid_until` can be earlier than `valid_from`. Step 2b's as-of queries must allow for that.

### Read bodies

A body is split at markdown headings, which always start a new chunk. Otherwise lines are packed into chunks of about 1,000 bytes, cut at line boundaries. A line longer than that stays whole. Blank-only chunks are dropped. A missing body is counted in the build report, and the title is still indexed.

### The build

`update` reads from `journal_offset`, and only complete lines count: a last line without its newline waits for the next run. One run is one transaction, and the offset is stored in it. A journal shorter than the offset is refused, and so is a bad line: the whole run rolls back. `rebuild` deletes the file and runs `update` from 0. A rebuild equals an incremental build, row for row (tested).

### Recall (lexical)

- **Tokens** as `Retrieval.cs`: lowercase, runs of `[a-z0-9][a-z0-9_-]{2,}`, minus its stop words. `the` is one of them.
- **Query**: each term is a quoted FTS5 phrase, and the terms are joined with OR. Neo4j's full-text index, which the C# route used, joins words with OR by default. With no terms left, the question's own words are quoted.
- **Ranking**: bm25 (`score` is its negation, so higher is better). Only current, active versions. `limit` is applied after the join.
- **Result**: `id`, `kind`, `text`, `at`, `source`, `trusted`, `score`, `route`, `rowid`. The JSON has `results` (with `content`), `abstain` (always false until 2b) and `routes`.
- **Routes**: `lexical` and `vector`, fused by reciprocal rank (step 2b, below). Step 2a's single-route refusal is gone.

### Measured (staging journal, 342 lines, 2026-11-16)

| Run | Result |
| --- | --- |
| Full rebuild | 312 facts from 342 lines (14 `spent`, 14 `sync` and 3 `review` carry no text, so 311, plus 1 read chunk). 6 versions ended. 8 ms in the build, 0.28 s wall |
| Update, no new lines | 0 writes. 0.03 s wall |
| Per kind, current / total | bookmark 4/4, exchange 25/25, home 19/19, idea 19/19, memory 144/160, read 2/2, remember 13/28, retire 2/2, work 53/53 |

Personal set, lexical, v2 (60 questions, `index_eval.py`, tokens by `len/4` because llama-server was down):

| Route mix | hit | abstention | tokens mean / median |
| --- | --- | --- | --- |
| M1 lexical (memory API) | 34/48 | 44/60 | 240.6 / 245.0 (llama tokens) |
| Step 2a lexical (index) | 34/48 | 48/60 | 127.8 / 132.5 (len/4) |

Step 2a abstains on nothing, so its 48/60 is the 48 answerable questions it did not refuse plus the 0 of 12 abstain questions it failed. M1 abstained on 5 of 12 abstain questions. The token columns are not comparable: M1 counted the llama tokenizer, and step 2a counted `len/4`.

Per type, M1 lexical against step 2a: single 24/26 and 24/26, temporal 8/12 and 9/12, multihop 2/10 and 1/10. The misses are the single questions j-01 (`/steven3zx` sits in a home line, outranked by facts that also say "vinci") and j-30 (`ActivityWatch` is one FTS5 token, so `activity` does not match it), and most multihop questions. Vector and fusion are for those.

### Step 2b (done, see the results below)

Abstention, the corrective query, date ranges, the vector route and fusion are built. Vectors are embedded by `vinci index embed` and scanned by `vector_scan.mojo`. Remaining open: `keys` (consolidation, step 3).
- `keys` are filled by consolidation. Until then `keys` is empty and search uses `text` only.

### Index vs baseline (M3 step 2b)

Step 2b of [the one-journal plan](plan-one-journal-one-index.md), phase 2: vectors, fusion, abstention, the corrective query and date boosts. Status: **both sets are measured.** Verdict below: phase 2's done-when is met on both sets. Tokens per answer are higher than M1 on the personal set, and phase 3 is where they get cut.

**Personal set** (v2, 60 questions, staging journal of 342 lines, 281 facts, `vinci recall --limit 10`, tokens from llama-server `/tokenize`):

| Route mix | v2 hit | v2 abstention | v2 tokens mean / median |
| --- | --- | --- | --- |
| M1 lexical (memory API) | 34/48 | 44/60 | 240.6 / 245.0 |
| M1 default, lexical + vector + graph | 35/48 | 48/60 | 244.0 / 246.5 |
| 2b lexical | 35/48 | 45/60 | 200.6 / 195.5 |
| 2b vector | 34/48 | 49/60 | 296.8 / 316.0 |
| 2b lexical + vector (new default) | 36/48 | 49/60 | 275.4 / 263.0 |

Against the phase 2 bar (at least the phase 0 default on both sets): the new default meets it on hits (36 against 35) and abstention (49 against 48). **Tokens are higher, not lower:** lexical + vector returns 275 tokens on average against the default's 244. Only lexical alone is lower (200.6). The plan's "tokens lower" goal is therefore not met by the default. Phase 3's budget work is where that gets addressed, not phase 2's pass/fail bar.

**LongMemEval subset** (100 questions, 90 answerable and 10 abstention, `lme_index_eval.py`). Per-type numbers are in `~/.vinci/eval/results/lme-index-2026-11-17.json`. The reference is `lme-baseline-2026-11-16.json` (SQLite FTS5 BM25 over each question's haystack). Tokens are over the top 10 results, counted with llama-server in both.

| Route mix | any@5 | all@5 | any@10 | all@10 | turn@10 | tokens median / mean |
| --- | --- | --- | --- | --- | --- | --- |
| Reference (BM25 over haystack) | 96.7% | 80.0% | 97.8% | 88.9% | 87.8% | 2487.5 / 2496.5 |
| 2b lexical | 96.7% | 80.0% | 97.8% | 87.8% | 87.8% | 2477.5 / 2539.1 |
| 2b vector | 98.9% | 92.2% | 98.9% | 98.9% | 96.7% | 1588.5 / 1591.3 |
| 2b lexical + vector (new default) | 97.8% | 88.9% | 98.9% | 94.4% | 94.4% | 2108.5 / 2177.6 |

The index abstained on 2 of the 10 abstention questions in every route mix. The subset's abstention questions are not scored in the baseline, so that count is reported only.

**Where the question vectors came from.** Documents (the turns) and the original questions are the GPU vectors (`qwen3-embedding:4b@gpu`) that `lme_embed.py` cached. The GPU job skipped 3 turns that were over the 8,192-token context, so 3 turns have no vector (2e6d26dc, 852ce960 and 2698e78f each have one). Those turns are found by lexical recall only. The corrective queries were not in the cache. All 100 questions had one, so 100 were embedded on the CPU (`qwen3-embedding:4b@cpu`, a distinct label, in the same cache file). The scored runs pointed both their base and fallback URLs at a closed port, and the cache held the same 46,604 rows before and after. So the scored runs made no successful server call. CPU and GPU are not the same device, so the query vectors are not identical to GPU ones. On a 20-turn sample, the CPU and GPU vectors of the same texts have cosine 0.9955 minimum and 0.9967 mean.

**Verdict, Phase 2 done-when** ("at least the phase 0 baseline on both sets, and a full rebuild time is recorded"):
- **Personal set: met.** The new default (lexical + vector) gets 36/48 hits against M1's 35/48, and 49/60 abstentions against 48/60. **Tokens are higher, not lower:** 275 mean against M1's 244 (llama tokens). Phase 2 does not require fewer tokens; Phase 3's budget sweep and handles are what cut them.
- **LongMemEval: met.** Lexical + vector is at or above the reference on every recall metric: any@5 97.8 against 96.7, all@5 88.9 against 80.0, any@10 98.9 against 97.8, all@10 94.4 against 88.9, turn@10 94.4 against 87.8. Its tokens are lower (median 2108 against 2488). Lexical alone does not beat the reference on all@10 (87.8 against 88.9), so the fusion is what gets there.
- **Rebuild time recorded:** below.

**Timings** (staging journal, 342 lines; machine shared with the GPU job):
- Full rebuild, no embedding: 0.3 s wall (120 ms in the build).
- Embedding all 281 facts on the CPU with a cold cache: 51.5 s.
- The GPU job that filled the LongMemEval cache: 80.2 min for 46,504 texts (about 9.8 texts/s), with a short restart midway.
- LongMemEval: 100 per-question indexes built and embedded from the cache in 1.4 s each (load 0.46 s, embed 0.98 s, about 494 turns each); the CPU warm pass and the 20-turn agreement sample ran together in under 2 minutes; each scored route took 5 to 8 s for 100 questions.
- Recall, lexical route on the personal set: 60 calls in 2.0 s. The vector route's first pass took 98 s for 60 calls, because each query vector was embedded once on the CPU; a later pass with the cache warm was not timed.

## Handles, open and budgeted recall (mark 2, M3 step 3a)

Items 1 and 2 of phase 3 of [the one-journal plan](plan-one-journal-one-index.md), with the packing from item 2. Items 3 to 5 are the budget sweep for answers, the summarize step and the write confirmation. The sweep, the summarize step and the confirmation are not built.

```sh
vinci open <id> [lines=A-B] [grep=word] [--index PATH] [--json]
vinci recall --budget N [--whole N] [--tokenizer-url URL] [--json] <query...>
```

Code: `mojo/src/handles.mojo` (ids, the handles table, the lookup), `open_view.mojo` (what `open` prints), `pack.mojo` (the packed context and the counters), `cmd_index.mojo` (the commands). Tests: `mojo/tests/test_open.mojo`, `test_pack.mojo`. The exact counter is `mojo/py/vinci_edge/llama_count.py`.

### Ids

- **A fact's id is its citation id** (`m12`, `i19`, `d1/2`, `L142`): the `cite` column of the index. `open` reads the facts table for it, so a fact's text is never copied.
- **A handle's id is `t<n>`** (`t1`, `t2` ...). No journal id uses `t`: the journal prefixes are `i r n s x p h b`, and the import adds `k m d e`. `save_handle` also skips any number whose id a fact already has as its cite, so a loaded record with the id `t2` is never shadowed.
- **Handles table** (created with `IF NOT EXISTS`, so a schema 1 file opens unchanged): `handles(id, created, source, text, digest)`. `source` names what the text came from (a tool call, a read document, a journal id). `save_handle` commits on its own, so it must not run inside another transaction. Nothing calls it yet: the chat tool that saves over-budget results is M4.
- **A read document** is opened as its title line followed by its body. The body is the document's chunks (`dN/1`, `dN/2` ...) joined by blank lines, so no second copy is stored.
- **One id, several facts.** An id can name more than one fact: `i1` is an idea, a home line and its work lines. `open i1` opens each current fact in turn, with its own header.

### open

- **Header** for each fact: `[<id>] <kind> <date> (<n> lines)`. A fact that is not current is marked `[superseded]`, `[retracted]` or `[archived]`, and only its latest version is shown.
- **Lines** are numbered from 1 and printed as `n: text`. A gap in the numbers prints as `--`.
- **`lines=A-B`** takes lines A to B, inclusive. B is clipped to the last line. `A > B`, `A < 1` and `A` past the end are errors.
- **`grep=word`** takes each line that contains the word, ignoring ASCII case, with one line of context either side. It is a substring match, so `activity` finds `ActivityWatch`. With no match it prints `no line matches 'word'`. A range with grep takes only the lines in the range.
- **Without options** it prints the whole text. Everything taken is capped at 4,000 bytes (the same figure as the C# tool cut in `src/Chat/Tools.cs`, measured in bytes here). A cut between lines ends with a notice such as `showing lines 1-190 of 300; open m1 lines=191-300 for more`. A single line over the cap is cut inside the line, and the notice says so: no option reads past that cut.
- **Errors** (exit 1): an unknown id (`no fact or handle named 'x' in the index`), a bad range, `lines=` and `grep=` together, two ids, and a missing index (`run vinci index rebuild`).
- **`--json`** prints `{"id", "items": [{"id", "kind", "date", "note", "total_lines", "lines": [{"n", "text"}], "truncated", "more", "notice"}]}`. `more` is the `lines=` value to read on with, or `null`.

### Packed recall

`vinci recall --budget N` packs the results instead of listing them. Results are best first, and the packer takes them in that order.

```
[e1] 2026-11-15 exchange: What do I know about the embedding model the project uses?
[e2] 2026-11-15 exchange: I do not know what embedding model the project uses.
[i1] 2026-11-15 work: Embed with Qwen3-Embedding-4B and record which model made each vector commit 18b7ae064fef734eccafbca… (open i1 for more)
```

- **Whole.** The best `--whole` results (default 2) are whole: `[<id>] <date> <kind>: <text>`, with the text as stored, newlines kept.
- **Pointer.** Every other result is one line: `[<id>] <date> <kind>: <digest> (open <id> for more)`. The digest is the first 100 characters with whitespace collapsed, and `…` after them when cut. When the digest is the whole text, the open hint is left off, since there is nothing more to open. This is a departure from the literal format in the plan, which would have added a hint to short records that have nothing past their digest. On the personal set that hint cost more tokens than the full text.
- **Fallback.** A whole candidate that does not fit with the lines before it is shown as a pointer. One that does not fit even alone is counted as oversized, and it is shown as a pointer too.
- **Stop.** The packer stops at the first pointer that does not fit, so the output keeps rank order and the rest are left out. The packed text never goes over the budget.
- **Counting.** The default is the estimate, `ceil(bytes / 4)` of the packed text. `--tokenizer-url http://127.0.0.1:8080/tokenize` counts exactly with llama-server. Its array input gives one flat list with no boundary between the items, so each check asks for the whole packed text. This is one request per candidate, a few per query.
- **Output.** Without `--json` the packed text is printed (with an `abstain:` line first when the answer should abstain). With `--json` the response keeps `results` and adds `budget`, `packed`, `packed_tokens`, `token_method` (`estimate` or `llama-server`), `packed_whole`, `packed_pointers`, `packed_oversized` and `packed_omitted`.

### Measured: recall at budget (personal set, v2, 60 questions)

`cd mojo/py && uv run python -m vinci_eval.index_eval --budget 100,150,250,500,1000,2000,3000,5000`. Routes lexical+vector, staging journal (342 lines), the estimator for tokens. The hit rate is over the 48 answerable questions, with the same test as the route runs: every expected string must be in the text that would be read. Written to `~/.vinci/eval/results/index-budget-2026-11-17.json`.

| Read as | Hit | Hit % | Tokens mean | median | Whole | Pointers | Omitted |
| --- | --- | --- | --- | --- | --- | --- | --- |
| top 10, content only (no ids) | 36/48 | 75.0% | 166.2 | 156 | - | - | - |
| top 10, all whole (packed form) | 36/48 | 75.0% | 227.0 | 217 | 10.00 | 0.00 | 0.00 |
| budget 100 | 31/48 | 64.6% | 88.1 | 90 | 2.00 | 2.07 | 5.93 |
| budget 150 | 32/48 | 66.7% | 136.4 | 138 | 2.00 | 4.22 | 3.78 |
| budget 250 | 36/48 | 75.0% | 212.1 | 218 | 2.00 | 7.33 | 0.67 |
| **budget 1000** | 36/48 | 75.0% | 231.5 | 222 | 2.00 | 8.00 | 0.00 |
| **budget 2000** | 36/48 | 75.0% | 231.5 | 222 | 2.00 | 8.00 | 0.00 |
| **budget 3000** | 36/48 | 75.0% | 231.5 | 222 | 2.00 | 8.00 | 0.00 |
| **budget 5000** | 36/48 | 75.0% | 231.5 | 222 | 2.00 | 8.00 | 0.00 |
| budget 500 | 36/48 | 75.0% | 231.5 | 222 | 2.00 | 8.00 | 0.00 |

What it says:

- **The budgets in the plan do not bind on this set.** The top 10 fits in about 230 estimated tokens, so 1,000 to 5,000 all pack the same ten results: two whole and eight pointers. The plan's sweep needs the LongMemEval set, where a top 10 is about 2,500 tokens. This was not run.
- **Below about 250 tokens recall drops**, to 64.6% at 100. At 250 the packed text holds every string the unbudgeted top 10 holds.
- **Pointers do not lose hits here.** At 1,000 and above, the eight pointers carry the same hits as the full text, because the records are short and the expected strings sit in the first 100 characters. Long records would show the loss.
- **The unbudgeted reference for tokens is the packed form, not the content.** The content-only row (166 tokens) leaves out ids and dates, which the model needs to cite.

Exact counts were checked on four questions at budgets 40, 100 and 180: llama-server's count of the packed text matched `packed_tokens` every time, and never exceeded the budget.

### Known limits

- `open` on an id that names many facts (`i1` has 30) prints every one, each up to the cap. The total is not capped. The chat tool will need a cap across the whole output.
- A pointer for a record over 100 characters can hide an expected string past character 100. `open <id>` reads it. The sweep scores the packed text as it is, so this costs a hit when it happens.
- The handles are not used yet. Nothing writes to the table, and the C# tool still cuts at 4,000 characters.

### Budget sweep (M3 step 3b)

Step 3b of [the one-journal plan](plan-one-journal-one-index.md), phase 3, item 3: does budgeted recall keep **answer** accuracy while spending fewer tokens? Recall numbers (3a) do not answer that. The sweep sends each packed context and the question to the chat model, and judges the reply. This section is the method; the results are in [m3-results.md](m3-results.md).

```sh
cd mojo/py
uv run python -m vinci_eval.budget_sweep --limit-questions 3 --out SCRATCH.jsonl   # smoke test
uv run python -m vinci_eval.budget_sweep --set both                                # full run, resumable
uv run python -m vinci_eval.budget_sweep --summary --out ~/.vinci/eval/results/budget-sweep-<date>.jsonl
```

- **Sets.** `lme`: the fixed 100 LongMemEval questions, configurations `baseline`, `b500w2`, `b1000w2`, `b1000w10`, `b2000w10`, `b3000w10`, `b5000w10` (`b<N>w<W>` is `vinci recall --budget N --whole W`; with `--whole 10` the packer fills whole records until the budget and turns the rest into pointers). Each question's index is `~/.vinci/eval/lme-index/<qid>.sqlite` (built by `lme_index_eval`, embeddings from the cache). `personal`: the 60 v2 questions on a temporary copy of the staging journal, index and docs, configurations `baseline`, `b100w2`, `b150w2`, `b250w2`, `b250w10`, `b500w10`. The staging home is only read.
- **Configurations.** `baseline` is the unbudgeted top 10 with every record whole (`--budget 10000000 --whole 10`). A budget row is `vinci recall --budget N --whole W`. All use `--limit 10`. Context tokens are `ceil(bytes / 4)` for the baseline, and the binary's `packed_tokens` for a budget, which must match that estimator.
- **Embeddings.** No embedding is computed. The base URL is closed, the LME runs use the same GPU label, cache and CPU corrective-query fallback as `lme_index_eval`, and the personal runs use the cache at `~/.vinci/index/embed-cache.sqlite`. A recall that needs an uncached vector exits non-zero, and that question is written as an error row.
- **Answer.** The chat model on `127.0.0.1:8080` (the id comes from `/v1/models`), temperature 0, `max_tokens` 256, thinking off with `chat_template_kwargs`, and any `<think>` block stripped. The prompt is a short system prompt, the packed excerpts, the question date (LME only) and the question.
- **Judge.** The same model, with LongMemEval's `evaluate_qa` prompts by question type, and the abstention prompt for ids ending in `_abs`. The prompts are reconstructed, since the LongMemEval repository is not on this machine. For personal questions the reference is the list of `expectedContains` strings, which the reply must state in any wording. Judging by the answering model is self-judging, so the summary says so.
- **Output.** One JSON line per answer in `~/.vinci/eval/results/budget-sweep-<date>.jsonl`: configuration, question id, context tokens, prompt and completion tokens for the answer and the judge, the answer, the verdict, seconds for recall, answer and judge, and an `error` field. A rerun skips the keys that finished without an error, so an interrupted run resumes. Errors run again.
- **Summary.** `--summary` prints a table per set (accuracy, correct/n, errors, mean and median context tokens, mean seconds, mean answer and judge seconds), and a per-type table for LongMemEval.

## Chat (mark 2, M4a)

`vinci` and `vinci chat` run in Mojo (`mojo/src/chat*.mojo`). The chat model is llama-server's `/v1/chat/completions` (`CHAT_BASE_URL`, default `http://127.0.0.1:8080/v1`; `CHAT_MODEL`, `CHAT_THINK=1`, `CHAT_HISTORY_CHARS`). Neo4j is not contacted: the journal and the index replace the memory service.

### Tools

| Tool | Does |
| --- | --- |
| `journal` | one vinci command, run as a child process so its output is captured, as the C# tool captures it. `chat`, `serve`, `memory`, `index`, `recall` and `open` are not journal commands here |
| `recall` | the index, packed under `budget` (default 3000 tokens) with the newest 10 records whole and the rest as pointers (`--whole 10`, the setting the M3 sweep left as the default without tools). An `abstain:` line comes first when recall abstains. The embedding server is optional: without it the answer is lexical and says so |
| (up front) | before the first model call of each turn, `recall` runs on the person's own message, verbatim and packed the same way. Its result goes into the context as a recall call the model made, so its ids are evidence for the citation check. It prints nothing (see the M4 step d section) |
| `open` | one id, with `lines=A-B` or `grep=word`. The whole output of one call is capped at 4,000 bytes, and a cut ends with a notice that says how many bytes were not shown and what to open next (`open <id> grep=<word>`, or `lines=` for one fact) |
| `remember` | a `remember` line (`k<n>`, category as its tag), then an index update |
| `read` | a text file (`.txt`, `.md`, `.markdown`) or a web page: passages go to `docs/<sha256>.md`, a `read` line (`d<n>`) points at it, then an index update. Web fetches and HTML-to-text are in `mojo/py/vinci_edge/reading_edge.py` |

Each exchange is written as an `exchange` line: the person's message always, the answer only when it is kept (below). Each write is followed by `vinci index update`, so the next recall sees it. A chat start runs the same update, which is what replaced `JournalSync`.

### Citations

Ids are cited as `[id]` (`[i1]`, `[m12]`, `[d1/2]`). The check runs per turn, on this turn's tool results, as the C# check did:

- A cited id that no tool result of the turn contains is reported: `  ! cites [i99], which recall did not return this turn`.
- A cited document passage (`d<n>` or `d<n>/<k>`) gets a `Sources:` list at the end of the answer, with the title and locator from the index. The model never writes that list. A passage with no document in the index is reported and the answer is not kept.
- An answer is kept only when it cites something, nothing it cites is unknown, and every passage has a source. Otherwise the exchange is saved without the answer, and the chat says `  · answer not saved`.

The C# citation pattern needs a `type:id` form, so the C# chat never flags a bare id such as `[i99]`; the Mojo check does. The scripted test therefore compares the unknown-citation script on the Mojo side only.

### Write confirmation

Once untrusted text has entered the conversation (the `read` tool ran, or a recall or open returned a line that is not the person's own journal line, `trusted = 0` in the index), a write is printed and needs a yes on standard input before it runs:

```text
  ? journal {"args":["idea","Glider"]} This is a write, and untrusted text is in this conversation. Allow it? [y/N]
```

`y` or `yes` runs it. Anything else, and end of input, is a no, and the model gets `refused: the person did not allow this write, so nothing was written.` as the tool result. A write is any `journal` command except the read-only set (`help`, `ideas`, `pipeline`, `next`, `show`, `notes`, `tags`, `sources`, `predictions`, `stats`, `issues`, `principles`, `bookmarks`, `watches`, `path`), plus `remember` and `read`. `sync` and `review` count as writes. Before any untrusted text is in the conversation, writes run without asking, as they do in the C# chat.

### Not in this step

- The summarize step. The M3 sweep showed the chat model reads 2.5k tokens of packed recall, so it waits for a measurement that needs it.
- Neo4j, the memory API and the worker: removed from the chat, not ported.
- The `Recorded N journal facts in memory` line, which had no meaning once the index is local.

### Tests

- Unit: `mojo/tests/test_chat_cite.mojo` (citation scanning and the unseen check), `test_chat_gate.mojo` (writes, the gate, yes and no), `test_chat_open.mojo` (the cap, notice, multi-fact cut, grep), `test_chat_args.mojo` (action parsing, accessors, summary).
- Differential: `cd mojo/py && uv run python -m vinci_diff.chat_diff` runs each script in `mojo/difftest/chat/*.json` against the C# oracle (`mojo/build/oracle/vinci chat`) and the Mojo chat. A fake llama-server (`vinci_diff/fake_llm.py`) replays the script's replies and records what the model was shown. The C# side is compared on stdout and on the journal lines it adds, with the Neo4j notices, the answer-not-saved line and the memory kinds (`exchange`, `remember`, `read`) left out, since those are by design. The Mojo side is checked against each script's expectations. `--show <name>` prints one Mojo transcript.

### Chat-loop evaluation (M4 step c)

The M3 sweep answered from packed recall with no tools. This step measures the real chat loop (`vinci chat`, with recall, open and the rest) on the same questions, judged the same way, so the two can be compared. The full results are not in yet; the smoke run (3 questions per set, both configurations) is clean, see below.

```sh
cd mojo/py
uv run python -m vinci_eval.chat_eval --limit-questions 3 --out SCRATCH.jsonl --transcripts DIR   # smoke
uv run python -m vinci_eval.chat_eval --set both                                                  # full run, resumable
uv run python -m vinci_eval.chat_eval --summary --out ~/.vinci/eval/results/chat-eval-<date>.jsonl
```

- **Runs.** Each conversation is one `vinci chat` process: one question on standard input, then end of input. The stdout is parsed into the answer (lines starting `  · `, `  ! `, `  ? ` removed), the citation flags and the tool calls. Tool calls come from the usage log (each model reply that is a tool call, whole), and are cross-checked against the count of `  · ` notice lines.
- **Configurations.** `chat-w10` (recall budget 3000, whole 10: the chat default) and `chat-w2` (budget 1000, whole 2: pointers, which need `open`).
- **Sets and fresh homes.** LME: an empty journal and a copy of the question's index (`~/.vinci/eval/lme-index/<qid>.sqlite`), with `VINCI_CHAT_INDEX` pointing at the copy and `VINCI_NOW` = the question date. Personal: a fresh copy of `~/.vinci/staging` per conversation, no `VINCI_NOW`. The originals are read only.
- **Embeddings.** The model rephrases its recall queries, and those vectors are not cached, so the query path goes further than `budget_sweep`'s question-only lookup. For each question vector, in order: the device label in the GPU cache (`gpu`); the CPU label in the same cache, the corrective-query rows `lme_index_eval` warmed (`cpu-cached`); a separate scratch cache of live CPU vectors (`cpu-live`); and only then a live embed on the CPU server at 127.0.0.1:8081, written to that scratch cache (`cpu-live`). Nothing is written to the GPU cache or the user's embedding cache. The scratch cache defaults to `~/.vinci/eval/cache/chat-eval-live-cpu.sqlite` (`--live-cache`; it must not be the GPU cache). Document vectors still come from the closed-URL caches. A recall whose vector cannot be had even on the CPU falls back to lexical, which is logged as `error`. Only that case makes a row an error. The judge runs on every completed answer, whether or not it was saved.
- **Eval-only hooks** (all unset by default; defaults unchanged):
  - `VINCI_CHAT_RECALL_BUDGET`, `VINCI_CHAT_RECALL_WHOLE` (chat_tools.mojo): the recall packing.
  - `VINCI_CHAT_INDEX` (chat_tools.mojo): the index file the chat reads, and then the chat never updates it from the journal.
  - `VINCI_CHAT_UPFRONT=0` (chat_tools.mojo `upfront_enabled`): turns off the up-front recall. On by default.
  - `VINCI_EVAL_EMBED_LABEL`, `_CACHE`, `_FALLBACK_LABEL`, `_FALLBACK_URL` (embed_edge.mojo `default_config`).
  - `VINCI_CHAT_USAGE_LOG` (vinci_edge/chat_edge.py): one JSON line per model call with the token counts, the reply and the lexical-fallback flag.
  - `VINCI_CHAT_TEMPERATURE` (chat_edge.py): sent with the request when set. The chat sends no temperature by default, while M3 used 0, so the eval sets 0.
  - `VINCI_EVAL_LIVE_CACHE`, `VINCI_EVAL_QUERY_LOG` (vinci_edge/index_embed.py `query_with_fallback`): the scratch cache for live CPU vectors (written there, not in the cache the caller names), and one `{"source": ...}` record per question vector (`gpu`, `cpu-cached`, `cpu-live`, `error`). Unset, the fallback writes into its caller's cache, as before.
- **Judge.** `budget_sweep`'s judge prompts, verdict parser and lexical check, on the chat's final answer. Token totals cover the answer rounds only.
- **Citations.** A bracketed token of the answer counts when it has journal shape (`[i1]`, `[d1/2]`) or when this turn's tool results show it bracketed (`[answer_d8e33f5c_2:0]` from an evaluation index). A journal-shaped id the results did not show is flagged, as before. Other bracketed text is ordinary text (`chat_cite.mojo`, `cited_ids_with_evidence`; unit test in `test_chat_cite.mojo`).
- **Results.** One row per conversation in `~/.vinci/eval/results/chat-eval-<date>.jsonl`: config, qid, question, answer, tool calls, citation flags, not-saved, model calls, prompt/completion/total tokens, `query_sources` (gpu, cpu-cached, cpu-live, error counts), seconds, judge verdict, lexical hit, error. Error rows run again on resume. The summary has a question-vector table per configuration.

#### Smoke (2026-11-18, second run): clean, 0 errors

The first smoke gave 12 error rows, because the model's rephrased recall queries were not cached. Live CPU embedding (above) fixed that. Second smoke, 3 questions per set, both configurations, scratch output and live cache, deleted afterwards:

| config | LME judge | LME tool / open | personal judge | personal lexical | total tokens (LME / personal) | seconds per conversation (LME / personal) |
| --- | --- | --- | --- | --- | --- | --- |
| chat-w10 | 1/3 | 3/3 / 0/3 | 2/3 | 3/3 | 4,037 / 2,237 | 12.4 / 6.0 |
| chat-w2 | 1/3 | 3/3 / 1/3 | 2/3 | 3/3 | 3,288 / 2,225 | 8.7 / 5.0 |

Question vectors: 1 GPU-cache hit, 7 live CPU embeds, 0 CPU-cached, 0 lexical fallbacks, across 12 conversations. Citation flags 0, answers not saved 0. Three samples per cell are too few to read accuracy; they show the path works. The full run is about 320 conversations at roughly 8 s each, so about 45 minutes, plus the judge and any first-time CPU embeds.

#### Known issues, from the smoke transcripts

- (a) The model answered without a tool: not seen. All 12 conversations called recall, and 1 of the 6 w2 conversations also used open.
- (b) Answers not saved because they cite no id: fixed in the citation check above. Before the fix, the LME answers cited `[answer_d8e33f5c_2:0]`, which the journal-shaped check never read, so they were not saved. Now 0 of 12 are not saved.
- (c) Single-session-preference: not seen. No such question was in the first three of each set.

### Chat fixes (M4 step d)

The first full evaluation (`chat-eval-2026-11-18.jsonl`) found three faults. Each is fixed here, and the rest of the evaluation is still to run (Opus runs it).

**1. The crash (SIGSEGV on the second recall).** 32 of 200 LongMemEval conversations died with `vinci chat exited -11`, 24 qids in both configurations. Root cause: `vectors.load_vectors` took a raw address from `address_of(row[9])`, where `row[9]` was a temporary PythonObject. The temporary was destroyed inside that call, and the bytes it referred to were freed before `insert_from_address` read them (gdb: the `free` came from `PythonObject.__deinit__` at that line, on a row whose only remaining reference was the temporary). The copy then read freed memory, which in one heap state was unmapped. Fix: `vectors.insert_blob(matrix, row[9])` passes the PythonObject as a borrowed argument, so the reference lives for the whole copy. The raw address now never leaves the function that reads it. The reference-count drop itself was not traced to a single Mojo line; the gotchas file records the probes. Regression test: `tests/test_index_vectors.mojo` `test_loaded_rows_are_the_stored_vectors` (463 rows at 2560 dimensions, two loads, every row compared with its normalised stored value). It passes on the broken code too, so it does not reproduce the crash; the gate is `chat_eval --qids` on the crashing qids, which now gives 0 errors in both configurations (48 conversations, the 24 qids).

**2. Up-front recall.** Before the first model call of each turn, the chat runs `recall` on the person's message verbatim, packed as the recall tool packs (`chat_tools.mojo` `upfront_recall`, which calls `recall_query`, the body the tool uses, with the same lexical fallback). The chat adds it to the history as an assistant `recall` call and a `Result of recall:` user message (`chat.mojo` `turn`). Its ids count as evidence for the citation check, and its trust feeds the write gate, because `recall_text` sets the untrusted flag. It prints nothing, so the C# comparison holds for the three scripts that compare with C# (`plain-answer`, `idea-write`, `bad-journal-args`). If recall cannot run, the turn goes on without it, and the model can still call recall. On by default; `VINCI_CHAT_UPFRONT=0` turns it off. In the first evaluation only 7 of 371 question vectors matched the cached question, because the model rewrote its queries. With the up-front recall the person's own words are the first query, and in the check run each conversation's question vector came from the GPU cache. `chat_diff` passes 10 of 10 unchanged, so no script expectation needed a change.

**3. Preference answers.** The judge is not the fault: `PREFERENCE_JUDGE` in `budget_sweep.py` is LongMemEval's preference prompt (the response satisfies the rubric, yes or no), and it stays as it was. The fault was the answer prompt. Its rule "answer only from the excerpts, and say you do not know when they do not contain the answer" made the model read "suggest events this weekend" as a question about events in memory, so it answered "I don't know" even when the journal held the person's preference (38146c39: "turbinado sugar adds a richer flavor"; 32260d93: "stand-up comedy with storytelling on Netflix"). The chat's system prompt (`chat.mojo` `SYSTEM_RULES`) now has a rule for suggestions and advice: base them on what the journal says the person likes or has done, tailor the answer, cite those facts, and count a stated preference as an answer. On the five preference questions the chat-w10 result went from 0/5 to 1/5, and the answers now give tailored recommendations with citations. `budget_sweep.py`'s `SYSTEM_PROMPT` has the same rule missing, but it is left as it was so the M3 numbers stay comparable.

**Check run** (`chat_eval --set both --limit-questions 10 --configs chat-w10`, scratch output, not the full run):

| set | correct | errors | used a tool | mean tool calls | used open | answer not saved | cite flagged | mean total tokens | mean seconds | same 10 qids in the first run |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| LongMemEval | 4/10 (40%) | 0 | 2/10 | 0.30 | 1/10 | 0/10 | 0/10 | 5,026 | 15.9 | 3/9 (one row crashed) |
| personal | 7/10 (70%) | 0 | 0/10 | 0.00 | 0/10 | 0/10 | 0/10 | 1,418 | 3.5 | 6/10 |

Question vectors: 30 gpu cache hits, 10 cpu-cached, 4 cpu-live, 0 lexical fallbacks over 20 conversations. The up-front recall is why the tool columns fell: most answers come from the packed recall alone. Ten questions per set is too few to read accuracy; the full run is the measurement.

Gates: `make -C mojo check` passes; `chat_diff` 10 of 10; `vinci_diff` (`--generated 50 --seed 1`) 74 scenarios, 3633 of 3633 steps matching, 0 diverged (the M3 note's "683 of 683" was a smaller scenario set). `portal_diff` reports 22 diverged requests, all the same page: the portal's "links" count is 40 on the candidate and 39 on the oracle, because the portal scans local ports and one more local server now serves a page. The portal binary and portal code are unchanged, so this is the environment, not this change.

