---
sut_path: C:\Users\nerfs\Documents\Projects\Janus
commit: f899a3196ccaa07a107b372ec259cb9c8923e7df
updated: 2026-09-26
external_references: []
---

# Property catalog

The user scoped this run to the repository only. The catalog targets timing-sensitive, concurrent, and partial-failure behavior that ordinary unit tests do not cover. Priorities are `P0` critical first-run safety, `P1` high-value recovery/contract behavior, and `P2` useful breadth.

## P0 — queue/state correctness

### `operation-state-never-regresses`

- **Type:** Safety (`Always`)
- **Priority:** P0
- **Property:** Once an operation reaches a terminal state (`succeeded` or `failed`), concurrent polling and retries never expose a later response that returns it to `queued` or `processing`.
- **Evidence:** `internal/domain/operations/lifecycle.go`, PostgreSQL operation writes, and the asynchronous submitter/worker split.
- **Antithesis angle:** Pause or restart API/worker around status writes and poll concurrently; explore duplicate delivery and stale updates.
- **SUT instrumentation:** Missing. Assert terminal-state monotonicity at the operation transition/write boundary with operation ID and previous/new status.
- **Open Questions:** None after code inspection; exact API status spelling should be confirmed in workload wiring.
- **Implementation status:** Partially implemented locally. The Go domain lifecycle now emits an Antithesis `Always` assertion for every successful operation transition, and the native Zig property target checks deterministic monotonic and terminal replay sequences; database-level concurrent polling and stale-write behavior still need workload coverage.
- **Provenance:** Data integrity, concurrency, protocol contracts.

### `queue-and-state-reconcile-after-worker-loss`

- **Type:** Liveness (`Sometimes` plus `eventually_` recovery check)
- **Priority:** P0
- **Property:** A build or deployment persisted in an executable/recoverable state eventually receives execution after the worker is paused or terminated and later restored, provided PostgreSQL and object dependencies recover.
- **Evidence:** `cmd/worker.go` `recoverPendingJobs` and `reconcileLoop`; `BuildReconciliationStore` and `DeploymentReconciliationStore`; architecture claim that reconciliation repairs lost/discarded jobs.
- **Antithesis angle:** Worker termination and database/network faults create the exact durable-recovery state deterministic tests miss.
- **SUT instrumentation:** Missing. Add reachable/recovery-phase markers for list, reschedule, and execution outcome; workload must poll terminal state during a quiet period.
- **Open Questions:** (needs human input) confirm worker termination faults are enabled for the target tenant; without them, use a custom test command to stop/restart the worker if permitted.
- **Provenance:** Failure recovery, lifecycle transitions, distributed coordination.

### `duplicate-delivery-does-not-duplicate-external-effects`

- **Type:** Safety (`Always`)
- **Priority:** P0
- **Property:** Repeated delivery of the same build/deployment job does not create multiple durable artifacts, active runtime instances, or conflicting terminal records for one logical ID.
- **Evidence:** River is at-least-once; build/deployment executors and object/runtime adapters are re-enterable after worker retries.
- **Antithesis angle:** Concurrent workers, pauses, and partial completion expose idempotency gaps.
- **SUT instrumentation:** Stable operation correlation IDs, River argument keys, artifact keys, runtime IDs, and terminal records are compared at the operation and workload boundaries.
- **Implementation status:** Implemented locally for duplicate overlap. Replayed project/deployment submissions with the same `X-Request-Id` reuse one operation through a PostgreSQL uniqueness guard; repeated River enqueue attempts for the same build and deployment IDs produce one durable job each; build and deployment executors use keyed single-flight protection plus PostgreSQL advisory locks, with tests covering sequential, concurrent, and cross-worker lock behavior. Crash/retry reconciliation still needs an end-to-end test proving an abandoned execution is safely reclaimed without reusing stale runtime state.
- **Open Questions:** (partial: runtime cleanup/reuse behavior is spread across launcher code and was not fully exercised) determine whether a deployment retry reuses a runtime ID or launches a second endpoint.
- **Provenance:** Idempotency/replay, concurrency, failure recovery.

## P1 — external boundary consistency

### `database-object-store-state-does-not-falsely-report-success`

