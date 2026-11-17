# M0 results: the bet

Date: 2026-11-16. This is the result of phase M0 in [mark 2](mark2-plan.md), section 7.

## Setup

- Mojo 1.2.0 from PyPI, installed with uv. Not pixi.
- Claude Haiku 5.5 subagents wrote the code. Claude Opus 5.5 directed them and reviewed their work.
- Modular's skills and the docs MCP were not installed. The stdlib has no local sources either. So this is the no-docs condition, and the results are a lower bound.

## Calibration tasks

| Task | Attempts to pass | First try passed | Passed by third try | First failure | Agent run time | Result check |
| --- | --- | --- | --- | --- | --- | --- |
| c1: journal to state | 2 | no | yes | Copying a non-copyable struct out of a `List` | 5.3 min | Output matches the C# `vinci ideas` (age and flow columns removed) on the real journal (19 ideas) and on a fixture (6 ideas). The reviewer re-ran both. |
| c2: SQLite with FTS5 | 4 | no | no | `mojo format`, then `Int(PythonObject)`, then `len(String)` | 4.8 min | Tests pass. Reviewed. |
| c3: SIMD cosine top-k | 2 | no (format only; build and tests passed) | yes | `mojo format` | 2.6 min | The reviewer re-ran it. Scalar 58.0 ms and SIMD 6.3 ms median per query at N = 100k, D = 768, k = 10 (9.3x). Pure Python is about 1.7 s, scaled from 10k rows (about 300x). |

## Stop conditions

Section 2 of the plan sets three stop conditions. Here is each one.

1. **Fewer than 60% pass by the third try.** Two of three pass by the third try, which is 67%. That passes, narrowly, and the sample is three. A format hook would fix format-only failures. Item 3 of the plan's list runs checks after each edit, and this hook adds `mojo format`. Counting format-only failures as fixed, all three pass by the third try, and one of three passes on the first.
2. **A phase takes more than twice its estimate.** Not yet applicable. The estimates below are the baseline.
3. **A release breaks the pinned build twice in a quarter.** None so far.

## Verdict

Decision, 2026-11-16: continue in Mojo. The sample is small: three tasks, and the third-try pass rate clears 60% only narrowly. So the stop conditions are checked again at the end of M2, the first phase with real volume.

## Findings that are not about Mojo

- `vinci ideas` is not read-only. Any C# command runs the tracker sync first, and the sync appends spent, sync and work events. So the differential tests in M2 must give both sides the same post-sync copy. c1's `diff.sh` does this.
- c2 commits once per add. Each commit costs about 150 ms of fsync on this disk. Indexing 93 records took 14 to 16 s, and one commit for the same 93 inserts took 0.1 s. Batch the writes in one transaction in M3.
- c2's index ignores a `(source, id, hash)` it has seen before. So a record that changes from A to B and back to A keeps showing B. The M3 index must compare against the latest hash, not any earlier one.
- Queries took under 1 ms each.
- The Python boundary (sqlite3, json) was easy to use and cheap at one call per query.

## Before M2

- Run `mojo format` automatically after each edit, with an agent hook.
- Install Modular's skills and the docs MCP. The install commands are not verified yet. **To do for you:** find the install commands and check them.
- Show deprecation warnings in the `attempt.sh` summary. They pass the gate silently today.

## Phase estimates

These are working days, agent work plus your review. They are the baseline for stop condition 2.

| Phase | Estimate |
| --- | --- |
| M1 | 2 |
| M2 | 3 |
| M3 | 4 |
| M4 | 3 |
| M5 | 3 |
| M6 | 4 |
| M7 | open |

M7 stays open. All estimates are re-estimated after M6, from the data.

The basis: each task took one agent run of 2.6 to 5.3 minutes, reading the C# sources included. M2 ports about 2,100 lines of C# (App, State, Review, Stats, Journal). Review is the slower part.
