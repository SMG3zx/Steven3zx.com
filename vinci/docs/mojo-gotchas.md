# Mojo gotchas

Agents read this first. Each line is a mistake an agent made, or would make, twice. Mojo is pinned at 1.2.0 (`vinci/py/pyproject.toml`). Check the snippet against the pin before trusting older examples.

The stdlib ships only as a compiled `std.mojoc`, with no sources. The compiler is the reference: write the name, then read the first error.

## Syntax and types

- **`fn` is removed.** Use `def`. Mojo 1.x rejects `fn` with "'fn' has been removed".
  ```mojo
  def add(a: Int, b: Int) -> Int:
      return a + b
  ```
- **Functions that can fail need `raises`.** Tests raise on failure, so `main` must declare it.
  ```mojo
  def main() raises:
      ...
  ```
- **No implicit conversion from a string literal to `Int`.** Parse explicitly or fix the type.
  ```mojo
  var x: Int = 3        # ok
  # var x: Int = "a"    # error: cannot implicitly convert 'StringLiteral' to 'Int'
  ```
- **Copying a struct out of a `List` needs `.copy()`.** A struct that is not `ImplicitlyCopyable` fails with "cannot be implicitly copied".
  ```mojo
  var idea = state.ideas[i].copy()     # not: var idea = state.ideas[i]
  ```
- **Docstring summaries start with a capital.** A lowercase start is only a warning, so the gate passes it.
  ```mojo
  def total(xs: List[Float32]) -> Float32:
      """Sum the values."""
  ```

- **`ref` is a keyword too.** A variable named `ref` fails with "unexpected token in expression", and `mojo format` says "Cannot parse". Name it `target`, `other` or the like. Hit twice in one build.
  ```mojo
  for r in refs:
      var target = String(r)           # not: var ref = String(r)
  ```
- **A struct built with positional arguments needs `@fieldwise_init`.** Without it the call fails with "no matching function in initialization" and lists a generated `move` or `copy` initializer as the missing argument. Four structs hit this in one build.
  ```mojo
  @fieldwise_init
  struct KindCount(Copyable, Movable):
      var kind: String
      var total: Int
      var current: Int
  ```
- **`where` is a keyword.** A struct field or argument named `where` fails to parse with "Cannot parse" under `mojo format`. Name it `where_`, as `for` is named `for_`. JSON keys keep their own spelling.
  ```mojo
  var where_: Optional[String]     # not: var where: Optional[String]
  ```
- **Passing a struct or an optional into a constructor moves it.** Constructor parameters must be `var`, and each one is moved with `^` or copied with `.copy()`. A borrowed `Optional` also needs `.copy()` before `.or_else(...)`.
  ```mojo
  def __init__(out self, var at: Instant, var refs: Optional[List[String]] = None):
      self.at = at^
      self.refs = refs^

  var refs = e.refs.copy().or_else(List[String]())   # not: e.refs.or_else(...)
  ```
- **`//` on `Int` rounds toward negative infinity.** `-7 // 2` is `-4`, but C# casts such as `(int)TotalMinutes` truncate to `-3`. Use a helper that adds one back for an inexact negative quotient.
  ```mojo
  def truncate_div(a: Int, b: Int) -> Int:
      var q = a // b
      if q < 0 and q * b != a:
          q += 1
      return q
  ```

- **A `Dict` subscript raises.** `d[key]` and `d[key] = v` can raise, so a helper that uses them fails with "cannot call function that may raise in a context that cannot raise". It hit two helpers in one build. Mark the helper `raises`, or test `key in d` first and let the caller decide.
  ```mojo
  def append_to(mut d: Dict[String, List[String]], key: String, value: String) raises:
      if key in d:
          d[key].append(value)
  ```

- **A struct that is not `Copyable` cannot be passed by value twice.** `Instant` and `VectorIndex` hit this. Store the plain values a callee needs (UTC ticks, a date string), or pass the struct borrowed (no `var`) and let the callee read it.
  ```mojo
  var now_utc = settings.now_utc      # an Int, not settings.clock (an Instant)
  ```
