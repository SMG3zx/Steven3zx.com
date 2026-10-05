# Current Architecture

Janus uses one Go binary with separate API (`serve`) and worker (`worker`) modes, rooted at `backend/janus-api/cmd`.

## Backend Shape

- `janus-api` and `janus-worker` run the same backend binary in separate modes.
- HTTP is the public transport boundary.
- Internal orchestration stays inside the application boundary through application use cases.
- Build and deployment execution run in the `worker` mode of the Janus binary and are dispatched through River on PostgreSQL.
- Durable state lives in Postgres; artifacts live in MinIO-compatible object storage.

## Request Flow

```text
browser / web client
  -> HTTP handler
  -> application use case
  -> postgres / object store / runtime launcher
  -> operation + build/deployment state persisted
  -> transactional River job
  -> janus worker executes long-running work
```

## Main Subsystems

- `cmd`: API startup, migrations, and River worker mode
- `internal/adapters/inbound/http`: HTTP routes, middleware, request/response mapping
- `internal/application/usecases`: orchestration and business workflow logic
- `internal/adapters/outbound/postgres`: persistence adapters
- `internal/adapters/outbound/objectstore`: source bundle and artifact storage
- `internal/adapters/outbound/buildexec`: build execution
- `internal/adapters/outbound/runtime/wasmer`: runtime launch

## Operational Model

- `/healthz` is a liveness probe.
- `/readyz` covers database readiness; River uses the same PostgreSQL database.
- `/metrics` exposes Prometheus metrics for HTTP and worker execution.
- Build and deployment records and their River jobs are committed atomically in PostgreSQL.
- River provides durable at-least-once delivery, retries, worker concurrency, and stuck-job recovery.
- External build and runtime side effects remain idempotent and are reconciled from PostgreSQL.
- A periodic worker reconciliation pass resubmits executable PostgreSQL state when a job was discarded or a worker was lost.
- Reconciliation uses error-returning PostgreSQL queries, so database failures stop the pass visibly instead of being treated as an empty queue.

## Current Improvement Focus

- Keep internal calls off self-HTTP and on application boundaries.
- Continue reducing old distributed-service terminology.
- Tighten River worker observability and concurrency control.
- Add periodic reconciliation for desired versus observed runtime state.
