# PolyJuice

Named for the Polyjuice Potion: take on the form of someone else while staying yourself. Working name; `polyjuice` is already taken on PyPI, so pick a distribution name before any publication.

A source-to-source compiler built on MLIR and LLVM. Source code from one language is lowered into MLIR, analysed to detect domain patterns, optimized with domain-specific passes, progressively lowered, and emitted as source in a target language.

Linux only for now. Source-to-source only for now; native executables are out of scope.

## Status

Frontends for 13 languages (Python, C, C++, Go, Java, C#, Rust, JavaScript, TypeScript, Lua, PHP, Kotlin, PowerShell) and emitters for C and Python, with differential tests. See [docs/languages.md](docs/languages.md) for what each accepts. See [DESIGN.md](DESIGN.md) for the architecture, survey findings and open questions, and [CLAUDE.md](CLAUDE.md) for working rules.

## Vision

Ingest code from many languages and emit from the lowered representation. "Any language" is an aspiration, not a design driver. Nothing is designed for it until two or three frontends work end to end.

## Try it

Requires the local LLVM build from `scripts/setup-llvm.sh` and `uv`.

```
cd python
uv run pytest                        # differential tests: Python original vs compiled C
uv run polyjuice prog.py --to c|python --out out/  # one file per pipeline stage, prints the result
uv run polyjuice prog.py --verify    # also lowers to LLVM IR under out/verify/
```

Each frontend accepts a subset (ints, floats, fixed-size arrays, loops, ifs, calls, early returns) and raises `UnsupportedSyntax` with a line number for anything else. The source language is inferred from the file extension; `--from` and `--to` override.

## Layout

```
python/   orchestration, CLI, test harness, frontend prototypes
cpp/      C++ only where MLIR requires it, or where benchmarks justify it
docs/     notes and benchmark reports
```
