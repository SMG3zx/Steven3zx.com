# Calibration c1: journal state in Mojo

## Result

- **Attempts to pass:** 2 (attempt 1 failed on one test-side error; attempt 2 passed format, build and all tests). Both are logged in `attempts.jsonl`.
- **Wall time:** from the start stamp (2026-11-17T03:09:27Z, taken after reading the C# sources and before writing code) to the last diff run (about 03:11:13Z): about 2 minutes by the system clock. Attempt 1 ran at 03:10:17Z and attempt 2 at 03:10:26Z.
- **diff.sh:** matches the C# oracle on both journals.
  - Real journal (copy of `~/.vinci/journal.jsonl`): 19 ideas, identical.
  - `fixture.jsonl`: 6 ideas, identical. The C# output also matched the expectations I wrote by hand for stages, touch and titles.
- **Scope:** only `ideas` was ported. Kinds that change neither id, stage, touch nor title are read and skipped (`tend`, `work`, `home`, `sync`, `review`, `bookmark`, `log`, ...). The fixture covers `stage` (including `Incubate` with a value), `ship`, `retire`, `spent` (integer, fractional and the 60-minute boundary) and `tend`.

## Finding: `vinci ideas` is not read-only

The task said `ideas` should only read. It does not. `App.Run` calls `Tracker.Sync` before every command, and that appended to the copy on the first oracle run:

```
{"kind":"spent","id":"i1","value":5.11,"where":"window"}
{"kind":"sync"}
{"kind":"work","id":"i1","text":"Set up vinci mark 2's Mojo toolchain for M0","where":"<commit>"}
```

Consequence for the harness: the oracle's output is a fold over the journal after that append. `oracle.sh` now leaves the post-run copy in place, and `diff.sh` gives the Mojo program that same copy. Before this change the first diff failed on the appended `spent` (i1 was off by 5 minutes). The first `diff.sh` run stopped at the new guard in `oracle.sh` before any comparison ran. The harness changed, not the code under test, so this does not count as an attempt.

## Deviations from the rules

- **Formatting before attempt 1.** I ran `uv run mojo format` on the five task `.mojo` files before attempt 1, because attempt.sh rejects unformatted sources. This is formatting, not a build or test, but attempt 1 therefore did not test the formatter path. The format step passed on attempt 1 and 2.
- **Reading the stdlib.** The installed Mojo 1.2.0 ships only the compiled `std.mojoc`, not source, so I could not check API names against stdlib source. I used names from memory and let the compiler check them. The Modular skills and the Mojo docs were not available to me in this session, so the first-try result below is without them.
- **Debugging after attempt 1.** Allowed by the rules. Each fix cycle was logged with attempt.sh (only one cycle was needed).

## Gotchas

1. **Copying a non-implicitly-copyable struct out of a list fails.** `var idea = state.ideas[i]` gives "value of type 'Idea' cannot be implicitly copied, it does not conform to 'ImplicitlyCopyable'". Use an explicit copy:
   ```mojo
   var idea = state.ideas[i].copy()
   ```
2. **JSON escapes inside Mojo string literals must be doubled.** Mojo decodes `\"` and `\u...` in a literal, so a JSON escape must be written with a doubled backslash to reach the parser intact:
   ```mojo
   lines.append('{"kind":"idea","id":"i3","text":"delta \\"quoted\\""}')
   lines.append('{"kind":"idea","id":"i2","text":"beta \\u2265 gamma"}')  # JSON text: ≥
   ```
   Both forms are covered by `test_string_escapes`, which passed. The single-backslash `≥` is what the Mojo literal decodes to, which is the expected title value.

Nothing else failed to compile or run. The following worked as first written, which I did not expect: `comptime` constants, `@fieldwise_init` with `Copyable, Movable`, `String(unsafe_from_utf8=...)`, `Path(...).read_text()` and `.exists()`, `argv()`, `Int(Float64)` and `String.byte_length()`.

## Missing or painful

- **No readable stdlib.** Only compiled `std.mojoc` ships, so there is no source to read for API names. Every name was a guess that the compiler then confirmed.
- **Silent behaviour differences are the risk.** The output matched only because the Mojo side reads the same bytes the C# folded. A reader that silently ignored some field would show up only as a diff. The only parts that are easy to get subtly wrong are the number rounding (`TimeSpan.FromMinutes` rounds to ticks; Mojo uses `Int(minutes * 6e8 + 0.5)`) and span truncation. The fixture covers `59.9999` and `0.5 + 0.5` for these.
- **Test duplication.** attempt.sh runs tests with an absolute path and the program's cwd is the Mojo root, so `test_state.mojo` cannot read `fixture.jsonl` by a relative path. Its fixture lines are duplicated in memory, and nothing checks that they match `fixture.jsonl` except that both match the oracle separately.

## Files

- `format.mojo`: ticks, `format_span`, `pad_right`
- `journal.mojo`: flat JSON line parser (strings with escapes, surrogate pairs, numbers, skipped values), `read_events`
- `state.mojo`: stage codes and names, `Idea`, `State.apply`, `idea_line`
- `ideas.mojo`: `main`, takes the journal path as `argv[1]`
- `test_state.mojo`: span, fixture stages and touch, the full line, escapes
- `fixture.jsonl`: hand-written journal (6 ideas; advance, ship, tend, retire, spent)
- `oracle.sh`: runs the C# `vinci ideas` on a copy in `/tmp/c1home` and strips the age and flow columns
- `diff.sh`: builds `ideas`, diffs against the oracle on the real journal copy and on the fixture
- `attempts.jsonl`: written by attempt.sh only
- `build/`: diff.sh output (ignored by git)
