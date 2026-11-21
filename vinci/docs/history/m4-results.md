# M4 results: chat in Mojo, then delete C# (2026-11-18)

M4 of [the mark 2 plan](mark2-plan.md): chat and the portal in Mojo, a chat-loop evaluation, then the C# project, Neo4j and their units removed.

## What was built

| Step | Commit | What |
|---|---|---|
| 4a | 74eb63f | `vinci chat` in Mojo against llama-server. `recall` reads the Mojo index, `open` is new, and `remember` and `read` write to the journal, then update the index. The citation check, and a yes/no before writes once untrusted text is in the context. |
| 4b | 25544c4 | `vinci serve` (the portal): routes and pages in Mojo, behind a Python `http.server` edge. |
| 4c | 20b2b5b | The chat-loop evaluation (`vinci_eval.chat_eval`). Live CPU embeddings for the queries the model rewrites. The citation check accepts any id the turn's tools returned. |
| 4d | 8a9245f | Fixed a use-after-free in vector loading that crashed chat. The user's message is recalled verbatim before the first model call. Suggestions are now based on stated preferences. |

Differential tests against the C# binary: commands 683/683 steps (3,633/3,633 with 50 generated scenarios), portal 136/136 requests, chat 10/10 scripts (3 compared with C#, 7 Mojo-only, because C# needs Neo4j for those tools).

## Chat-loop evaluation (4c and 4d)

The real `vinci chat`, Qwen 27B on port 8080, with tools. Qwen also judges, as in M3. Results: `~/.vinci/eval/results/chat-eval-2026-11-18b.jsonl`, 320 conversations, 0 errors.

| config | LongMemEval | personal (judge) | personal (lexical) | tool used | open used | mean tokens LME | mean s LME |
|---|---|---|---|---|---|---|---|
| single-shot, top 10 whole (M3) | 69/100 | 33/60 | 29/48 | – | – | 2,461 context | 12.5 |
| chat-w10 (budget 3000, whole 10; the default) | 65/100 | 34/60 | 28/48 | 12% | 3% | 3,912 | 12.1 |
| chat-w2 (budget 1000, whole 2, pointers) | 49/100 | 35/60 | 31/48 | 45% | 35% | 4,243 | 9.3 |

Mean tokens for chat cover every model call in the turn, the system prompt included. The M3 figure is context tokens only.

- **chat-w10 matches single-shot within noise.** On LongMemEval, 11 questions were right only in single-shot and 7 only in chat (exact McNemar p = 0.48). Three of the 11 are abstentions the judge marked wrong although chat said "I don't have any record of …". Per type: temporal 19/30 (20), multi-session 17/30 (19), knowledge-update 13/15 (13), abstention 5/10 (8), preference 1/5 (0).
- **Pointers still cost answers on LongMemEval, even with `open`**: 49 against 65. The model opened a record in only 35% of conversations. On the personal set, pointers are slightly ahead (35 and 31/48 lexical) but within noise. The default stays whole records.
- **Up-front recall fixed the query drift.** Before it, 7 of 371 question vectors were the user's own words, and LongMemEval was at 57/90. After it, 220 of 349 recalls hit the cached question vector.
- **The crash** (first run: 32 of 200 LongMemEval conversations died with SIGSEGV) came from `load_vectors` copying from the address of a temporary `PythonObject` that had already been freed. After the fix: 0 errors in 320 conversations.
- **Preference questions** failed because the prompt told the model to abstain unless the excerpts held the answer. With the new rule they reach 1/5. The judge rubric (LongMemEval's own) is strict about what a suggestion must avoid.
- **Answers not saved** (no recalled id cited): 7% on LongMemEval and 32% on the personal set, mostly abstentions and answers that cite nothing. Open for M6, where answer quality is scored.

## Delete C# (4e)

- The C# project (`src/`, `tests/`, `dotnet-tools.json`), the Neo4j compose file and the `memory` / `memory-worker` units are gone from the repo. `vinci memory …` now prints a "removed in mark 2" error.
- The C# binary is archived at `~/.local/share/vinci-csharp-oracle/` (it needs the .NET 10 runtime), with its source in `source/`. The three harnesses read it through `VINCI_ORACLE`. Against the archive: commands 3,633/3,633, portal 136/136, chat 10/10.
- `install.sh` checks and builds the Mojo binary and installs it to `~/.local/share/vinci/`, with its runtime libraries and the Python edge. A launcher pins the interpreter from `mojo/.venv`, so the installed command still depends on the checkout's venv. `portal.service` now runs the Mojo binary.
- On the machine: the old C# install was moved to `~/.local/share/vinci-csharp-installed-2026-11-18`. The memory units were stopped and removed. `~/.config/vinci/memory.env` keeps only the model and embedding settings (the full original is in `~/.vinci/backups/`). The memory audit and sync files were moved to `~/.vinci/backups/`. The user's uncommitted `Tracker.cs` edit is saved there as a patch.
- The Neo4j container was reported stopped. Correction 2026-11-21: ports 7474 and 7687 were still listening on all interfaces; see mark3-plan D6.

## Open after M4

- Abstention under chat (5/10, against 8/10 single-shot), partly judge noise.
- Pointers with `open` need a model that opens more often, or a better hint; revisit with M7 routing.
- The summarize step was not built. Context of around 2.5–4k tokens reads fine.
- Time tracking is paused until the M5 feeds.