- **A result struct built with positional arguments needs `@fieldwise_init` too.** `Recall` and `Assessment` failed the same way as `KindCount` above.

## Strings

- **`strip()` and `split()` give a `StringSpan`, not a `String`.** Passing one to a `String` parameter fails with "cannot be converted from 'StringSpan'". Wrap it.
  ```mojo
  var name = String(ascii_prefix(line, at).strip())    # not: ascii_prefix(line, at).strip()
  ```

- **JSON escapes inside Mojo string literals must be doubled.** Mojo decodes `\"` and `\u` first, so write `\\` to reach the JSON parser intact.
  ```mojo
  lines.append('{"kind":"idea","id":"i3","text":"delta \\"quoted\\""}')
  lines.append('{"kind":"idea","id":"i2","text":"beta \\u2265 gamma"}')  # JSON text: ≥
  ```
- **`len()` on a `String` is rejected.** Mojo strings are UTF-8, so the length is ambiguous. Use `byte_length()`. `len()` on a `List` is fine.
  ```mojo
  if word.byte_length() == 0:          # not: len(word) == 0
      continue
  ```

## Python interop

- **`Int(PythonObject)` does not compile.** The error lists about 15 SIMD constructors and never says PythonObject. Go through `String`, or compare in Python terms.
  ```mojo
  var n = Int(String(obj))             # not: Int(obj)
  if Bool(obj == 1):                   # compare instead of converting
      ...
  ```
- **`Float64(PythonObject)` is not tested directly.** Go through `String`, which compiled and gave the right bm25 score.
  ```mojo
  var score = Float64(String(row[3]))  # not: Float64(row[3])
  ```
- **`Python.list()` works as sqlite3 parameters.** Build it with `append`, and wrap it in a variadic helper so call sites stay short.
  ```mojo
  def params(*values: PythonObject) raises -> PythonObject:
      var out = Python.list()
      for v in values:
          out.append(v)
      return out
  ```
- **Mojo's `hash` changes on every run.** Use `hashlib` through interop for a digest that stays the same.
  ```mojo
  var hashlib = Python.import_module("hashlib")
  var digest = String(hashlib.sha256(PythonObject(text).encode()).hexdigest())
  ```

- **Interop calls raise, so the function calling them must too.** `Python.import_module` in a non-raising `def` fails with "cannot call function that may raise". Mark the function `raises`, or wrap the call in `try:` inside a helper that may swallow the error (a stderr write at exit is the usual case).
  ```mojo
  def stderr_line(text: String):
      try:
          var sys = Python.import_module("sys")
          sys.stderr.write(text + "\n")
      except:
          pass
  ```

- **Each statement commits on its own in Python's sqlite3, and each commit fsyncs.** On this disk that is about 100 to 150 ms per statement. Schema creation ran about 1 s until it sat in one `BEGIN IMMEDIATE` ... `COMMIT`. A run that changes nothing should write nothing: an update with no new lines fsynced its meta rows until that check was added.
  ```mojo
  _ = self.conn.execute("BEGIN IMMEDIATE")   # the schema and a build: one transaction
  # ... CREATE TABLE ..., INSERT ...
  self.conn.commit()
  ```

## Pointers and SIMD

- **`UnsafePointer.load` is deprecated.** Use `unsafe_load`, which builds without a warning.
  ```mojo
  var v = p.unsafe_load[width=W](j)    # not: p.load[width=W](j)
  ```
- **Positional pointer indexing is deprecated.** Use `unsafe_offset=`, which the compiler names in its warning.
  ```mojo
  var x = p[unsafe_offset=i]           # not: p[i]
  ```
- **Import the SIMD width from `std.sys`.** It compiles, and the width is a compile-time value.
  ```mojo
  from std.sys import simd_width_of

  comptime W = simd_width_of[Float32]()
  ```

