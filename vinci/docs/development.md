# Development

```sh
make -C mojo check      # format check, build, tests, Python lint and types
make -C mojo gate       # check, the golden comparisons, the eval thresholds: GATE PASS or GATE FAIL
```

## The gate

`make -C mojo gate` (`mojo/scripts/gate.sh`) runs, in order, and prints one `STAGE` line per stage and a final `GATE PASS` or `GATE FAIL (stages)`:

| Stage | What it checks | Needs |
| --- | --- | --- |
| `check` | `make check`, then a fresh build of `mojo/build/vinci` | uv |
| `cli` | the journal commands: 23 hand-written and 50 generated scenarios (`--generated 50 --seed 1`), every step's exit code, stdout, stderr and journal | the goldens |
| `chat` | the three chat scripts that the C# binary also runs, against their goldens; the Mojo-only scripts against their written expectations | the goldens |
| `portal` | `vinci serve`: 136 requests over four journal kinds, each status, header and body, and the journal and previews after each run | the goldens |
| `sync` | the tracker: ten cases against a fake ActivityWatch, each step's exit code, output, journal and the queries the fake server received | the goldens |
| `personal` | the personal set (60 questions, 48 answerable): lexical and lexical+vector hits and abstentions | `~/.vinci/staging`, `~/.vinci/eval` |
| `feed` | the feed set (15 questions) after `vinci feed run` on a copy | `~/.vinci/staging`, `~/.vinci/eval` |
| `lme` | LongMemEval, 100 questions, lexical+vector any@10 and all@10 | `~/.vinci/eval`, the CPU embed server on 127.0.0.1:8081 |

The golden stages need no .NET and no oracle: the gate sets `VINCI_ORACLE` to a missing path, so a live oracle run would fail. The evals run on copies (a scratch home, a copy of the embedding cache and of the GPU cache, a scratch results folder). Nothing under `~/.vinci` is written.

**The evals are for this machine.** They need `~/.vinci/staging` and `~/.vinci/eval`, which hold personal data. Without them every eval stage prints `SKIP evals: data missing`. A SKIP fails the gate; set `GATE_ALLOW_SKIP=1` to let a skip pass (only for a machine that has no data). The `lme` stage also SKIPs when the CPU embed server is down, and never passes on a skip. The LongMemEval indexes under `~/.vinci/eval/lme-index` must hold the facts under source `journal`; `lme_index_eval --prepare` rebuilds them from the GPU cache copy.

The LongMemEval stage reproduces the M3 record with the old equal fusion weights, `FUSION_SEMANTIC=0.4,0.4,0.2 uv run python -m vinci_eval.lme_index_eval --score`: lexical+vector any@10 98.9, all@10 94.4, 2,178 tokens. With the default weights (0.25:1) it measures all@10 95.6 and about 2,007 tokens. Its records are stored under source `journal`, with the session in the id (`session:turn`), so `recall` finds them under the default `--sources journal`.

`mojo/eval/gate-thresholds.json` holds the floors: the personal hits and abstentions, the feed hits, and the LongMemEval any@10 and all@10. Each number has a `from` note saying where it was measured. The gate prints each measured number beside its floor and a minimum detectable difference (a rule of thumb from [mark 3, section 2.7](mark3-plan.md), about 8 net questions per 48, scaled by the square root of the set size). A change that moves a number by less than that is noise.

Checks that prove the gate can fail, and that the goldens agree with the live oracle: `sh scripts/gate-selftest.sh` (it runs a wrong candidate and four tampered copies of the goldens, each of which must fail, and, with the oracle present, the live `--oracle` mode of each harness).

## Golden files

The golden files are what the C# oracle printed, saved in `mojo/difftest/golden/<harness>/`: one JSON file per scenario or case, and `_meta.json` with the sha256 of the oracle binary and of its `vinci.dll`, the date, the repository commit and the file count. The `portal` bodies are in one gzip per journal kind (`<kind>-bodies.gz`, base64 JSON), so a failing body can be read with `gunzip`. A journal is stored as the bytes each step appended, and `golden.py` checks each link's sha256.

Each harness has three modes:

- **default**: compares the candidate with the goldens. No .NET, no oracle directory. `VINCI_ORACLE` may point at a missing path.
- **`--oracle`**: the live comparison with the C# binary, as before the goldens existed. It warns when the oracle is not the binary the goldens came from.
- **`--record`**: runs the oracle, checks the candidate against what it recorded, and writes the goldens only if they agree. A disagreement refuses the record and keeps the staged files for reading. `--force` writes them anyway, with a loud warning; the golden check then fails until the candidate is fixed.

Only a recorded oracle output is ever a golden. Three things are not in the goldens, on purpose:

