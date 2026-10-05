# bounded-work-does-not-leak-process-or-connection-resources

## Evidence trail

Build/runtime paths create subprocesses and stream stdout/stderr through goroutines. `ringLogBuffer` is mutex protected and bounded, while worker concurrency is configured by environment. Build timeout parsing and HTTP client timeouts provide cancellation boundaries.

## Failure scenario

Repeated timeouts, child termination, log streaming, or worker retries leave subprocesses, goroutines, connections, or endpoint reservations alive after the logical job is terminal.

## Local implementation

`test_build_workflow.py` captures the worker's `wasmtime` process count before a deployment and asserts the count returns to baseline after the deployment is deleted, including PostgreSQL and MinIO fault cases.

## Instrumentation status

Child-process cleanup is now checked locally. Metrics for goroutines, database connections, and repeated-build resource baselines remain open.

## Investigation Log

- 2026-09-26: inspected build execution helpers, runtime launcher, ring buffer, worker concurrency settings, and timeout parsing. Repository code lacks a single resource accounting surface.
- 2026-09-26: added a worker process-count return-to-baseline assertion after deployment deletion.

## Open Questions

`(needs human input)` Define acceptable resource baseline and tolerance for the Antithesis image.