- **Type:** Safety (`Always`)
- **Priority:** P1
- **Property:** A build/deployment is not reported as successfully usable when its referenced artifact is unavailable or missing from MinIO; if the artifact boundary fails, the record reaches a visible failed/retryable state.
- **Evidence:** PostgreSQL stores bucket/key metadata while MinIO stores bytes; worker build and deployment paths use both.
- **Antithesis angle:** Partition or throttle MinIO between object write/read and database status transition.
- **SUT instrumentation:** The deployment execution boundary fails when the runtime launcher cannot download the referenced object; the workload removes the MinIO object and verifies the deployment reaches failed with an error.
- **Open Questions:** (needs human input) identify the canonical artifact download/status endpoint for workload verification.
- **Implementation status:** Implemented locally for deployment consumption. The native Zig property target verifies the deterministic fail-closed artifact path boundary; the service-backed MinIO deletion workflow remains a separate integration gate.
- **Provenance:** Data integrity, failure recovery, external dependencies.

### `runtime-launch-state-matches-process-reality`

- **Type:** Safety (`Always`) and Reachability (`Reachable` for launch-failure path)
- **Priority:** P1
- **Property:** A deployment marked running has a reachable runtime endpoint; a failed/terminated runtime is not left indefinitely marked running, and launch error handling is reachable.
- **Evidence:** `runtime/wasmer/launcher.go` reserves ports, starts subprocesses, waits for TCP, parses stdout, and exposes endpoint state; deployment executor marks running or failed.
- **Antithesis angle:** Thread pauses, endpoint delays, process termination, and malformed runtime responses create state/process divergence.
- **SUT instrumentation:** Deployment execution now emits an `Always` assertion requiring runtime ID and endpoint before `MarkDeploymentRunning`; lifecycle assertions and the workload cover cleanup/termination and domain-proxy reachability.
- **Implementation status:** Implemented locally for launch identity and endpoint truthfulness. The local build/deployment workload verifies the assertion, runtime-domain reachability, deletion-to-stopped convergence, and PostgreSQL/MinIO fault outcomes.
- **Open Questions:** (partial: cleanup behavior is distributed across launcher helpers) determine the maximum intended stale-running window.
- **Provenance:** Lifecycle transitions, resource boundaries, failure recovery.

### `readiness-reflects-required-dependencies`

- **Type:** Safety (`Always`) and Reachability (`Reachable` for dependency-failure response)
- **Priority:** P1
- **Property:** `/healthz` remains a process liveness signal, while `/readyz` reports unavailable when required database/runtime dependencies cannot be used and recovers after they become usable.
- **Evidence:** `server.go` readiness handler and `http_test.go` tests for runtime readiness failure; compose dependency startup currently only verifies PostgreSQL health.
- **Antithesis angle:** Independent dependency faults and startup ordering are not covered by current deterministic tests.
- **SUT instrumentation:** `/readyz` now checks both PostgreSQL and the configured MinIO artifact bucket, while compose waits for a MinIO healthcheck before API/worker startup.
- **Implementation status:** Implemented locally. The native Zig property target verifies deterministic dependency-disabled readiness; restarting PostgreSQL and MinIO independently remains a separate service-backed integration gate.
- **Provenance:** Protocol contracts, lifecycle transitions, external dependencies.

## P1 — protocol and authorization

### `authorized-user-cannot-cross-resource-boundary`

- **Type:** Safety (`Always`)
- **Priority:** P1
- **Property:** A valid authenticated user cannot read or mutate another user's project, build, deployment, credentials, operation, or runtime through alternate endpoint sequences or guessed IDs.
- **Evidence:** auth middleware, `AuthContext`, project/build/deployment handlers, and repository ownership checks.
- **Antithesis angle:** Concurrent creation/deletion and replayed IDs can expose authorization gaps that isolated endpoint tests miss.
- **SUT instrumentation:** Workload uses two users and asserts that the second user's project listing excludes the first user's project; internal authorization assertions remain optional at repository boundaries.
- **Open Questions:** (needs human input) confirm whether projects are intentionally shareable across users/teams; current repository-only evidence does not define tenancy policy.
- **Implementation status:** Implemented locally for project read and mutation boundaries. Project listing is owner-scoped, project update/delete commands carry the authenticated owner and verify it before mutation, and the two-user workload verifies listing, update, and delete isolation against the Docker-backed API.
- **Provenance:** Security boundaries, protocol contracts.

