# Calibration c2: SQLite with FTS5 from Mojo 1.2.0

## Result

- Passed on attempt 4 of 4 (`attempts.jsonl`). Attempts 1 to 3 failed: format, then two compile errors. Each failure was fixed in one cycle.
- Attempt 1 failed only on `mojo format`. The test and build had not run yet, so the first real compile error came at attempt 2.
- Attempt wall time per run was 0.16 s, 0.50 s, 0.64 s and 5.84 s. Session wall time was not measured precisely; the attempt log spans about 31 s of attempt runs (03:08:27 to 03:08:58 UTC).
- Backend: Python `sqlite3` through interop. The FTS5 pre-check passed on SQLite 3.53.1. The community `sqlite` package was not used.

## Files

- `index.mojo`: `Hit`, `Index` (`__init__(path)`, `add`, `search`), and the helpers `params` and `quote_query`.
- `load.mojo`: `def main() raises`. Takes a journal path and a db path, indexes lines with a `text` field, then prints the top 3 hits per query argument.
- `test_index.mojo`: six tests on `:memory:` dbs: idempotent re-add, new version replaces old, bm25 ordering on a 5-record fixture, k limit, no hits (including empty query), and FTS5 syntax in queries.
- `journal-index.sqlite`: the db built from the real journal during timing (86 KB). Not committed; delete it when done.
- `attempts.jsonl`: the attempt log, written by `scripts/attempt.sh`. Not edited by hand.

## Timing

Journal: `~/.vinci/journal.jsonl`, 120 lines, 93 with a `text` field, all indexed. Read only.

| Run | Wall time |
| --- | --- |
| Fresh db, index only (3 runs: 15.6 s, 15.2 s, 14.1 s) | about 14 to 16 s |
| Existing db, index only, no new rows (idempotent) | 0.66 s (3 runs: 662, 646, 622 ms) |
| Existing db, index plus 100 queries | 659 to 665 ms, and 633 to 646 ms on repeat |

- **Per query: under 1 ms, not measured precisely.** The 100-query run was within noise of the index-only run. The difference is about 0.1 ms per query, which is below wall-clock resolution with about 0.6 s of process startup. Treat it as an upper bound. An in-process timer would need `perf_counter_ns` in a separate bench file, which was not added here.
- **The fresh-db index cost is per-commit fsync, not Mojo.** `Index.add` commits on every call. On this btrfs disk (`/home`), 93 commits took 13.8 s in plain Python sqlite3, against 0.1 s with one commit for the same 93 inserts. The earlier 2.8 ms result came from a `tmpfs` /tmp and does not apply. The fix belongs in the next design step: batch writes with one commit per load, or a `commit()` method on `Index`. It was not changed here, because the code already passes.
- Query time at 100 queries for a 93-line index is negligible. Startup and the commit cost dominate.

## Python boundary and its cost

Interop is used at the edge (`load.mojo`) and inside `Index`:

- **Per `add`**: seven `PythonObject(...)` conversions, a `params` list built with seven `append` calls, one `execute`, `rowcount`, `lastrowid`, a second `execute` for FTS, and `commit`. That is about 14 Python calls per add. Not measured in isolation.
- **Per `search`**: one `execute(...).fetchall()` and then four conversions per returned row (`String` x3, `Float64`). With k=3 that is about 14 Python-side operations. Not measured in isolation.
- **Per journal line in `load.mojo`**: `strip`, `json.loads`, `get` x3, `hashlib.sha256(encode).hexdigest`, and `String` conversions. About 10 calls per line.
- **Edge interop, noted per the task**: `json` and `hashlib` are read from Python. Mojo has no JSON module here, and `hashlib` gives a stable digest; Mojo's `hash` is randomized per process.
- The boundary is not in a tight loop. Its cost is dominated by the commit above, not by call overhead.

## Mojo gotchas hit

Each line is a mistake made, and a snippet that works on 1.2.0.

