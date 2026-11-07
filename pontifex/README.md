# Pontifex

*Pontifex*: Latin for "bridge-builder" (`pons` + `facere`). Working name; the PyPI names `pontifex`, `pons`, `interpres` and `fabrica` are all taken, so rename before any publication.

A source-to-X compiler/transpiler built on MLIR and LLVM. Source code from several languages is lowered through MLIR into a shared mid-level layer, then emitted either as native code (via LLVM) or as source in another language.

## Status

Phase 0: survey and design only. No compiler code yet. See [DESIGN.md](DESIGN.md) for the architecture, survey findings and open questions, and [CLAUDE.md](CLAUDE.md) for working rules.

## Vision

Ingest code from many languages and emit from the lowered representation. "Any language" is an aspiration, not a design driver. Nothing is designed for it until two or three frontends work end to end.

## Layout

```
python/   orchestration, CLI, test harness, frontend prototypes
cpp/      C++ only where MLIR requires it, or where benchmarks justify it
docs/     notes and benchmark reports
```
