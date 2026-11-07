# Languages and pipelines

## Top 10 (TIOBE, November 2026)

Source: [tiobe.com/tiobe-index](https://www.tiobe.com/tiobe-index/), fetched 2026-11-07 (page content was summarized by a tool, so recheck the table before relying on exact ratings).

| # | Language | Rating | Fit for the shared layer today |
|---|---|---|---|
| 1 | Python | 20.86% | Subset done (ints, arrays, `for range`, `if`). Dynamic features need types first. |
| 2 | C | 11.48% | Structured via ClangIR, but no upstream lowering to `scf`/`memref` in LLVM 23.1.5 (see `spike.md`). Needs our own lowering or a C subset frontend. |
| 3 | Java | 8.89% | Imperative core fits. Objects, generics, exceptions need shared-layer features that do not exist yet. |
| 4 | C++ | 8.35% | Same as C plus templates, classes, RAII, exceptions. Hardest to translate faithfully. |
| 5 | C# | 3.39% | Like Java. |
| 6 | JavaScript | 2.42% | Dynamic. Needs a typed subset (or TypeScript) to start. |
| 7 | R | 2.27% | Vectorized. Maps better to `tensor`/`linalg` than to loops, which suits the detection pipeline. |
| 8 | SQL | 1.91% | Declarative and relational, not imperative. Needs its own dialect, not the loop/array layer. |
| 9 | Visual Basic | 1.88% | Imperative core fits. Legacy runtime and forms do not. |
| 10 | Go | 1.69% | Imperative core fits well. Goroutines and interfaces need new features. |

Caveats on the list:
- Indexes disagree. TIOBE measures search activity, not usage. The 2026 Stack Overflow survey puts JavaScript first ([results](https://stackoverflow.blog/2026/10/06/the-results-of-the-2026-developer-survey-are-here/)), and GitHub's Octoverse reports TypeScript at #1 ([post](https://github.blog/2026-01-01-state-of-the-octoverse-top-programming-languages/), headline only, table not verified).
- An October 2026 TIOBE report said Rust held #10 ([TechRepublic](https://www.techrepublic.com/article/news-tiobe-october-2026-java-closes-cpp-gap/), page returned 403, headline and snippet only). The fetched November table shows Go at #10 and no Rust. Unresolved.

## Pipelines

A pipeline is any (frontend, emitter) pair from the registry in `python/src/polyjuice/languages.py`. N frontends and M emitters give N x M pipelines without N x M code.

| | Emitter: c | python | java | ... |
|---|---|---|---|---|
| Frontend: python | done | next | | |
| c | | | | |

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
