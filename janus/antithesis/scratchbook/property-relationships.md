---
sut_path: C:\Users\nerfs\Documents\Projects\Janus
commit: f899a3196ccaa07a107b372ec259cb9c8923e7df
updated: 2026-09-26
external_references: []
---

# Property relationships

## Cluster A — durable queue and lifecycle recovery

`queue-and-state-reconcile-after-worker-loss`, `operation-polling-eventually-converges`, and `operation-state-never-regresses` share operation/build/deployment transitions and River recovery. The first is the primary recovery property; the second is the externally visible liveness consequence; the third is the safety guard against stale or duplicate updates. A successful first property does not imply monotonic API responses, so the safety check remains independent.

## Cluster B — at-least-once side effects

`duplicate-delivery-does-not-duplicate-external-effects`, `database-object-store-state-does-not-falsely-report-success`, and `runtime-launch-state-matches-process-reality` cover the two-phase boundary between durable records and object/runtime effects. Duplicate-delivery is the broad dominance candidate: if strong idempotency holds at every side-effect boundary, many duplicate artifact/runtime failures disappear, but it does not by itself prove status accurately reflects availability.

## Cluster C — dependency and lifecycle contracts

`readiness-reflects-required-dependencies` and `runtime-launch-state-matches-process-reality` both test whether availability signals match actual dependency/process health. Readiness is a precondition for recovery workload sequencing; it is not a substitute for probing a deployment endpoint.

## Cluster D — adversarial input and resource pressure

`source-build-selection-is-deterministic-and-fails-closed` and `bounded-work-does-not-leak-process-or-connection-resources` share build/runtime toolchain paths. Malformed source can create long-running or failed subprocesses, so the resource property should be run alongside build fixtures.

## Orthogonal security cluster

`authorized-user-cannot-cross-resource-boundary` is orthogonal to queue recovery but should run concurrently with lifecycle operations to catch authorization mistakes in stale/replayed identifiers.

## Assumptions and Open Questions

### Assumptions

- Relationships describe suspected test interactions, not proven implications.

### Open Questions

- The exact workload sequencing needed to create artifacts and deployments is not yet established.
