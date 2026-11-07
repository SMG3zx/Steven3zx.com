# PolyJuice

Named for the Polyjuice Potion: take on the form of someone else while staying yourself. Working name; `polyjuice` is already taken on PyPI, so pick a distribution name before any publication.

A source-to-source compiler built on MLIR and LLVM. Source code from one language is lowered into MLIR, analysed to detect domain patterns, optimized with domain-specific passes, progressively lowered, and emitted as source in a target language.

Linux only for now. Source-to-source only for now; native executables are out of scope.

## Status

Phase 0: survey and design only. No compiler code yet. See [DESIGN.md](DESIGN.md) for the architecture, survey findings and open questions, and [CLAUDE.md](CLAUDE.md) for working rules.

## Vision

Ingest code from many languages and emit from the lowered representation. "Any language" is an aspiration, not a design driver. Nothing is designed for it until two or three frontends work end to end.

## Try it

Requires the local LLVM build from `scripts/setup-llvm.sh` and `uv`.

```
cd python
uv run pytest            # differential tests: Python original vs compiled C
```

Supported Python subset (v0): `int` (32-bit, wraparound) and `Array[int, N]` parameters; assignment, `+ - *`, comparisons, array load/store, `for i in range(a[, b])`, `if/else`, one trailing `return`. Anything else raises `UnsupportedSyntax`.

## Layout

```
python/   orchestration, CLI, test harness, frontend prototypes
cpp/      C++ only where MLIR requires it, or where benchmarks justify it
docs/     notes and benchmark reports
```