- **`UnsafePointer` is deprecated in 1.2, and `std.memory` has no `memcpy`.** Use `Pointer`, built from an address, and copy element by element with `unsafe_offset=`. A bytes object's address comes from `ctypes`, and it is valid only while that object is alive.
  ```mojo
  var src = Pointer[Float32, MutAnyOrigin](unsafe_from_address=addr)
  var dst = values.unsafe_ptr()
  for i in range(count):
      dst[unsafe_offset=i] = src[unsafe_offset=i]
  ```

- **`os.environ[key] = value` works; `os.environ.__setitem__(key)` does not.** The synthetic signature needs a `value` argument as well. Hit in a test helper.
  ```mojo
  os.environ[PythonObject("VINCI_MOJO_PY")] = py_dir
  ```
- **Python results are `PythonObject`, not `String`.** `os.path.join` and `os.path.abspath` return one, and a `String` variable will not take it. Wrap the call in `String(...)`.
  ```mojo
  py_dir = String(os.path.join(dirname, PythonObject(".."), PythonObject("py")))
  ```
- **Python's `sqlite3` opens a transaction before the first `INSERT`.** A later `BEGIN IMMEDIATE` then fails with "cannot start a transaction within a transaction", and a raw `COMMIT` fails when nothing is open. In a fixture that writes with `put()` before calling `begin()`, commit with `store.conn.commit()` first. The production paths begin before any write.

## Formatting

- **`mojo format` rewrites files in place and has no check mode.** To check, format a copy and diff it. `scripts/fmt-check.sh` does this.
  ```sh
  cp -R src "$tmp/" && uv run mojo format "$tmp/src" && diff -ru src "$tmp/src"
  ```
- **The default line length is 80.** `mojo format` takes `-l`/`--line-length`; it was not checked whether it reads any config file.
- **`mojo format` joins a signature onto one line when it fits, and wraps one longer than 80 columns.** Either way the check compares against `mojo format`, so run `uv run mojo format <file>` on the file rather than wrapping by hand. A `def` over 80 columns fails `make fmt-check` as written.
  ```mojo
  def add(a: Int, b: Int) -> Int:
  ```
- **`mojo format` joins adjacent string literals into one.** A string concatenated across lines fails the check, and formatting it does not keep the parentheses. Write the literal on one line, then run `uv run mojo format` before the attempt.
  ```mojo
  var cur = conn.execute(
      "INSERT OR IGNORE INTO records (source, id) VALUES (?, ?)",
      PythonObject(1),
  )
  ```

## Tooling

- **There is no `mojo test` in 1.2.0.** `mojo test` prints "no such command 'test'". A test is a program with `def main() raises:` that asserts, run with `mojo run`.
  ```mojo
  from std.testing import assert_equal

  def main() raises:
      assert_equal(1 + 1, 2)
      print("test_x: ok")
  ```
- **The testing module is `std.testing`, not `testing`.** `from testing import ...` fails with "unable to locate module 'testing'".
  ```mojo
  from std.testing import assert_equal   # not: from testing import assert_equal
  ```
- **Run a test with the source directory on the import path.** `-I src` lets `from hello import add` resolve `src/hello.mojo`.
  ```sh
  uv run mojo run -I src tests/test_hello.mojo
  ```
- **A failed assertion exits non-zero.** `mojo run` returns 1 and prints "AssertionError". Check the exit code, not just the output.
- **Set `MODULAR_DEBUG=stack-trace-on-error` for a stack trace.** The default message is only a hint, so `scripts/attempt.sh` skips it when it picks the first error.
- **Warnings do not fail the gate.** Deprecations pass `scripts/attempt.sh` silently, and `mojo run` echoes warnings from imported modules too. Grep the output for them.
  ```sh
  uv run mojo run -I src tests/test_hello.mojo 2>&1 | grep warning
  ```
- **Read the first `error:` line.** The lines after it are often candidate lists that hide the cause.
  ```sh
  uv run mojo build -I src src/hello.mojo -o build/hello 2>&1 | grep -m1 'error:'
  ```
