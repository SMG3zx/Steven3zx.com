# Languages and pipelines

## Status of the 13 languages (2026-11-07)

All 13 have a **frontend** (source -> shared layer). Only **C** and **Python** exist as emitters (targets), so
13 x 2 = 26 pipelines run today. `polyjuice --list` prints them.

Every frontend is a thin mapper from a tree-sitter parse tree (or Python's `ast`) onto the neutral AST in
`nast.py`; `lower.py` does everything else once. Each is checked with the same nine canonical programs
(`python/tests/programs/canon.*`): the original program, the emitted C, the emitted Python and the shared-layer
IR run through LLVM must all agree with a Python reference.

| Language | Parsed by | Original run in the tests | Numbers | Notes |
|---|---|---|---|---|
| Python | `ast` | yes | `int` = i32, `float` | annotations required; `//` `%` floor like Python |
| C | tree-sitter-c | gcc | `int`, `double` | `T a[N]` array parameters |
| C++ | tree-sitter-cpp | g++ | as C | `static_cast`, `using`; no classes/templates |
| Go | tree-sitter-go | `go run` | `int`/`int32` = i32, `float64` | `*[N]T` and read-only `[N]T`; `:=` infers |
| Java | tree-sitter-java | javac + java | `int`, `double` | static methods; arrays are unsized so refused |
| C# | tree-sitter-c-sharp | **no** (runtime only, no SDK) | `int`, `double` | static methods; arrays refused |
| Rust | tree-sitter-rust | rustc | `i32`, `f64` | tail expressions are returns; `&[T; N]` arrays |
| JavaScript | tree-sitter-javascript | node | every number is `f64` | counters are floats, so `for` becomes `while`; `%` is `fmod` |
| TypeScript | tree-sitter-typescript | node (type stripping) | `number` = `f64` | `number`/`void` annotations only |
| Lua | tree-sitter-lua | lua | `integer` = i32, `number` = `f64` | types from `---@param` / `---@return` comments |
| PHP | tree-sitter-php | **no** (no php binary) | `int` = i32, `float` | type hints required; first assignment declares; `/` on ints refused |
| Kotlin | tree-sitter-kotlin | **no** (no kotlinc) | `Int`, `Double` | expression bodies; `0 until n`, `0..n` |
| PowerShell | tree-sitter-powershell | **no** (no pwsh) | `[int]` = i32, `[double]` | `[OutputType([int])]` gives the result type; calls are commands |

"No original run" means the sample program is only checked against the Python reference through the other
paths; a mistake in how the sample is written in that language would not be caught by a real compiler.

### What every frontend refuses (with a line number)

Strings, booleans as values, collections and objects, closures, `break`/`continue`, `switch`/`match`, ternary
expressions, bitwise operators, `long`/`float`/`byte`-style types, and anything language-specific outside the
subset above. Arrays exist only where the language can give them a fixed size (C, C++, Go, Rust, Python).

### Semantic compromises to know about

- `int` is 32-bit and wraps in the shared layer. Python, Go, PHP and Lua integers are wider in the source
  language; C leaves signed overflow undefined; PowerShell promotes to double. Programs that stay in range agree.
- `int / int`: C-family and Go/Rust/Java/Kotlin truncate (`div`), Python and Lua give a float (`/`), PHP and
  PowerShell return an int *or* a float depending on the values, so those two refuse it.
- `//` and `%`: floor semantics for Python and Lua, truncation for the C family, `fmod` for JavaScript.
- Early `return` inside a loop is supported; loops containing one are `while` loops in the output.

Suggested next steps: emitters for the other languages (each is one syntax table plus any idiom hooks), dynamic
arrays with a length (the shared-layer rung that unblocks Java, C#, Kotlin, JavaScript, TypeScript, Lua, PHP and
PowerShell arrays), strings, structs, then the type-inference stage for fully dynamic code.

### Reference: TIOBE top 10, November 2026

Source: [tiobe.com/tiobe-index](https://www.tiobe.com/tiobe-index/), fetched 2026-11-07 (summarized by a tool, so recheck exact ratings): Python 20.86%, C 11.48%, Java 8.89%, C++ 8.35%, C# 3.39%, JavaScript 2.42%, R 2.27%, SQL 1.91%, Visual Basic 1.88%, Go 1.69%. An October 2026 report said Rust held #10; the November table does not show it (unresolved). The [2026 Stack Overflow survey](https://stackoverflow.blog/2026/10/06/the-results-of-the-2026-developer-survey-are-here/) puts JavaScript first and GitHub's Octoverse reports TypeScript at #1 ([post](https://github.blog/2026-01-01-state-of-the-octoverse-top-programming-languages/), headline only). The owner's list was not taken from one index. R, SQL and Visual Basic are not on it.

## Pipelines

A pipeline is any (frontend, emitter) pair from the registry in `python/src/polyjuice/languages.py`. N frontends and M emitters give N x M pipelines without N x M code.

Implemented: every one of the 13 languages -> `c` and `python`. The other 11 target columns are open.

`polyjuice --list` prints the registered frontends, emitters and pipelines. `--from` is inferred from the file extension.

## The real constraint: the shared layer, not the language count

Each language costs one frontend and one emitter, but only for the constructs the shared layer can express. Today that is `int`, fixed arrays, `for`, `if`, calls-free functions. Supporting more of each language means widening the shared layer once, which then benefits every language. Proposed ladder, in order:

1. Done: i32, fixed arrays, `for range`, `if/else`, functions.
2. Mostly done: f64 with explicit `float()`/`int()` casts, `while`, calls between functions (including recursion and calls used only for effect). Still missing: bool values as variables, `break`/`continue`, integer `/`, `//` and `%` (Python and C disagree on negatives), `range()` with a step, strings.
3. Structs/records.
4. Strings.
5. Dynamic arrays and heap data (ownership/GC policy needed).
6. Exceptions.
7. Closures, classes, generics.

A faithful "any language to any language" translator needs all seven, and rungs 5 to 7 are the open research question flagged in `DESIGN.md` section 2. Until then each frontend accepts a restricted subset and raises `UnsupportedSyntax` for the rest.

Frontend parser candidates per language (unverified, check before committing): tree-sitter grammars exist for most of these languages; Python, Go and C# ship their own parsers in their toolchains; C and C++ have ClangIR.
