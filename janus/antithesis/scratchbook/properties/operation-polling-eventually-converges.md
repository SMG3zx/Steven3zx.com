# operation-polling-eventually-converges

## Evidence trail

The frontend polling helper in `frontend/web/src/lib/janus-api.ts` waits for operation state with an interval and timeout. Backend operation queries read PostgreSQL while the worker writes transitions and reconciliation resubmits recoverable records.

## Failure scenario

An external side effect completes but a worker or database fault interrupts the status update. After dependencies recover, polling must eventually observe the terminal result instead of hanging in queued/processing forever.

## Instrumentation status

No Antithesis assertions exist. Add completion/reconciliation markers and use a quiet-period eventual check from the workload.

## Investigation Log

- 2026-09-26: inspected frontend polling, operation query paths, worker reconciliation, and lifecycle tests. Frontend and backend timeout budgets are distributed across files.
- 2026-09-26: the former Python state-machine coverage was replaced by native Zig contract tests. The suite exercises authenticated HTTP operations, polling, project creation/listing, and local worker behavior; dependency-backed PostgreSQL and MinIO restart scenarios remain integration boundaries. The local native run passes.
- 2026-09-26: the local workload now also covers source-bundle upload, queued build polling, worker restart, and terminal artifact visibility with a deterministic prebuilt WASM fixture.
- 2026-09-26: deployment creation and runtime convergence are now covered after artifact build; the local fixture reaches `running` with a runtime ID after worker restart.

## Open Questions

`(partial: timeout values are split between frontend and backend)` Set a bounded convergence budget for the workload.
