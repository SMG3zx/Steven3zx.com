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

## Strings

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

## Formatting

- **`mojo format` rewrites files in place and has no check mode.** To check, format a copy and diff it. `scripts/fmt-check.sh` does this.
  ```sh
  cp -R src "$tmp/" && uv run mojo format "$tmp/src" && diff -ru src "$tmp/src"
  ```
- **The default line length is 80.** `mojo format` takes `-l`/`--line-length`; it was not checked whether it reads any config file.
- **`mojo format` joins a signature onto one line when it fits.** A signature wrapped over several lines fails the format check, so write it on one line.
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
