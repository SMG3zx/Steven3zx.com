# Mojo gotchas

Agents read this first. Each line is a mistake an agent made, or would make, twice. Mojo is pinned at 1.2.0 (`vinci/mojo/pyproject.toml`). Check the snippet against the pin before trusting older examples.

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
