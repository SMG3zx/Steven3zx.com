# C3: SIMD cosine top-k scan (M0 calibration)

## Outcome

- **Attempts to first pass: 2 (not first try).** Attempt 1 failed on the format check only: `scan_scalar`'s signature was wrapped over three lines and `mojo format` wants it on one line. Fixed in one edit, passed on attempt 2. Build and tests passed on the first try, so the only first-try failure was formatting.
- **Passed by the third try: yes** (attempt 2). Log: `attempts.jsonl`, untouched by hand.
- **Wall time:** 2026-11-17 03:07:52 to 03:08:47 UTC, about 55 s for the whole task, including the Python benchmark run. Most of it was writing the four files. Attempt runs took 0.2 s and 3.4 s.
- Not committed.

## Benchmark

Built with `mojo build` (optimised), run on this box. N = 100_000, D = 768, k = 10, 20 queries, random unit rows from xorshift64* seed 42.

| Kernel | Median ms per query | vs Python |
| --- | --- | --- |
| Python (pure loops, N = 10_000, scaled x10 to N = 100_000) | 1718 (measured 171.8 at N = 10_000) | 1x |
| Mojo scalar (plain loop, `List` indexing) | 57.2 | 30x |
| Mojo SIMD (`WIDTH` lanes, remainder loop) | 5.5 | 311x |

- SIMD over Mojo scalar: **10.4x**.
- Top-k indices agreed between scalar and SIMD on every query.
- The plan's comparison against C# `TensorPrimitives` is **deferred to M3**, as the task says. These numbers are not compared to it.
- Caveats: the Python figure is a linear scale from 10k rows, so it ignores cache effects. Only one run, no warm-up. Timings are on a shared desktop box.

## Design

- `cosine.mojo`: `normalize` (in place, zero vector stays zero), `Store` (one flat `List[Float32]` of N x D, normalised on insert), `TopK` (sorted insertion, k small, ties keep the earlier row), `scan_scalar`, `scan_simd`. Both scans raise on a query whose length is not D.
- Zero query, defined and tested: normalises to zero, every score is 0.0, and the first k rows come back in order.
- `test_cosine.mojo`: scalar and SIMD agree at D = 768 and D = 13, k > N, zero query, identical vector ranks first at about 1.0, and seeded determinism. The RNG is a local xorshift so the tests do not depend on a stdlib random module.
- Top-k uses insertion into a sorted list of length k, not a heap or a full sort. It is O(N x k) worst case but cheap here because most rows fail the `score <= last` check.

## Mojo gotchas hit

1. **Signature line wrapping.** `mojo format` joins a signature onto one line when it fits in 80 columns, and the attempt fails on the diff:
   ```mojo
   def scan_scalar(store: Store, query: List[Float32], k: Int) raises -> List[Hit]:
   ```
2. **`UnsafePointer.load` is deprecated** (warning, not an error, in build and tests). The compiler says to use `unsafe_load`. I did not change it, so the snippet below is the compiler's suggestion, not a tested replacement:
   ```mojo
   acc += dp.unsafe_load[width=WIDTH](base + j) * qp.unsafe_load[width=WIDTH](j)
   ```
3. **Positional pointer indexing is deprecated.** The compiler says to use `unsafe_offset=` (form not tested here):
   ```mojo
   score += dp[base + j] * qp[j]   # warning: positional __getitem__ is deprecated
   ```
4. **Docstring summary must start with a capital letter** (warning). `"""xorshift64*: ..."""` fails; write `"""Xorshift64*: ..."""`.
5. **`simd_width_of` import.** The docs page I fetched was an index only. I used `from std.sys import simd_width_of`, and it compiled.
6. **`attempt.sh` `first_error` can point at source, not the error.** The first attempt's logged `first_error` was `raise Error("scan: query length ...")`, a source line that matched `Error`, not the format message. The format output is the real signal, so read the log, not just the field.
7. **`mojo build` warnings echo through `mojo run` too** (imported modules' warnings print in the test run), so noisy output is normal here.

## Missing or painful

- **The stdlib source is not readable locally.** `.venv/.../modular/lib/mojo/std.mojoc` is a binary with no Mojo sources or docs, so the "look it up in the venv" route from the task does not work. Signatures had to come from guesses plus the compiler.
- **The Mojo docs fetch was partial.** `docs.modular.com` redirects to `mojolang.org`, and the `sys.info` index page had no signatures, no `perf_counter_ns` (`std.time`), no `UnsafePointer.load`, and no `List` methods. This is the same gap the plan's docs MCP and Modular skills are meant to close. They were not installed for this run, so this measurement is closer to the "no docs" condition.
- **`mojo test` does not exist**, so tests are `main()` programs, as the gotchas doc says. Fine, but `assert_almost_equal` is not used because I did not verify its signature; I used `assert_true(abs(a - b) < 1e-4)`.
- **Warnings are not failures.** Deprecated APIs pass attempt.sh, so a deprecated API will not be caught by the gate. Worth a hook or a gotchas line.
- **The benchmark builds its data in a Mojo loop** (77M floats, about 300 MB, one `insert` per row). It works, but setup dominates the run time and is not timed.

## Files

- `calibration/c3-simd-cosine/cosine.mojo`
- `calibration/c3-simd-cosine/test_cosine.mojo`
- `calibration/c3-simd-cosine/bench.mojo`
- `calibration/c3-simd-cosine/bench_py.py`
- `calibration/c3-simd-cosine/attempts.jsonl` (written by attempt.sh)
- `calibration/c3-simd-cosine/notes.md` (this file)
