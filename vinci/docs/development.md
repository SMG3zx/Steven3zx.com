# Development

```sh
dotnet test tests
```

The only NuGet package in the program is `Neo4j.Driver`, used by `vinci memory`. The journal commands have no dependencies beyond .NET 10.

| File | Holds |
| --- | --- |
| `src/Journal.cs` | The event record and the append-only file |
| `src/State.cs` | Rebuilding the current state from events |
| `src/App.cs` | The commands and the stage gates |
| `src/Tracker.cs` | Recording window time, commits and use, read from ActivityWatch and git |
| `src/Portal.cs` | The portal: which links go on the page, how it is drawn, and the server |
| `src/Memory/` | `vinci memory`: the Neo4j store (`Neo4jStore.cs`), recall and ranking (`Retrieval.cs`), the API (`Service.cs`), the worker (`Worker.cs`), the MCP server (`Mcp.cs`), the audit log (`Audit.cs`), the web pages (`Pages/`), and the benchmarks (`Evaluation/`) |
| `src/Chat/` | `vinci` and `vinci chat`: the conversation (`Conversation.cs`), the model client (`OpenAiChat.cs`), the tools (`Tools.cs`), the memory API (`MemoryApi.cs`), and the journal-to-memory sync (`JournalSync.cs`) |
| `src/Review.cs` | The findings, the limits, and what to do next |
| `src/Stats.cs` | Control limits |
| `deploy/awatcher.service` | The systemd user unit that runs ActivityWatch |
| `deploy/portal.service` | The systemd user unit that runs `vinci serve` |
| `deploy/memory.service`, `deploy/memory-worker.service` | The systemd user units for `vinci memory serve` and `worker` |
| `deploy/memory/` | The Neo4j compose file and the settings example |
| `deploy/models/` | The Modelfile that builds the chat model, and how to set up both models |
| `docs/m0-results.md` | The results of mark 2's M0 phase: the calibration tasks, the stop conditions, and the phase estimates |
| `docs/m2-results.md` | The results of mark 2's M2 phase: the Mojo core, the differential test against the C# binary, and the known gaps |
| `docs/mojo-gotchas.md` | Mojo mistakes that agents make twice, with a correct snippet for each |
| `mojo/` | The Mojo project: the pinned toolchain (`pyproject.toml`), the Makefile, tests, `scripts/attempt.sh`, the calibration tasks in `calibration/`, and the agent attempt log (`m2-attempts.jsonl`) |
| `mojo/src/` | The Mojo port of vinci, built into `mojo/build/vinci`. `vinci.mojo` is the entry point and dispatch. `cmd_ideas.mojo`, `cmd_knowledge.mojo` and `cmd_ops.mojo` hold the commands. `journal.mojo`, `state.mojo`, `session.mojo`, `review.mojo` and `stats.mojo` hold the data and the logic. `jsonline.mojo`, `text.mojo` and `timeutil.mojo` hold the helpers. |
| `mojo/py/vinci_diff/` | The differential test: runs the C# binary (the oracle) and the Mojo binary on the same scenarios, one command at a time, and compares exit code, stdout, stderr and the journal. Scenario and generator code, the report, and the CLI (`cli.py`) |
| `mojo/difftest/` | The differential test's inputs: hand-written scenarios (`scenarios/`), a mid-project journal fixture (`fixtures/`), and inputs that crash the C# binary (`oracle-bugs/`) |
| `mojo/py/vinci_eval/` | The evaluation scripts: the LongMemEval subset (`lme_subset.py`) and reference baseline (`lme_baseline.py`), and the personal baseline (`personal_baseline.py`) |
| `mojo/eval/` | The fixed LongMemEval subset ids (`longmemeval-subset.json`) |

`VINCI_NOW` (an ISO 8601 time with an offset) replaces the clock, and `VINCI_NO_TRACK=1` skips the window and git reads so a run only appends what the command itself writes. Both exist for the mark 2 differential test, which runs the C# binary (`mojo/build/oracle/`) against the Mojo port on the same journal; neither is set in normal use.
