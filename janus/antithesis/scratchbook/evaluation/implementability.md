---
sut_path: C:\Users\nerfs\Documents\Projects\Janus
commit: f899a3196ccaa07a107b372ec259cb9c8923e7df
updated: 2026-09-26
external_references: []
---

# Implementability evaluation

API/worker/PostgreSQL/MinIO separation supports the core properties. The workload can observe operation, build, deployment, health, readiness, and proxy results over HTTP. Internal-only assertions are needed for exact queue/recovery phases and resource counts.

## Refinements

- Add a MinIO healthcheck and use `service_healthy` in the Antithesis compose file; the root compose currently has only `service_started`.
- Keep API and worker as separate containers so worker termination can be independent.
- Treat artifact existence and resource baselines as explicit workload/setup decisions, not assumptions.

## Blockers

`snouty` is unavailable, so the topology cannot yet be validated against the tenant's config or compose contract. The missing CLI blocks setup validation, not codebase research.