- **`mojo build` takes one file and needs a `main`.** Library files compile when a test imports them, so build only programs.
  ```sh
  uv run mojo build -I src src/hello.mojo -o build/hello
  ```
- **`mojo file.mojo` is shorthand for `mojo run file.mojo`.** The CLI help says so.
- **Pin `mojo` exactly, with `==`.** The PyPI package pulls `mojo-compiler` and `mojo-compiler-mojo-libs` at the same version. Pinning the wrapper alone is not enough, so use `uv add "mojo==1.2.0"`.
- **`uv add` of a missing version fails with "no solution found".** Ask PyPI for the list instead, so you do not guess: `curl -s https://pypi.org/pypi/mojo/json | jq -r '.releases | keys[]'`.
- **Pin the Python version in `.python-version`.** This project uses 3.13 (`uv python pin 3.13`).
- **A stray `VIRTUAL_ENV` makes uv warn on every command.** The Makefile runs `unexport VIRTUAL_ENV`. Run `unset VIRTUAL_ENV` in your own shell if you see the warning.
- **`pyright --strict` is not a CLI flag.** Pyright says "Unexpected option --strict". Set `typeCheckingMode = "strict"` under `[tool.pyright]` and run `pyright py/`.
  ```toml
  [tool.pyright]
  typeCheckingMode = "strict"
  include = ["py"]
  ```
- **Untyped Python fails strict pyright.** An unannotated parameter or return value is an error, so annotate every public function.
  ```python
  def add(a: int, b: int) -> int:
      return a + b
  ```

## Found in M3 step 3a (twice-hit)

- **Format a file after its last edit, not before.** `mojo format` changes a file, so an edit made after it breaks `make fmt-check`, which compares a copy. Hit twice in one step: a one-line fix to `handles.mojo` failed the check, and a test file changed after it was formatted. Run `uv run mojo format <file>` on every touched file as the final step before `make check`.
  ```sh
  uv run mojo format src/handles.mojo tests/test_open.mojo   # last, after every edit
  ```

- **A `json.loads` value needs a cast before strict pyright.** The result is `Any`, so `raw.get(...)` and `len(tokens)` report "partially unknown" under `typeCheckingMode = "strict"`. The fix is to narrow with `isinstance`, then `cast` to the concrete type (as `index_eval.py` does).
  ```python
  reply = cast("dict[str, object]", raw)
  tokens = reply.get("tokens")
  if not isinstance(tokens, list):
      raise TypeError("no tokens list")
  return len(cast("list[object]", tokens))
  ```

- **Check a hand-written expected value against the program before changing the code.** Five expectations in the new tests were wrong on the first run: a grep inside a range (the range limits the result), a fixture line padded with `x` (`line 12xxx`), a JSON quote written as `\"` where `json_string` gives `"`, and two word counts. Each was wrong in the expectation, not the code. Work the value out, then confirm it against the output before deciding the code is wrong.

## Found in M3 step 3b (Python, twice-hit)

- **Ruff flags a bare `except Exception` (BLE001) and an implicit string concatenation inside a list (ISC004).** The ISC004 rule fired twice in one module. Catch the exact errors (the recall, HTTP and parse failures), and wrap each concatenated string in parentheses.
  ```python
  lines = [
      (
          "| config | accuracy |"
          " correct/n |"
      ),
  ]
  ```

## Found in M4a (the chat, twice-hit)

- **`origin` is a reserved word.** It cannot name a function or a variable: `def origin_of` fails with "cannot be used as a function name in this context", and the variables named `origin` and `origins` fail the same way. Hit in the function, then in the variables.
  ```mojo
  def source_of(store: IndexStore, cite: String) raises -> String:   # not: origin_of
      ...
  ```

- **`mojo format` wraps a call that was one line, so a text edit by script misses it.** A script that replaces `pack(found.results, budget, DEFAULT_WHOLE, estimate_counter())` silently matched nothing once the call was wrapped across lines, and the build then failed on the old name. Run `uv run mojo format <file>` first, then grep for the name before a scripted edit, or edit with the compiler's line.
  ```sh
  uv run mojo format src/chat_tools.mojo && grep -n DEFAULT_WHOLE src/chat_tools.mojo
  ```

