# Languages and pipelines

## Target languages (chosen by the owner, 2026-11-07)

The owner's list has 13 entries: JavaScript, Python, TypeScript, Java, C#, PowerShell, C++, C, Rust, PHP, Go, Kotlin, Lua. Every one is both a source and a target eventually. Status: only Python (source) and C (target) exist.

Grouped by what a frontend must do, since that decides the cost:

| Group | Languages | What it takes |
|---|---|---|
| Static, imperative | C, C++, Go, Rust, Java, C#, Kotlin, TypeScript | Types map directly to MLIR types. Cheapest to start. Objects, generics, exceptions, ownership come later via the shared-layer ladder. |
| Dynamic | Python, JavaScript, PHP, Lua | Values have no declared types. Needs a type-inference or annotation stage, or a boxed value model. Python is done only for an annotated `int`/array subset. |
| Shell | PowerShell | Pipelines of objects, dynamic scoping, cmdlets. Least like the rest; defer. |

Per-language notes (hypotheses, not verified against each language's tooling):
- **C, C++**: ClangIR gives structured IR, but LLVM 23.1.5 has no lowering to `scf`/`memref` (see `spike.md`). Open decision: own C++ lowering pass vs a restricted C subset in Python. C++ adds templates, classes, RAII.
- **Rust**: as a source, ownership and borrowing must be dropped or checked. As a target, idiomatic output needs ownership choices (slices vs arrays, `&mut`), so scalars and arrays first.
- **Java, C#, Kotlin**: close relatives. One C-family object model in the shared layer could serve all three, plus TypeScript's classes.
- **TypeScript**: the typed way into JavaScript. Do TypeScript before JavaScript.
- **Go**: small, regular, statically typed. Good second source after Python.
- **Lua**: tiny and dynamic. Its single data structure (the table) forces the shared layer to have a map/array model.
- **PHP, JavaScript**: dynamic, with implicit coercions that differ from every other language; each needs its own semantics layer before it is trustworthy.

Suggested order (proposal): sources Python (done), Go, C; targets C (done), Python, Go; then the static family (Java, C#, Kotlin, TypeScript, Rust, C++); then the dynamic ones (JavaScript, Lua, PHP) once a type-inference stage exists; PowerShell last.

### Reference: TIOBE top 10, November 2026

Source: [tiobe.com/tiobe-index](https://www.tiobe.com/tiobe-index/), fetched 2026-11-07 (summarized by a tool, so recheck exact ratings): Python 20.86%, C 11.48%, Java 8.89%, C++ 8.35%, C# 3.39%, JavaScript 2.42%, R 2.27%, SQL 1.91%, Visual Basic 1.88%, Go 1.69%. An October 2026 report said Rust held #10; the November table does not show it (unresolved). The [2026 Stack Overflow survey](https://stackoverflow.blog/2026/10/06/the-results-of-the-2026-developer-survey-are-here/) puts JavaScript first and GitHub's Octoverse reports TypeScript at #1 ([post](https://github.blog/2026-01-01-state-of-the-octoverse-top-programming-languages/), headline only). The owner's list was not taken from one index. R, SQL and Visual Basic are not on it.

## Pipelines

A pipeline is any (frontend, emitter) pair from the registry in `python/src/polyjuice/languages.py`. N frontends and M emitters give N x M pipelines without N x M code.

Implemented: `python -> c`, `python -> python`. Every other cell of the 13 x 13 matrix is open.

`polyjuice --list` prints the registered frontends, emitters and pipelines. `--from` is inferred from the file extension.

## The real constraint: the shared layer, not the language count

Each language costs one frontend and one emitter, but only for the constructs the shared layer can express. Today that is `int`, fixed arrays, `for`, `if`, calls-free functions. Supporting more of each language means widening the shared layer once, which then benefits every language. Proposed ladder, in order:

1. Done: i32, fixed arrays, `for range`, `if/else`, functions.
2. Floats, bool, `while`, `break`/`continue`, calls between functions.
3. Structs/records.
4. Strings.
5. Dynamic arrays and heap data (ownership/GC policy needed).
6. Exceptions.
7. Closures, classes, generics.

A faithful "any language to any language" translator needs all seven, and rungs 5 to 7 are the open research question flagged in `DESIGN.md` section 2. Until then each frontend accepts a restricted subset and raises `UnsupportedSyntax` for the rest.

Frontend parser candidates per language (unverified, check before committing): tree-sitter grammars exist for most of these languages; Python, Go and C# ship their own parsers in their toolchains; C and C++ have ClangIR.
