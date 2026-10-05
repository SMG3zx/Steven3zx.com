# source-build-selection-is-deterministic-and-fails-closed

## Evidence trail

`internal/domain/builds/strategy.go` detects Rust, TinyGo, C/WASI, Python, JavaScript, .NET, and prebuilt strategies from source markers. `source.go` validates source metadata. The root image installs several toolchains and the worker records build status in PostgreSQL.

## Failure scenario

A source tree is incomplete, ambiguous, or interrupted during build. The same input should not randomly select different strategies, and a failed build must not publish a success artifact or leave a recoverable state indistinguishable from success.

## Local implementation

`internal/domain/builds/strategy_test.go` now uses deterministic temporary fixtures for prebuilt WASM, Go, Rust, C/WASI, Python, JavaScript, and unknown sources, and checks repeated detection returns the same strategy. The missing-artifact workload verifies a separate publication/consumption failure boundary.

## Instrumentation status

Strategy selection is covered by deterministic local tests. A Docker-backed malformed-source workload and dedicated strategy SDK markers remain open.

## Investigation Log

- 2026-09-26: inspected strategy detection, source validation, build lifecycle, and toolchain declarations. The repository does not contain a dedicated Antithesis fixture source tree.
- 2026-09-26: added fixture coverage for supported strategy markers and deterministic repeated detection.

## Open Questions

`(needs human input)` Select the smallest supported fixture language for the first bootstrap workload.