- **A chat summary line is the tool name and its arguments as compact JSON, not the arguments alone.** A test expectation of `  · open i1` failed twice (two scripts) against the real `  · open {"id":"i1"}`. Copy the expected line from a run, after checking that the run is right.
  ```text
    · open {"id":"i1"}      # not: · open i1
  ```

- **A .NET HTTP client sends a chunked body, with no Content-Length.** A fake server that reads only Content-Length gets an empty body, returns a 500, and the C# chat then prints its error and exits. Read `Transfer-Encoding: chunked` as well (`py/vinci_diff/fake_llm.py`).

- **Python edges return a status word instead of raising into Mojo.** The chat's Python calls return `("ok", text)` or `("error", message)`, and `("line", text)` or `("eof", "")` for standard input, so Mojo reads `String(pair[0]) == "ok"` and never parses a Python exception text. Raising across the boundary gave a message that the Mojo side could not match.
  ```mojo
  var pair = edge.read_line()
  if String(pair[0]) == "eof":
      return None
  return String(pair[1])
  ```

## Found in M4b (the portal, twice-hit)

- **A `BaseHTTPRequestHandler` that overrides `handle_one_request` must set what the base class logs from.** `log_request` reads `self.requestline` and `send_response` reads `self.request_version`. Without them each reply fails with an `AttributeError` inside the handler, and the client gets no answer. Set both on each request, and give the class defaults so error paths find them.
  ```python
  class Handler(BaseHTTPRequestHandler):
      requestline = ""
      request_version = "HTTP/1.1"
  ```

- **Ruff flags `print("")` (FURB105) and `text[1:] if text.startswith(mark) else text` (FURB188).** Both fired twice in the portal's Python. Write `print()` and `text.removeprefix(mark)`.
  ```python
  print()
  return text.removeprefix("\ufeff")
  ```

## Found in M4d (the chat crash, twice-hit)

- **A bytes object's address is valid only while its PythonObject is alive, and a temporary dies after its last use.** `insert_from_address(address_of(row[9]))` took the address from a temporary that was destroyed inside that expression, so the copy read freed memory. The chat died with SIGSEGV on the second recall of 32 of 200 LongMemEval conversations (24 qids). gdb showed the free coming from `PythonObject.__deinit__` at the line that took the address. Pass the object itself into the function that reads it (`insert_blob(matrix, row[9])`), so the caller's reference lives for the whole copy. Never return a raw address from a function that owns the object.
  ```mojo
  insert_blob(out.matrix, row[9])            # not: out.matrix.insert_from_address(address_of(row[9]))
  ```

- **A crash that disappears when you add a debug line is a lifetime bug, not a fixed one.** Keeping one more reference, or printing `row[9]`, hid the crash in several runs. Check that the object is alive for the whole read; do not trust a run that stopped crashing after a change elsewhere.

- **`mojo build -O0 -g` gives the source line of a crash; the default build gives only `main (+0x2839b)`.** Build a debug copy of the sources in the scratchpad and run it under gdb, with `VINCI_MOJO_PY` pointing at `mojo/py` (the binary cannot find the Python edges from another directory). A `free` breakpoint for one address needs `set breakpoint pending on`, because libc is not loaded when the script starts: `break free if $rdi == 0x...`.
  ```sh
  uv run mojo build -I src -O0 -g src/vinci.mojo -o "$scratch/vinci"
  gdb -batch -ex 'set breakpoint pending on' -ex 'break free if $rdi == 0x555556f693e0' -ex run --args "$scratch/vinci" chat
  ```

- **A unit test with synthetic blobs can pass on code that crashes in production.** `test_loaded_rows_are_the_stored_vectors` (463 rows, two loads, a file-backed store) passed on the broken code, so it does not reproduce the freed read. The gate for this bug is the eval repro: `chat_eval --qids` on the qid that crashed, with 0 errors.

