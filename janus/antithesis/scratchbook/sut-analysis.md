---
sut_path: C:\Users\nerfs\Documents\Projects\Janus
commit: f899a3196ccaa07a107b372ec259cb9c8923e7df
updated: 2026-09-26
external_references: []
---

# SUT analysis

## Scope and provenance

The user answered the scope question verbatim: “Just this directory”. No external documentation, issue tracker, or related repository was consulted. The repository is at commit `f899a3196ccaa07a107b372ec259cb9c8923e7df`, with substantial uncommitted changes present; this analysis describes the current working tree as inspected on 2026-09-26.

## Product and architecture

Janus is a code-to-runtime control plane. A browser or API client submits authentication, project, build, deployment, runner, and telemetry operations to a Go HTTP API. The API persists domain state in PostgreSQL, stores source bundles and built artifacts in MinIO-compatible object storage, and places long-running build/deployment work on River queues backed by the same PostgreSQL database. A separate invocation of the same Go binary runs the River worker. The Qwik frontend is built into the API binary and served by the backend.

Primary entrypoints are `backend/janus-api/cmd/main.go` (`serve`, `worker`, `migrate`) and `backend/janus-api/cmd/app.go`. `server.go` registers HTTP handlers and middleware. The request path is HTTP handler -> application use case -> PostgreSQL/object store/wasmer adapter -> operation/build/deployment state and River job.

## Components and boundaries

- `janus-api`: HTTP API, readiness/liveness/metrics, authentication, CRUD and operation submission, reverse proxying to deployed WASM runtimes.
- `janus-worker`: same binary in `worker` mode; consumes River build/deployment jobs, executes build toolchains or WASM launchers, and periodically reconciles recoverable database state.
- `postgres`: durable users, projects, builds, deployments, operations, runners, credentials, logs, migration state, and River queue state.
- `minio`: source bundles and build/runtime artifacts. The compose file starts it without a healthcheck, so API/worker currently depend on `service_started` rather than verified readiness.
- `wasmer`/`wasmtime` subprocesses: runtime side effects for deployed artifacts; the launcher reserves endpoints, starts a process, streams output, and proxies HTTP requests.
- external Git providers, email providers, container/build toolchains, and uploaded source contents are integration boundaries. They are not represented as independent compose services in the current local stack.

## State and lifecycle

PostgreSQL is the durable source of truth for operation, build, and deployment state. The application uses domain lifecycle transitions for queued/processing/succeeded/failed operations and build/deployment status transitions. Build and deployment creation plus job insertion are intended to be coordinated through repository/queue adapters; the worker also calls `recoverPendingJobs` at startup and every 30 seconds to resubmit recoverable builds and starting deployments.

MinIO state is external to PostgreSQL. Database rows refer to source/artifact bucket/key values, while the object store contains the bytes. Runtime processes are ephemeral and are reconciled from database state only in the worker's currently implemented recovery paths.

The API has process-local state including rate-limiter buckets, runtime endpoint/process bookkeeping, and ring-buffered runtime logs. `sync.Mutex` protects several local buffers and rate-limiter structures. These values are not replicated between API and worker containers and are lost on process termination.

## Concurrency model

The Go HTTP server handles requests concurrently. The worker starts River's build and deployment consumers concurrently with a 30-second reconciliation goroutine. Build and deployment concurrency are configurable and default to two each. Runtime launchers stream child-process output concurrently and use mutex-protected ring buffers. PostgreSQL transactions and River's at-least-once job execution are the main coordination mechanism; retries can re-enter build/deployment code after a crash or timeout.

The frontend polls operation/deployment state and log endpoints, so API reads can race with worker transitions. The API can also reverse-proxy to active runtime endpoints while deployment state changes.

## Failure-prone areas and Antithesis fit

1. **Transactional queue/state boundary:** an operation or build may be committed while job insertion, worker acknowledgement, or external side effect is interrupted. The recovery pass should make the desired state executable again without duplicate or terminally inconsistent records.
2. **At-least-once build/deployment execution:** retries and worker loss can repeat object writes, runtime launches, or status transitions. This is the highest-value concurrency/partial-failure seam.
3. **Recovery loop behavior:** `recoverPendingJobs` returns database errors, but the periodic loop logs and continues. A database outage can therefore create a recovery gap until the next tick; repeated resubmission can also create duplicate queue pressure if the underlying job is still active.
4. **Object-store/database split:** database state may say an artifact exists while the object is missing or unavailable, and a successful object write may precede a failed database transition. Build and deployment paths need explicit observability for this split.
5. **Runtime process boundary:** reserve-port/launch/wait/proxy/cleanup sequencing is timing-sensitive. Process termination, endpoint reuse, malformed runtime output, or a runtime that starts but does not serve can leave deployment state and actual process state divergent.
6. **Readiness and dependency startup:** `/readyz` checks database/runtime conditions, while compose currently waits for PostgreSQL health but only `service_started` for MinIO. Startup races are likely around migrations, buckets, artifact reads, and runtime tool availability.
7. **API auth and operation exchange:** authentication, MFA, sessions, and operation polling combine user identity with asynchronous results. Replays, concurrent MFA verification, or authorization checks around object/project/deployment IDs are security and consistency targets.
8. **Source/build detection:** build strategy selection scans uploaded repositories and invokes multiple toolchains. Malformed or adversarial source trees, timeouts, partial artifacts, and path assumptions are good fault and resource targets.

## Existing test strategy

The repository has 33 discovered Go/TypeScript test files. Coverage is strongest in pure domain transitions, repository scanners, config, runtime parsing, HTTP readiness, and mocked use cases. There is an end-to-end frontend pipeline spec, but no Antithesis SDK imports or assertion calls were found. Deterministic tests do not exercise independent API/worker/container faults, River recovery after process loss, object-store outages, or repeated runtime launch races.

## Claimed guarantees to test

The local architecture documentation claims that build/deployment records and River jobs are committed atomically, River provides durable at-least-once delivery and stuck-job recovery, external side effects are idempotent and reconciled from PostgreSQL, and reconciliation repairs discarded jobs or lost workers. These are recorded as properties rather than accepted as established facts.

## Minimal useful Antithesis scope

The first harness should keep API, worker, PostgreSQL, and MinIO as separate containers. The frontend can remain embedded in the API image. Runtime subprocesses and the build toolchain can remain inside the API/worker image for the first pass. Separate API and worker containers are essential because independent worker/API faults are central to the properties; PostgreSQL and MinIO must also remain independently faultable.

## Assumptions and open questions

### Assumptions

- The intended SUT is the current Janus working tree, not only the last committed revision.
- A local Antithesis harness can use the existing root `Containerfile` and compose dependency layout after adapting it under `antithesis/config/`.
- The first workload can exercise API endpoints and observe PostgreSQL-backed state through the API rather than requiring direct database access.

### Open Questions

- The `snouty` CLI is not installed in this environment, so Antithesis-specific documentation and local validation could not be run during research.
- The exact tenant fault configuration is unknown; worker termination and clock faults may need explicit enablement for recovery properties.
- The minimum authenticated API sequence for creating a project, build, artifact, and deployment needs confirmation during workload implementation.
- MinIO bucket initialization and runtime artifact shape are not fully documented in this repository.