- The hand-written scenario that copies the real journal (`real-journal-readonly`): the journal is personal. It runs only with `--oracle`, and the default prints a SKIP for it.
- The portal's `staging` kind uses the committed fixture `mojo/difftest/fixtures/mid-project.jsonl` as its journal, for the same reason. The live portal run defaults to `~/.vinci/staging`; pass `--journal` to choose.
- Paths and ports. A golden holds no path of this machine: the work folder is written as `<ROOT>` or `<BASE>`, and the portal's fixture ports are pinned (47300 to 47310) because the portal orders its cards by URL. Goldens need those ports free; `--oracle` takes random ones.

Regenerating goldens (needs .NET and the oracle):

```sh
cd mojo/py
uv run python -m vinci_diff --record --candidate ../build/vinci --generated 50 --seed 1
uv run python -m vinci_diff.chat_diff --record --candidate ../build/vinci
uv run python -m vinci_diff.portal_diff --record --candidate ../build/vinci
uv run python -m vinci_diff.sync_diff --record --candidate ../build/vinci
```

The generated scenarios are stored with their steps and their lengths, and the golden check replays the generator against the recorded journal, so a change to the generator fails the cli stage rather than silently changing the gate.

## Fresh clone

`sh mojo/scripts/fresh-clone-check.sh` proves that the repository works from a clean clone. It makes a sparse, shallow clone of `vinci/` from origin, runs `make check` with the oracle missing and no `dotnet` on `PATH`, does a scratch install (`VINCI_APP_DIR` set, so the portal is not restarted), runs `vinci stats`, `vinci recall x` and `vinci help` from `/` with a temporary `VINCI_HOME`, and runs the golden CLI comparison against the installed launcher. `--overlay-worktree` copies this checkout's uncommitted `vinci/` files into the clone, so the check can run before the goldens are committed. It needs the network for uv and the pinned mojo wheel.

The toolchain is pinned in `mojo/pyproject.toml` (Mojo 1.2.0, Python 3.13) and run through `uv`. Nothing else is needed: no .NET and no database. `unset VIRTUAL_ENV` before `uv run` if another project's environment is active; the Makefile does this for its own commands.