### `operation-polling-eventually-converges`

- **Type:** Liveness (`Sometimes` with quiet-period eventual check)
- **Priority:** P1
- **Property:** After a submitted operation's underlying work completes and dependencies recover, operation polling eventually returns the final result and does not remain indefinitely in an intermediate state.
- **Evidence:** frontend polling in `frontend/web/src/lib/janus-api.ts`, operation query handlers, River worker transitions, and reconciliation loop.
- **Antithesis angle:** Faults can interrupt the transition between side effect, database write, and polling response.
- **SUT instrumentation:** Missing. Mark transition completion and reconciliation; workload polls with bounded backoff and a quiet period.
- **Open Questions:** (partial: timeout values are split between frontend and backend) settle the workload's maximum eventual-convergence budget.
- **Implementation status:** Partially implemented locally. Native Zig tests cover route/auth contracts and deterministic operation boundaries; authenticated HTTP workflows and worker/PostgreSQL/MinIO restart sequences remain separate service-backed integration work.
- **Provenance:** Failure recovery, protocol contracts, lifecycle transitions.

## P2 — resource and build robustness

### `bounded-work-does-not-leak-process-or-connection-resources`

- **Type:** Safety (`Always`) with Reachability (`Reachable` for timeout/cancellation paths)
- **Priority:** P2
- **Property:** Repeated builds, runtime launches, log streaming, and failed requests do not leave unbounded child processes, goroutines, file descriptors, database connections, or ring-buffer memory.
- **Evidence:** build/runtime subprocess creation, output streaming goroutines, mutex-protected ring buffer, build timeout configuration, and worker concurrency configuration.
- **Antithesis angle:** Throttling, hangs, timeout races, and repeated retries expose leaks more effectively than fixed tests.
- **SUT instrumentation:** The build/deployment workload records the worker's `wasmtime` process count before launch and verifies it returns to baseline after deployment deletion; broader process/goroutine/connection metrics remain open.
- **Open Questions:** (needs human input) define acceptable resource baseline/tolerance for the Antithesis image.
- **Implementation status:** Partially implemented locally. Runtime child-process and build-work-directory cleanup are checked under dependency fault cases and unsupported-source failure; database connection, goroutine, and repeated-long-run resource baselines remain open.
- **Provenance:** Resource boundaries, failure recovery, concurrency.

### `source-build-selection-is-deterministic-and-fails-closed`

- **Type:** Safety (`Always`) and Reachability (`Reachable` for unsupported/malformed source)
- **Priority:** P2
- **Property:** The same source tree and declared runtime select the same build strategy, while unsupported or malformed source fails with a visible build failure and does not publish a misleading artifact.
- **Evidence:** `domain/builds/strategy.go`, source validation, multiple language toolchains in the runtime image, and build executor status writes.
- **Antithesis angle:** Partial source trees, toolchain delays, and interrupted builds test cleanup and publication ordering.
- **SUT instrumentation:** Strategy selection is covered by deterministic fixture tests across prebuilt, Go, Rust, C/WASI, Python, JavaScript, and unknown source markers; build publication failure remains covered by the missing-artifact boundary.
- **Open Questions:** (needs human input) select the smallest supported fixture language for first-run bootstrap; current repository does not define an Antithesis fixture.
- **Implementation status:** Implemented locally for strategy selection and failure handling. Go fixture tests verify deterministic selection and unknown-source classification; the Docker-backed workload uploads an unrecognized source, verifies a visible failed build, and verifies no artifact is published.
- **Provenance:** Protocol contracts, resource boundaries, lifecycle transitions.

## Catalog assumptions and unresolved scope

- Properties are formulated from repository code and local architecture notes only; no external claim was promoted without local code evidence.
- Recovery properties require a quiet period and may require tenant-enabled termination faults.
- The catalog intentionally avoids making a claim about multi-region consensus or replication because Janus uses a single PostgreSQL service in the repository topology.
