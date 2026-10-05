# operation-state-never-regresses

## Evidence trail

`backend/janus-api/internal/domain/operations/lifecycle.go` defines operation lifecycle transitions. `internal/application/usecases/operations/submitter.go` creates and dispatches operations, while `internal/adapters/outbound/postgres/operations.go` persists and queries status. API polling can overlap worker updates and retries.

## Failure scenario

An API request creates an operation, the worker begins it, and a delayed or duplicate transition writes an older state after a terminal transition. The client then observes a regression or a terminal operation that is re-executed.

## Instrumentation status

Partially implemented locally. `internal/domain/operations/lifecycle.go` now calls the Antithesis Go SDK `assert.Always` with the stable message `operation status transitions never regress`, including operation ID, kind, previous status, and next status. The assertion is evaluated after each successful domain transition. Database-level concurrent polling and stale-write behavior still require workload coverage.

## Investigation Log

- 2026-09-26: inspected lifecycle, submitter, repository interfaces, and operation persistence paths. No Antithesis instrumentation was found; no invalidating path was identified.
- 2026-09-26: added the SDK dependency and monotonic-rank assertion at the domain transition boundary. `go test ./internal/domain/operations` passes.
- 2026-09-27: replaced the deleted Python model runner with native Zig properties in `Janus-Zig/src/antithesis_tests.zig`; monotonic and terminal replay checks pass in the root Zig test target.

## Open Questions

None from repository evidence. Confirm API status spellings while implementing the workload.
