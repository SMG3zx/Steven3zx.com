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
