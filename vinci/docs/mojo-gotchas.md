# Mojo gotchas

Agents read this first. Each line is a mistake an agent made, or would make, twice. Mojo is pinned at 1.2.0 (`vinci/mojo/pyproject.toml`). Check the snippet against the pin before trusting older examples.

## Syntax

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

## Testing

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

## Formatting

- **`mojo format` rewrites files in place and has no check mode.** To check, format a copy and diff it. `scripts/fmt-check.sh` does this.
  ```sh
  cp -R src "$tmp/" && uv run mojo format "$tmp/src" && diff -ru src "$tmp/src"
  ```
- **The default line length is 80.** `mojo format` takes `-l`/`--line-length`; it was not checked whether it reads any config file.

## Build and run

- **`mojo build` takes one file and needs a `main`.** Library files compile when a test imports them, so build only programs.
  ```sh
  uv run mojo build -I src src/hello.mojo -o build/hello
  ```
- **`mojo file.mojo` is shorthand for `mojo run file.mojo`.** The CLI help says so.

## uv and PyPI

- **Pin `mojo` exactly, with `==`.** The PyPI package pulls `mojo-compiler` and `mojo-compiler-mojo-libs` at the same version. Pinning the wrapper alone is not enough, so use `uv add "mojo==1.2.0"`.
- **`uv add` of a missing version fails with "no solution found".** Ask PyPI for the list instead, so you do not guess: `curl -s https://pypi.org/pypi/mojo/json | jq -r '.releases | keys[]'`.
- **Pin the Python version in `.python-version`.** This project uses 3.13 (`uv python pin 3.13`).
- **A stray `VIRTUAL_ENV` makes uv warn on every command.** The Makefile runs `unexport VIRTUAL_ENV`. Run `unset VIRTUAL_ENV` in your own shell if you see the warning.

## Pyright and ruff

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
