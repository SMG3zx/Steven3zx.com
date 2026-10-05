---
sut_path: C:\Users\nerfs\Documents\Projects\Janus
commit: f899a3196ccaa07a107b372ec259cb9c8923e7df
updated: 2026-09-26
external_references: []
---

# Coverage-balance evaluation

The catalog covers the major risks in the SUT analysis: durable queue recovery, state monotonicity, at-least-once side effects, PostgreSQL/MinIO split-brain, runtime process state, readiness, auth boundaries, resource cleanup, and build strategy failure. It intentionally has no consensus/replication property because the repository topology is single PostgreSQL with no replica or leader election.

## Gap

The first workload should include both build and deployment paths; otherwise runtime-launch and artifact properties are vacuous. The catalog records this as an implementation open question rather than adding another duplicate property.

## Passes

Safety and liveness are balanced; reachability is used only for important failure branches.

## Bias

The catalog is backend/control-plane weighted. That is appropriate for Antithesis's first pass because the frontend is embedded and existing browser tests are deterministic, but a later pass should add UI/API contract properties if the frontend is a production-critical boundary.