1. **`mojo format` wants parentheses around a multi-line string concatenation inside a call argument.** It rewrites this, and attempt 1 failed because the file was not formatted.
   ```mojo
   var cur = self.conn.execute(
       ("INSERT OR IGNORE INTO records" " (source, id) VALUES (?, ?)"),
       params(PythonObject(source), PythonObject(id)),
   )
   ```
2. **`Int(PythonObject)` does not compile.** The error is "no matching function in initialization", with a long list of SIMD candidates that does not mention `PythonObject`. Convert to `String` and keep the value as a string, or compare against a Python value.
   ```mojo
   var lineno = String(pair[0])            # not: Int(pair[0])
   if Bool(cur.rowcount == 1):             # not: Int(cur.rowcount) == 1
       ...
   ```
3. **`len()` on a `String` is rejected.** The error text says to use `byte_length()`, `codepoints()` or `graphemes()`. `len(List)` is fine.
   ```mojo
   if word.byte_length() == 0:             # not: len(word) == 0
       continue
   ```
4. **`Float64(PythonObject)` was not tested directly.** `Float64(String(row[3]))` compiled, and the bm25 score printed correctly from the real journal. Use it instead of a direct `Float64(...)` on the object.
5. **Passing a Python list as sqlite3 parameters.** `Python.list()` plus `append` works. A `params(*values: PythonObject)` helper wraps it so call sites stay readable.
   ```mojo
   def params(*values: PythonObject) raises -> PythonObject:
       var out = Python.list()
       for v in values:
           out.append(v)
       return out
   ```
6. **Iterating Python results works**, including `for pair in builtins.enumerate(lines, 1)`, indexing a tuple with `pair[0]`, and `for row in rows` from `fetchall()`.
7. **Comparing against Python `None` works.** `text == Python.none()` compiled and skipped the lines without `text`.
8. **Not tested**: keyword arguments on Python calls (`open(..., encoding=...)`). I avoided them and used the default encoding, which is UTF-8 on this box. `PythonObject` implicit conversions from `String` and literals were not relied on; every call used `PythonObject(...)` explicitly.

## Test notes

- No test was weakened, and no test failed on its own logic. Every assertion passed on the first run of the test binary.
- The bm25 ordering test asserts that the top hit is `a` or `e`, and that `c` (long body, one occurrence) is last. These were reasoned from the bm25 formula, not computed, and they passed. Three of the four hits are asserted by score direction only. The exact bm25 values are not checked.
- The FTS-syntax test asserts that no query raises, and that a query of a bare `NOT` finds nothing. It does not assert hits for `sqlite-fts` or `title:sqlite`, because quoting makes those phrase queries and the outcome depends on tokenization.

## Design notes

- Records keep every version in `records`, keyed by `UNIQUE(source, id, hash)`. The FTS table holds one row per inserted version, keyed by the same sequence number. Search joins to `MAX(seq)` per `(source, id)`, so only the newest version of each record is returned.
- `quote_query` quotes each whitespace-separated word as an FTS5 phrase, so the query is an implicit AND. Double quotes inside words are doubled. There is no OR and no prefix search.
- `score` is SQLite's bm25 value, not negated. It is lower for better matches, and results are ordered ascending.
- `hash` is the SHA-256 of the stripped line. The id is the 1-based line number, which counts blank lines too.

## What was missing or painful

- **No offline Mojo stdlib reference.** `std.mojoc` in the venv is a compiled blob with no readable API. The `std.python` surface had to be inferred from compiler errors, one error per attempt.
- **Format is strict and runs first.** Every edit needs `mojo format` before the attempt. That is one extra cycle per formatting change.
- **Error messages list dead candidates.** The `Int(PythonObject)` error lists about 15 SIMD constructors, which hides the real reason.
- **Per-commit cost is the largest measured problem.** It is a design issue, not a Mojo issue, and it would also hit the C# version.
- **No stable hash in the standard library here** that was obvious to me. `hashlib` through interop was the simplest choice.
- The plan's `Index` cannot be exercised for concurrency or reopening a db yet; no test covers a second `Index` on the same file.
