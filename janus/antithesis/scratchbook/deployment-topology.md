---
sut_path: C:\Users\nerfs\Documents\Projects\Janus
commit: f899a3196ccaa07a107b372ec259cb9c8923e7df
updated: 2026-09-26
external_references: []
---

# Deployment topology

## Proposed minimal topology

```text
test drivers -> janus-api:8080 -> postgres:5432
                         |        -> minio:9000
                         |        -> runtime subprocesses
                         |
                    janus-worker -> postgres:5432
                                  -> minio:9000
                                  -> build toolchains / runtime subprocesses
```

### `postgres`

Use a pinned PostgreSQL image with a healthcheck. It is the durable system of record and River queue store. It must be separate so Antithesis can independently delay, partition, throttle, or terminate the database.

### `minio`

Use the existing MinIO-compatible image or a pinned equivalent with an explicit healthcheck and a deterministic bucket-initialization path. It must be separate because source/artifact availability is a distinct failure boundary from PostgreSQL.

### `janus-api`

Build from the project Containerfile, run `serve`, expose port 8080, and connect to PostgreSQL and MinIO. Keep the frontend embedded in this image. Add a simple healthcheck against `/healthz` and readiness check against `/readyz`. This container owns HTTP authentication, operation submission/polling, and runtime proxying.

### `janus-worker`

Build the same image, run `worker`, and connect to PostgreSQL and MinIO. Do not combine it with the API: independent worker termination and network faults are required to test durable jobs and reconciliation.

### Test driver

Place workload/test commands under `antithesis/test/`. The driver should use the public API to create a project and submit a small build/deployment flow, poll operation and deployment state, and record stable IDs. A driver may use a prebuilt fixture artifact initially if source-build setup is too expensive, but build and artifact paths should be covered separately.

## Dependency edges and readiness

- `janus-api` -> `postgres` with `condition: service_healthy`.
- `janus-api` -> `minio` with `condition: service_healthy`.
- `janus-worker` -> `postgres` with `condition: service_healthy`.
- `janus-worker` -> `minio` with `condition: service_healthy`.

The current root compose file uses `service_started` for MinIO and has no MinIO healthcheck; setup must correct this in the Antithesis compose file rather than copy it unchanged. API and worker both need `NO_COLOR=1`, `platform: linux/amd64`, explicit hostnames/container names, and `init: true`.

## Instrumentation and observation plan

Start with SUT-side Go assertions in the API/worker image, cataloging the assertion artifacts as required by setup. Candidate assertion points are operation lifecycle transitions, build/deployment status writes, queue scheduling, recovery resubmission, and runtime launch success/failure. Workload-visible checks should remain the source of truth for end-to-end liveness.

## Assumptions and Open Questions

### Assumptions

- Four runtime containers are sufficient for the first useful search space.
- Frontend behavior can be tested through API calls initially; a browser container is not necessary for the first run.

### Open Questions

- Whether Antithesis tenant policy permits node termination faults for `janus-worker` and `postgres`.
- Whether the existing MinIO image's API and bucket behavior should be retained or replaced by a setup helper.
- Whether an external test driver container is required by the tenant contract or whether Antithesis test commands can run in one existing SUT container.