`./install.sh` runs the same check, builds the release binary, and installs it. See [the README](../README.md#install).

| File | Holds |
| --- | --- |
| `mojo/src/vinci.mojo` | The entry point: settings, the clock, routing and dispatch. `memory` is refused with a message that it was removed in mark 2 |
| `mojo/src/cmd_ideas.mojo`, `cmd_knowledge.mojo`, `cmd_ops.mojo` | The journal commands: ideas and time, notes and sources, measurements, predictions, the portal's bookmarks, and `help` |
| `mojo/src/journal.mojo`, `state.mojo`, `session.mojo`, `review.mojo`, `stats.mojo` | The append-only file, the fold that rebuilds current state, the session, the findings and limits, and control limits |
| `mojo/src/jsonline.mojo`, `text.mojo`, `timeutil.mojo` | The helpers |
| `mojo/src/index_store.mojo`, `index_build.mojo`, `recall.mojo`, `fusion.mojo`, `abstain.mojo`, `vectors.mojo`, `vector_scan.mojo`, `cmd_index.mojo`, `open_view.mojo`, `pack.mojo` | The index at `$VINCI_HOME/index.sqlite` (derived from the journal), recall and its routes, abstention, the vector scan, `index`, `recall`, `open`, and packed output |
| `mojo/src/chat.mojo`, `chat_tools.mojo`, `chat_action.mojo`, `chat_cite.mojo`, `chat_gate.mojo`, `chat_open.mojo`, `chat_edge.mojo` | `vinci` and `vinci chat`: the turn loop and citations, the tools, the action parser, the citation check, the write confirmation, the open cap, and the Python calls (see [memory.md](memory.md#chat-mark-2-m4a)) |
| `mojo/src/portal*.mojo`, `route.mojo` | `vinci serve`, the portal: the route and scan logic (`portal.mojo`), saved links and the card (`portal_link.mojo`), `portal.json` (`portal_json.mojo`), addresses and request paths (`portal_url.mojo`), HTML text (`portal_html.mojo`), the page (`portal_page.mojo`), and the Python edge calls (`portal_io.mojo`). `portal_entities.mojo` and `portal_assets.mojo` are generated by `mojo/py/vinci_diff/portal_tables.py` from the archived C# source |
| `mojo/src/embed_edge.mojo` | The Python edge for embeddings (`vinci_edge.index_embed`) |
| `mojo/src/feed.mojo`, `feed_store.mojo`, `feed_git.mojo`, `feed_vaults.mojo`, `feed_edge.mojo`, `cmd_feed.mojo` | The feeds (`vinci feed`): the record contract and its store, the git and vault readers, their Python edge, and the command. See [feeds.md](feeds.md) |
| `mojo/py/vinci_edge/` | The Python edges, standard library only: git, the Markdown walk and the journal bytes for the feeds (`feed_edge.py`); the model and standard-input calls for chat (`chat_edge.py`), reading text and web pages (`reading_edge.py`), embeddings and their cache (`embed.py`, `index_embed.py`), token counts (`llama_count.py`), and the portal's HTTP and network calls (`portal_edge.py`) |
| `mojo/py/vinci_diff/` | The differential tests against the archived C# binary (see below): `cli.py` (journal commands), `chat_diff.py` (chat scripts), `portal_diff.py` (portal requests), `sync_diff.py` (the tracker). `golden.py` reads and writes the goldens. `oracle.py` holds the one setting for the oracle. `portal_tables.py` and `portal_truth/` are the portal's constant tables and their provenance |
| `mojo/py/vinci_eval/` | The evaluation scripts: the LongMemEval subset and reference baseline, the index and budget evaluations, and the chat-loop evaluation. `gate.py` runs the three eval stages of the gate |
| `mojo/tests/` | The Mojo tests, each a program with `def main()` that raises on failure |
| `mojo/difftest/` | The differential test's inputs: hand-written scenarios (`scenarios/`), chat scripts (`chat/`), a mid-project journal fixture (`fixtures/`), and inputs that crash the C# binary (`oracle-bugs/`). `golden/` holds the oracle's recorded outputs (see [the gate](#the-gate)) |
| `mojo/eval/` | The fixed LongMemEval subset ids (`longmemeval-subset.json`), the feed question set (`feed-questions.json`), and the gate's floors (`gate-thresholds.json`) |
| `mojo/calibration/` | The calibration tasks from mark 2's M0 phase |
| `mojo/scripts/` | `attempt.sh` (one calibration attempt, logged), `fmt-check.sh`, the gate (`gate.sh`), its selftest (`gate-selftest.sh`) and the fresh-clone check (`fresh-clone-check.sh`) |
| `mojo/m2-attempts.jsonl` | The agent attempt log |
| `docs/mojo-gotchas.md` | Mojo mistakes that agents make twice, with a correct snippet for each. Read it first |
| `docs/mark2-plan.md` | The mark 2 plan: the phases, the gates, and what each phase removed |
| `docs/m0-results.md`, `m2-results.md`, `m3-results.md`, `m4-results.md` | The results of mark 2's phases M0, M2, M3 and M4 |
| `deploy/llm/` | The systemd user units for llama-server: the chat model and the embedding model |
| `deploy/models/` | The Modelfile that builds the chat model, and how to set up both models |
| `deploy/portal.service` | The systemd user unit that runs `vinci serve` |
| `deploy/awatcher.service` | The systemd user unit that runs ActivityWatch (for the window time: the tracker and the activity feed) |

## Backup and verify

`vinci backup [--dir DIR] [--keep N]` copies `journal.jsonl` to `$VINCI_HOME/backups/journal/` as `journal-<UTC time>.jsonl`, with a `.sha256` manifest that `sha256sum -c` reads. Backup reads without the lock and cuts the copy after the journal's last newline, so a line being written is left out. The bytes are checked before the copy gets its name, and an existing file is never replaced. `--keep N` removes only older copies of this name pattern. `vinci verify [--index] [--strict]` checks the journal without writing: the lines, the ids that must be unique, unknown kinds and future times. `--index` also compares the stored index with a rebuild in a temporary folder. The daily timer is `deploy/vinci-backup.timer`; it is in the repository and is not installed.

## The oracle

The differential tests compare the Mojo binary with the C# binary that mark 2 replaced. That binary is archived outside the repository at `~/.local/share/vinci-csharp-oracle/vinci`:

- It is a framework-dependent .NET 10 publish. It needs the .NET 10 runtime installed.
- `source/` beside it holds `src/` and `tests/` as they were at the commit in `source/HEAD.txt`, and `portal_truth/Truth.cs`. The portal's constant tables are generated from these files, and the coverage report reads `src/App.cs`.
- Set `VINCI_ORACLE` to use another binary. Every harness reads that one setting (`mojo/py/vinci_diff/oracle.py`).

Nothing in the repository builds the oracle. The C# source is in git history before the removal commit.

Two environment variables let the oracle and the Mojo binary run on the same journal with the same clock. Both are read by the Mojo binary too:

- `VINCI_NOW` (an ISO 8601 time with an offset) replaces the clock.
- `VINCI_NO_TRACK=1` skips the window and git reads (the sync that runs before commands). Both binaries honour it, and the differential tests set it.

The differential tests are run from `mojo/py`. By default they compare with the goldens (see [the gate](#the-gate)); add `--oracle` to compare with the live C# binary, or `--record` to regenerate the goldens from it:

```sh
uv run python -m vinci_diff --generated 50 --seed 1      # journal commands, against the goldens
uv run python -m vinci_diff.chat_diff                    # chat scripts
uv run python -m vinci_diff.portal_diff                  # the portal's requests
uv run python -m vinci_diff.sync_diff                    # the tracker
```

`--oracle-binary PATH` names the C# binary for `--oracle` and `--record` (default `VINCI_ORACLE`). The Mojo binary is `mojo/build/vinci` (`make -C mojo build`). Pass `--candidate` (an absolute path) to test another binary.