- **`mojo run` and a built binary run the chat on the real home unless the eval environment is set.** Set `VINCI_HOME`, `VINCI_ENV_FILE` and `VINCI_CHAT_INDEX` before every manual `vinci chat` run, in the same command. `chat_eval` sets them for each conversation; a bare `./build/vinci chat` writes to `~/.vinci`.

## Found in M5a (the feeds, twice-hit)

- **`read` is a reserved argument convention.** A parameter named `read` fails with "'read' was removed; use 'imm'", and the build stops at the first error, so locals named `read` are not reached yet. Rename both. Hit in the feed reader and its caller.
  ```mojo
  def apply_read(mut store: IndexStore, got: FeedRead) raises:   # not: read: FeedRead
      ...
  ```

- **`strip()` is a `StringSpan` on return too.** A `def` that returns `String` fails the same way as a parameter does. Hit twice in one test helper.
  ```mojo
  return String(String(done.stdout).strip())    # not: return String(done.stdout).strip()
  ```

- **Python keyword arguments work from Mojo.** `subprocess.run(cmd, capture_output=True, text=True, check=False)` and `os.makedirs(path, 511, True)` both compiled and ran. Positional form is still the safer choice for `makedirs`, since its second parameter is the mode.

- **A check on a stored status counts ended versions too.** A version ended by a newer one keeps `status = 'active'`, so `WHERE status = 'active'` counts history. A current count needs `valid_until IS NULL` as well. The first expectation was off by the number of versions ended.
  ```sql
  SELECT COUNT(*) FROM facts WHERE source = 'git' AND status = 'active' AND valid_until IS NULL
  ```

- **Values from `tomllib` and `json` are `object` to pyright strict.** `isinstance` narrows to an unknown `list` or `dict`, so the next line reports "partially unknown". Narrow, then `cast`, and build the typed dictionaries in local variables first.
  ```python
  items = cast("list[object]", value)
  return [str(v) for v in items]
  ```

- **A type check raises `TypeError`, not `ValueError`.** Ruff's TRY004 flags a `ValueError` after an `isinstance` test. A bad value that is the wrong type is a `TypeError`.
  ```python
  if not isinstance(value, dict):
      raise TypeError(f"[{name}] must be a table")
  ```

- **Check the harness's arguments before a run.** `vinci_diff.chat_diff` takes a script filter as its first argument, so `--candidate` was read as a filter and nothing ran ("no scripts match"). It uses `py/vinci` with no arguments.

- **A binary outside `mojo/build` cannot find the Python edges.** `vinci` looks for `../py` beside itself, so a copy in a scratch directory fails with "No module named 'vinci_edge'". Set `VINCI_MOJO_PY=<checkout>/mojo/py` in the same command.
  ```sh
  VINCI_MOJO_PY=/home/steven3zx/Steven3zx.com/vinci/mojo/py "$scratch/vinci" feed run journal
  ```

## Found in M5a-2 (recall by source, twice-hit)

- **A field cannot be moved out of a value that is returned in pieces.** `return f(...).candidates^`, and a local whose field is moved with `^`, both fail with "field ... destroyed out of the middle of a value". Hit twice in one function. Return a copy, or return the struct and let the caller read the field.
  ```mojo
  var only = lexical_table(store, "fts", match_expr, limit, status, journal)
  return only.candidates.copy()      # not: return only.candidates^
  ```

- **A SQL filter does not change FTS5 scores.** bm25 uses the whole table's word counts, so rows that a `WHERE` removes still move the scores of the rows it keeps. Rows that should not affect the search need their own FTS table. The personal set's default answers changed on about half the questions when feed rows shared the journal's table.

## Found in M5b (the tracker and the activity feed, twice-hit)

- **A harness that changes directory must resolve its binary paths first.** `--candidate ../vinci` passed to `vinci_diff` and to `sync_diff` failed with `FileNotFoundError` at the first step, because each case runs with its own working directory. Resolve the paths in `main` before the first case.
  ```python
  args.candidate = args.candidate.resolve()
  ```

