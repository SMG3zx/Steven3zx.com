# Janus full Rust replacement plan

This document is the migration contract for replacing the Go and Zig control
and runtime layers with Rust. The existing Rust crate remains the deterministic
domain kernel; production adapters must preserve its command, event, effect,
tenant, generation, and versioning contracts.

## Target ownership

| Capability | Current owner | Rust owner | Cutover gate |
| --- | --- | --- | --- |
| HTTP, CORS, rate limits, readiness | Go `internal/adapters/inbound/http` | Rust HTTP adapter | Route and response compatibility tests pass |
| Authentication, sessions, MFA | Go auth handlers and auth domain | Rust auth actor and security port | Auth/MFA contract and boundary tests pass |
| Projects and repositories | Go project/repo use cases | Rust project/repository ECS systems | Tenant-scoped CRUD parity |
| Builds and source bundles | Go build handlers/executors | Rust build ECS systems and build actor | Lifecycle, upload, log, and cancellation parity |
| Deployments and domains | Go deployment handlers/executors | Rust deployment/runtime actors | Process reality matches reported state |
| Operations and events | Go operations service | Rust operation entities and event stream | Idempotent operation convergence |
| Runners and workers | Go runner service/River queue | Rust supervisor and worker actors | Lease, heartbeat, loss, and recovery tests |
| SpacetimeDB state | Go persistence adapters and durable state | Rust SpacetimeDB module/client boundary | Reducer, snapshot/journal, outbox, and recovery tests |
| Object storage | Go MinIO adapter | Rust object-store effect actor | Artifact integrity and tenant isolation |
| Runtime execution | Zig process/runtime layer | Rust capability-scoped process actors | Resource, timeout, and cleanup properties |
| Telemetry and metrics | Go OpenTelemetry/Prometheus layer | Rust `tracing` and metrics adapter | Readiness and telemetry contract tests |

## API compatibility inventory

The first Rust adapter must preserve these route families before any legacy
handler is removed:

- `/healthz`, `/readyz`, and `/metrics`
- `/api/v1/auth/*` and `/api/v1/mfa/*`
- `/api/v1/projects*`, `/api/v1/repos/*`, and `/api/v1/templates`
- `/api/v1/uploads/*`, `/api/v1/builds*`, and `/api/v1/deployments*`
- `/api/v1/operations/*`, `/api/v1/logs`, and `/api/v1/events*`
- `/api/v1/runners*`, `/api/v1/domains`, and `/api/v1/backend-resources`
- `/api/v1/git/providers*`, `/api/v1/billing`, and `/api/v1/metrics`
- `/api/v1/telemetry*` and `/api/v1/admin/*`

The route table in `janus-rust/src/api.rs` is the compatibility source of
truth. Each route needs a typed request/response contract and an integration
test against the current Go behavior before cutover.

## Delivery stages

### Stage 1: Rust service shell

Add a Rust HTTP server, configuration loading, structured error responses,
authentication middleware, readiness checks, and a command adapter for the
existing build/deployment kernel. Keep Go as the externally visible fallback.

### Stage 2: durable control plane

Implement the SpacetimeDB schema and reducers, an outbox/inbox protocol,
worker leases, module deployment/versioning, generated client bindings, and
crash recovery. Keep bounded file stores as deterministic local fallbacks. No
external effect may be reported as successful until its durable state
transition is committed.

The initial module boundary lives in `janus-spacetimedb/`; the
`janus-core::LocalSpacetimeDb` implementation is the offline contract test
double. `janus-core::GeneratedSpacetimeDb` and its
`SpacetimeDbReducerClient` trait now isolate generated SDK types from the ECS.
The module must be checked against the pinned SpacetimeDB 2 Rust crate before
the environment-dependent generated bindings are committed.

Rust snapshot recovery now has an explicit `restore_and_recover` boundary. It
requeues running builds and starting deployments, clears stale deployment
process claims, and leaves already-running deployments intact when their
durable runtime identity is present. The recovery contract is covered by
`snapshot_recovery_requeues_incomplete_builds_and_deployments`. The
`FileRecoveryQueue` adapter now persists bounded recovery jobs with tenant and
generation metadata, separates build/deployment identities, and rejects stale
generation reuse. `RecoveryReconciler::startup` and
`RecoveryReconciler::reconcile` now expose the worker startup and periodic
entry points; the production worker still needs to supply the real
SpacetimeDB client and call them.

### Stage 3: complete domain parity

Move projects, repositories, operations, runners, artifacts, domains,
authentication, and telemetry into Rust ECS components and actor-owned
systems. Generate compatibility DTOs at the HTTP boundary rather than leaking
storage models.

### Stage 4: runtime replacement

Replace Zig process/runtime ownership with capability-scoped Rust actors for
build execution, deployment processes, workspaces, object storage, Git, and
network access. Every actor must have bounded resources, cancellation, cleanup,
and generation-fenced results.

### Stage 5: shadow and cutover

Run Rust in shadow mode, compare normalized commands/events/state with Go, then
cut over one route family at a time. Keep rollback routing until the Rust path
has passed soak tests and recovery drills.

## Non-negotiable verification

- Existing HTTP tests pass against the Rust adapter.
- Tenant, permission, and authentication boundaries are tested at every route.
- Duplicate delivery is idempotent across process restarts.
- SpacetimeDB recovery reconstructs the same ECS state and pending effects.
- Worker loss and process loss converge without leaked claims or false success.
- Deterministic simulations replay production failure transcripts.
- Rust and legacy responses are compared during shadow mode.
- Legacy Go/Zig components are removed only after their cutover gates pass.
