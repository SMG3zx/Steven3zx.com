# M2 results: the core in Mojo

Date: 2026-11-16. This is the result of phase M2 in [mark 2](mark2-plan.md), section 7.

## What was built

The Mojo binary is `mojo/build/vinci`. Build it with `make -C mojo build`. The source is about 5,400 lines in `mojo/src/`.

| Module | Holds |
| --- | --- |
| `journal.mojo` | The append-only journal, one JSON object per line (`src/Journal.cs`) |
| `timeutil.mojo` | Instants and durations, as .NET keeps them |
| `jsonline.mojo` | JSON written and read the way System.Text.Json does it |
| `text.mojo` | The text helpers from `src/App.cs` |
| `state.mojo` | The fold over the journal that rebuilds current state (`src/State.cs`) |
| `session.mojo` | One command's working set: the state read once, and the argument helpers |
| `vinci.mojo` | The entry point: settings, clock, routing and dispatch (`src/Program.cs`, `src/App.cs`) |
| `cmd_ideas.mojo` | The ideas group: idea to grown, spent, tend, retire |
| `cmd_knowledge.mojo` | The knowledge group: bookmarks, sources, notes, links, predictions, logs, show, stats |
| `cmd_ops.mojo` | The ops group: issues, principles, commit, kept, missed, review, path |
| `review.mojo` | The review loop: what to fix now, the next step, and where ideas sit longest |
| `stats.mojo` | Shewhart control limits |

Scope:

- Every command except `chat`, `serve` and `memory`.
- `sync` is a no-op. The port does not track. Tracking returns as feeds in M5.
- Inputs that crash the C# binary are refused with a user error. Two cases are known: a huge `spent` in minutes, and a huge `advance` in days. The inputs are kept in `mojo/difftest/oracle-bugs/`. The C# binary appends the event first and then throws. Every later command then reads a broken journal.
- With no arguments, `vinci`, `chat`, `serve` and `memory` exit 2 with "not in the Mojo port yet".

## The oracle

The C# binary is the oracle. Two environment variables let both sides run on the same journal with the same clock:

| Variable | Effect in the C# binary |
| --- | --- |
| `VINCI_NOW` | Replaces the clock. An ISO 8601 time with an offset. |
| `VINCI_NO_TRACK` | Skips the window and git reads, so a run only appends what the command writes. |

Both are in [development.md](development.md).

## The differential test

The test is `mojo/py/vinci_diff`. It runs each command on both binaries. It compares the exit code, stdout, stderr and the journal after every step. Each side gets its own copy of the journal.

Scenarios are in `mojo/difftest/scenarios/`: 23 hand-written files. They include read-only commands on a copy of the real journal.

Run it:

```sh
cd mojo/py && uv run python -m vinci_diff --generated 100 --seed 7
```

Pass a candidate or oracle path as an absolute path if it is not the default.

### Result

| Run | Scenarios | Steps | Match |
| --- | --- | --- | --- |
| Hand-written (re-run by the reviewer) | 23 | | All |
| Generated, seed 7 (re-run by the reviewer; no agent used this seed) | 100 | 6,153 | 6,153 of 6,153 |
| Generated, seed 1 (agents' run) | | 3,622 | 3,622 of 3,622 |

The generated seed 7 run is the result of record. Every step matches.

## Speed

One data point: `review` on the real journal, 10 runs each.

| Binary | Time per run | Note |
| --- | --- | --- |
| Mojo | 28 ms | |
| C# | 70 ms | Mostly process start-up |

## Calibration of the agents

This table is for the agent work, as in [m0-results.md](m0-results.md). The log is `mojo/m2-attempts.jsonl`.

| Piece | Attempts logged | First try passed | Passed by third try | First failure |
| --- | --- | --- | --- | --- |
| W1: core (journal, state, text) | 2 | no | yes, see note | Formatting. Then `^` on a borrowed value and a `raises` context. |
| Skeleton: the binary and dispatch | 2 | no | yes | A `def` signature wrapped by `mojo format` |
| Ideas | 3 | no | yes | An `Instant` copied implicitly. Then a `raises` call in a non-raising context. |
| Knowledge | 2 | no | yes | `url^` on a borrowed argument |
| Ops | 2 | no | yes | A struct with no `@fieldwise_init` |

Totals: first try 0 of 5. Passed by third try 5 of 5.

Note on W1: its second attempt is logged as failed. The cause was another agent's unfinished Python, which broke `make check`. W1's Mojo steps passed on that attempt. The log has four gate passes and one Mojo-only pass.

Gotchas from these failures are in [mojo-gotchas.md](mojo-gotchas.md).

## Stop conditions

Section 2 of the plan sets three stop conditions. M0 said they would be checked again at the end of M2. Here is each one.

1. **Pass by third try, at least 60%.** 5 of 5 pieces pass by the third try (see the note on W1). The first try was 0 of 5. The first failures were formatting, ownership and `raises` contexts. The sample is five pieces, so the rate is weak evidence.
2. **A phase takes more than twice its estimate.** M2 was estimated at 3 working days. The agent work took about an hour of wall time, plus review. Well inside the limit.
3. **A release breaks the pinned build twice in a quarter.** No release broke the pinned build (Mojo 1.2.0). None so far.

Verdict: continue in Mojo.

## Known gaps

Each gap is from the agents' reports.

| Gap | Effect |
| --- | --- |
| Case-insensitive compares and lowercasing are ASCII-only | C# also folds non-ASCII letters |
| The tags index orders ties by byte order | C# uses the culture comparer. Same for lowercase ASCII. |
| `IsWebAddress` approximates `System.Uri` | Edge inputs may differ |
| `show` prints "(touch X)" when a stage's touch total is non-zero | C# prints it when any spent event was recorded in that stage. Differs only below 1e-7 minutes. |
| Invalid UTF-8 on stdin exits 3 | .NET substitutes replacement characters |
| `folder_arg` assumes .NET collapses a leading `//` | The Mojo side follows that assumption |
| No-argument `vinci`, `chat`, `serve` and `memory` exit 2 | Not in the port yet |

## Next

M3: memory. The index in Mojo, measured against the M1 baseline in [memory.md](memory.md). The C# binary stays installed until M4.