- **A journal records a repository's path, so both binaries must build it at one path.** Giving each side its own folder made every `home` line differ, and the sync diff failed at step 1 with the paths, not the tracker. Build the repositories again at one shared path before each binary, and run the binaries one after the other.

- **A scripted cut from one `def` to the next can delete a `struct` between them.** An edit that took `lexical_from` through to the next `def` removed `struct Settings` (with its constructor) and left the function's old body behind. Anchor the end on a line that is unique, such as the function's own `return out^` with its leading newline (`"\n    return out^\n"`). The shorter `"    return out^\n"` also matches the eight-space guard inside the function.
  ```python
  end = s.index("\n    return out^\n", start) + len("\n    return out^\n")
  ```

- **A `len()` on a string literal is rejected.** `len("2026-11-18/")` fails in a test. Use `String("2026-11-18/").byte_length()`, as the rule for `len` on strings already says.

- **A `struct` or a value moved inside a conditional expression needs the same ownership on both branches.** `merged = merge_ranked(merged, found, limit) if have_any else found^` compiled. The earlier form that moved `merged` in one branch and copied in the other did not.

- **An ActivityWatch duration is a double, and the tick count comes from truncation.** `int(seconds * 10_000_000)` in the edge matches `TimeSpan.FromSeconds`. Rounding to the nearest tick or to milliseconds gives a different `at` on every `spent` fact with a fraction.

- **A bm25 table must hold only the rows it ranks.** The activity feed has its own `fts_activity` table. Loading it into `fts_feeds` would have moved the git and vault scores (bm25 counts every row of the table), so the rule above applies again, one table per source.

- **A deny list is compared in the same case as the text it is matched against.** The window text was folded for matching but the configured terms were not, so `journal.cs` never matched `JOURNAL.CS`. The unit test passed because it passed folded terms. Fold the list once per run and check that the end-to-end count changes.

- **Each Bash call starts with an empty shell variable set.** A build log written to `$S/build.log` after a call that did not set `S` went to `/build.log` and was denied, and the build did not run. Set the scratch path in each command that uses it.

- **A file in the scratch folder shadows the standard library for scripts run from there.** `inspect.py` in the scratch folder made `dataclasses` fail to import when a dump script was run from the same folder. Run scratch scripts from their own subfolder.
- **`recall` writes the embedding cache under `~/.vinci/index` whatever `VINCI_HOME` is.** A scratch recall with `VINCI_HOME` set appended query vectors to the real `embed-cache.sqlite` (the file predates the session, so it was appended to, not created). Set `VINCI_EVAL_EMBED_CACHE=<scratch>/embed-cache.sqlite` in the same command for any scratch recall, as the chat and portal harnesses point `HOME` away from `~/.vinci`.
  ```sh
  VINCI_HOME="$scratch" VINCI_EVAL_EMBED_CACHE="$scratch/embed-cache.sqlite" ./build/vinci recall "word"
  ```

## Found in M5d (fusion weights, twice-hit)

- **A vector label that does not match returns no vectors, and no error.** `index_eval --label qwen3-embedding:4b@gpu` against an index whose vectors are labelled `@gpu` gave the vector route 0 of 15 and the same lexical results, with no warning. Check the label the index stores before a run.
  ```sh
  python3 -c "import sqlite3,sys; print(sqlite3.connect(sys.argv[1]).execute('SELECT label, COUNT(*) FROM vectors GROUP BY label').fetchall())" index.sqlite
  ```

- **A recall whose facts do not have source `journal` returns nothing since M5c.** The default `--sources` is `journal`, so a fact stored under any other source is dropped in SQL. An eval that loads documents with the session id as the source gets `results: []` and abstains on every question. Load them with source `journal`, or pass `--sources` with a name the index holds.

