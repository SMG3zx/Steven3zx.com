# Development

```sh
make -C py test           # the pytest suite (py/tests)
make -C py gate           # tests and the eval thresholds: GATE PASS or GATE FAIL
```

## The gate

`make -C py gate` (`py/scripts/gate.sh`) runs, in order, and prints one `STAGE` line per stage and a final `GATE PASS` or `GATE FAIL (stages)`:

| Stage | What it checks | Needs |
| --- | --- | --- |
| `tests` | the pytest suite (`py/tests`) | uv |
| `personal` | the personal set (60 questions, 48 answerable): lexical and lexical+vector hits and abstentions | `~/.vinci/staging`, `~/.vinci/eval` |
| `feed` | the feed set (15 questions) after `vinci feed run` on a copy | `~/.vinci/staging`, `~/.vinci/eval` |
| `lme` | LongMemEval, 100 questions, lexical+vector any@10 and all@10 | `~/.vinci/eval`, the CPU embed server on 127.0.0.1:8081 |

**The evals are for this machine.** They need `~/.vinci/staging` and `~/.vinci/eval`, which hold personal data. Without them every eval stage prints `SKIP evals: data missing`. A SKIP fails the gate; set `GATE_ALLOW_SKIP=1` to let a skip pass (only for a machine that has no data). The `lme` stage also SKIPs when the CPU embed server is down, and never passes on a skip. The LongMemEval indexes under `~/.vinci/eval/lme-index` must hold the facts under source `journal`; `lme_index_eval --prepare` rebuilds them from the GPU cache copy.

The LongMemEval stage reproduces the M3 record with the old equal fusion weights, `FUSION_SEMANTIC=0.4,0.4,0.2 uv run python -m vinci_eval.lme_index_eval --score`: lexical+vector any@10 98.9, all@10 94.4, 2,178 tokens. With the default weights (0.25:1) it measures all@10 95.6 and about 2,007 tokens. Its records are stored under source `journal`, with the session in the id (`session:turn`), so `recall` finds them under the default `--sources journal`.

`py/eval/gate-thresholds.json` holds the floors: the personal hits and abstentions, the feed hits, and the LongMemEval any@10 and all@10. Each number has a `from` note saying where it was measured. The gate prints each measured number beside its floor and a minimum detectable difference (a rule of thumb from [mark 3, section 2.7](mark3-plan.md), about 8 net questions per 48, scaled by the square root of the set size). A change that moves a number by less than that is noise.


## Fresh clone

`sh py/scripts/fresh-clone-check.sh` proves that the repository works from a clean clone. It makes a sparse, shallow clone of `vinci/` from origin, runs `make test` with no `dotnet` on `PATH`, does a scratch install (`VINCI_APP_DIR` set, so the portal is not restarted), and runs `vinci stats`, `vinci index rebuild`, `vinci recall x` and `vinci help` from `/` with a temporary `VINCI_HOME`.

The toolchain is pinned in `py/pyproject.toml` (Python 3.13 or later, with pytest and ruff as dev tools) and run through `uv`. Nothing else is needed: no .NET and no database. `unset VIRTUAL_ENV` before `uv run` if another project's environment is active; the Makefile does this for its own commands.

`./install.sh` runs the same check, builds the release binary, and installs it. See [the README](../README.md#install).

| File | Holds |
| --- | --- |
| `py/vinci_cli/vinci.py` | The entry point: settings, the clock, routing and dispatch. `memory` is refused with a message that it was removed in mark 2 |
| `py/vinci_core/cmd_ideas.py`, `cmd_knowledge.py`, `cmd_ops.py` | The journal commands: ideas and time, notes and sources, measurements, predictions, the portal's bookmarks, and `help` |
| `py/vinci_core/journal.py`, `state.py`, `session.py`, `review.py`, `stats.py` | The append-only file, the fold that rebuilds current state, the session, the findings and limits, and control limits |
| `py/vinci_core/jsonline.py`, `text.py`, `timeutil.py` | The helpers |
| `py/vinci_core/index_store.py`, `index_build.py`, `recall.py`, `fusion.py`, `abstain.py`, `vectors.py`, `vector_scan.py`, `cmd_index.py`, `open_view.py`, `pack.py`, `dates.py`, `handles.py`, `terms.py` | The index at `$VINCI_HOME/index.sqlite`, its build from the journal, recall, fusion, abstention, the vector route, `open` and the packed context |
| `py/vinci_core/chat.py`, `chat_tools.py`, `chat_action.py`, `chat_cite.py`, `chat_gate.py`, `chat_open.py` | `vinci` and `vinci chat`: the turn loop and citations, the tools, the action parser, the write gate and `open`'s output cap |
| `py/vinci_core/portal*.py`, `route.py` | `vinci serve`, the portal: the request answers and the scan (`portal.py`), saved links and the card (`portal_link.py`), `portal.json` (`portal_json.py`), addresses (`portal_url.py`), the page and its assets, and the query route (`route.py`) |
| `py/vinci_core/feed.py`, `feed_store.py`, `feed_git.py`, `feed_vaults.py`, `feed_activity.py`, `cmd_feed.py` | The feeds (`vinci feed`): the record contract and its store, the git, vault and activity readers, and the command |
| `py/vinci_core/track.py` | The window and git tracker that runs before a journal command |
| `py/vinci_core/verify.py`, `backup.py`, `command_kinds.py`, `embed_config.py`, `portal_entities.py` | `vinci verify` and `vinci backup`, the command table the chat checks, the embedding settings, and the HTML entity table |
| `py/vinci_edge/` | The Python edges, standard library only: git, the Markdown walk, the journal bytes for the feeds, embeddings, the portal's HTTP layer, the chat's model call, the tracker's reads |
| `py/vinci_eval/` | The evaluation scripts: the LongMemEval subset and reference baseline, the index and budget evaluations, and the gate (`gate.py`) |
| `py/tests/` | The pytest suite: the core, the commands, the feeds, the portal, the chat and the index |
| `py/eval/` | The fixed LongMemEval subset ids (`longmemeval-subset.json`), the feed question set (`feed-questions.json`), and the gate thresholds (`gate-thresholds.json`) |
| `py/scripts/` | The gate (`gate.sh`), its selftest (`gate-selftest.sh`), and the fresh-clone check (`fresh-clone-check.sh`) |
| `docs/history/` | The plans and results of earlier marks. The Mojo build, the golden harness (tag `golden-final`) and the C# oracle are in git history |

## Backup and verify

`vinci backup [--dir DIR] [--keep N]` copies `journal.jsonl` to `$VINCI_HOME/backups/journal/` as `journal-<UTC time>.jsonl`, with a `.sha256` manifest that `sha256sum -c` reads. Backup reads without the lock and cuts the copy after the journal's last newline, so a line being written is left out. The bytes are checked before the copy gets its name, and an existing file is never replaced. `--keep N` removes only older copies of this name pattern. `vinci verify [--index] [--strict]` checks the journal without writing: the lines, the ids that must be unique, unknown kinds and future times. `--index` also compares the stored index with a rebuild in a temporary folder. The daily timer is `deploy/vinci-backup.timer`; it is installed and enabled on this machine (it ran on 2026-11-19 and 2026-11-20).

