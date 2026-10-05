# duplicate-delivery-does-not-duplicate-external-effects

## Evidence trail

The repository architecture claims River at-least-once delivery and idempotent external side effects. Build and deployment executors call PostgreSQL repositories, object storage, and WASM runtime adapters. These are separate side effects and can be interrupted between completion and status persistence.

## Failure scenario

Two worker attempts process one logical build or deployment, or a retry re-enters after the first attempt succeeded externally but before its database update. The system must converge to one artifact/runtime and one coherent terminal record.

## Instrumentation status

Operation-level correlation deduplication is now instrumented by the PostgreSQL uniqueness guard and exercised by the local workload. River's durable argument uniqueness is exercised for repeated build/deployment enqueue attempts. Runtime endpoint truthfulness is also exercised through the local domain proxy; duplicate worker execution after a lease handoff remains to be added around storage/runtime side effects.

## Investigation Log

- 2026-09-26: inspected River queue package references, build/deployment executors, object store adapter, and runtime launcher. The code establishes at-least-once entry points, but full idempotency semantics are not centralized in one function.
- 2026-09-26: added correlation-based operation reuse for repeated `X-Request-Id` submissions, a unique PostgreSQL index, and local replays for project and deployment submissions. The same operation ID is returned and the build/deployment fault matrix passes; duplicate worker delivery remains to be tested directly.
- 2026-09-26: configured the local worker to advertise its service hostname instead of container-local `127.0.0.1`, so runtime endpoints are reachable through the API domain proxy; both PostgreSQL and MinIO fault cases pass.
- 2026-09-26: added a PostgreSQL-backed River integration check that submits each build/deployment key three times and verifies one durable job per logical ID.

## Open Questions

`(partial: runtime cleanup/reuse behavior is distributed across launcher helpers)` Determine whether a retry reuses a runtime ID or starts a second endpoint.