- **An unused first value makes a warning.** `var value = Float64(0)` followed by an assignment in `try` warns "assignment ... was never used". Declare the variable and assign it inside the `try` instead.
  ```mojo
  var value: Float64
  try:
      value = Float64(value_text)
  except:
      raise user_error("weights: " + name + " must be a number")
  ```

- **A recall test that checks an order pins the default weights.** Changing the default changes the order, and the expectation has to be worked out by hand again. The route score divides by the peak, so compute each candidate's score from its fused value, not from the sum of its ranks. The test's expected order changed from f1, f3, f2 to f3, f1, f2 on the new default.

- **Eval harnesses write result files under `~/.vinci/eval/results` unless told otherwise.** `index_eval` writes there unless `--out` is given, and `lme_index_eval` writes to its module-level `RESULTS_DIR`. Pass `--out`, or point the module's paths at a copy before calling its `main()`, as the M5d driver does.

## Transactions, parameters and edges

- **A tuple declared with `comptime` cannot be iterated.** `for p in SOURCES` over `comptime SOURCES = ("a", "b")` fails with "does not implement the '__iter__' method" or "cannot materialize comptime value". Build a `List` in a function instead.
  ```mojo
  def sources() -> List[String]:
      var out = List[String]()
      out.append("src/a.mojo")
      return out^
  ```

- **A helper that calls a mutating method needs `mut` on its parameter.** `def helper(s: Session)` calling `s.record(...)` fails with "invalid use of mutating method on rvalue". A `sed` that adds `mut` misses parameter lists that break across lines, so grep for `s: Session` without the `mut`.
  ```mojo
  def bookmark(mut s: Session, title: String) raises:
  ```

- **A default argument that is moved out needs `var`.** `text: Optional[String] = None` followed by `Event(text=text^)` fails with "cannot transfer out of immutable reference". Declare `var text: Optional[String] = None`.

- **`Python.builtins()` does not exist.** Use `Python.import_module("builtins")`, for example to call `len` on a Python list.

- **Import an edge module only after `edge_module()` has put `mojo/py` on `sys.path`.** An import before it is not found. Call `_ = edge_module()` first, every time, as the other edges do.

- **`Path.read_bytes()` gives a `List[UInt8]`, not a Python object.** Pass the text to Python instead, with `PythonObject(Path(p).read_text()).encode()`, when the file is ASCII. Otherwise let the Python edge read the bytes.

- **`flock` is per open file, so one process can block on itself.** Two descriptors on one lock file wait for each other, even in one process. `vinci_edge/journal_lock.py` keeps one descriptor per journal path and counts the nested takes, so a read inside a transaction does not wait.

- **`HOME` is not the account's home.** `os.path.expanduser("~")` follows `$HOME`, which a test harness or a wrapper can change. A boundary such as "files under home" uses `pwd.getpwuid(os.getuid()).pw_dir` (`reading_edge.account_home`).

## Found in K0a (the goldens and the gate, twice-hit)

- **`mojo format` moves a docstring's closing quotes onto a line of their own when the last text line runs past 80 columns.** `fmt-check` then fails on a file that looked right by hand. Run `uv run mojo format <file>` on the file, or keep each docstring line under 80 columns, so the closing `"""` stays on its text.
  ```mojo
  """The external reads one tracker sync makes. A read is made once per key, so the
  pass that decides and the pass that writes share it."""
  ```

- **`mojo build -o build/vinci` needs `build/` to exist.** In a fresh clone or a `git archive` copy, the compiler stops with "unable to write file ... build does not exist". `make build` creates the folder (`mkdir -p build`); a direct `uv run mojo build` must do the same first.

- **`edge_module()` costs about 20 ms, because it imports the index's HTTP module.** A new edge that needs only its own Python file pays that on every call, and a plain `vinci idea` went from 29 ms to 58 ms. Put the path setup in `edge_path()` (`embed_edge.mojo`), which imports nothing, and import only the module the edge needs. Measure with `PYTHONPROFILEIMPORTTIME=1` on the binary, which shows the imports the embedded interpreter makes.
