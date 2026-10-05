---
sut_path: C:\Users\nerfs\Documents\Projects\Janus
commit: f899a3196ccaa07a107b372ec259cb9c8923e7df
updated: 2026-09-26
external_references: []
---

# Antithesis-fit evaluation

The strongest fits are worker-loss reconciliation, duplicate delivery, object-store/database splits, runtime process divergence, and polling convergence. They require independent container faults, timing variation, or at-least-once interleavings. Readiness and authorization are useful protocol properties but should remain workload-efficient so they do not consume search budget better spent on recovery.

## Findings

- `operation-state-never-regresses`: strong fit when run concurrently with duplicate delivery; weaker as a standalone fixed-input test.
- `queue-and-state-reconcile-after-worker-loss`: highest-value fit; requires worker termination or an equivalent custom fault.
- `duplicate-delivery-does-not-duplicate-external-effects`: highest-value fit; instrument logical IDs and side-effect keys.
- `bounded-work-does-not-leak-process-or-connection-resources`: useful under throttling/hangs, but resource thresholds need a compact measurement plan.

## Passes

The catalog includes safety, liveness, and reachability forms and separates deterministic unit-test concerns from timing-sensitive properties.

## Uncertainties

Tenant fault availability and local SDK integration are unknown because `snouty` is not installed.
