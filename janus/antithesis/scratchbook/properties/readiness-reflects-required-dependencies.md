# readiness-reflects-required-dependencies

## Evidence trail

The API exposes `/healthz` and `/readyz`. HTTP tests cover readiness propagation for runtime failures. Root compose waits for PostgreSQL health but only `service_started` for MinIO, leaving an explicit startup race for object operations.

## Failure scenario

The process is alive while PostgreSQL, MinIO, or runtime support is unavailable. Health must remain a liveness signal and readiness must accurately prevent traffic that requires missing dependencies.

## Local implementation

`/readyz` now reports PostgreSQL and MinIO artifact-bucket status. Compose has a MinIO live healthcheck and API/worker depend on `service_healthy`. Native Zig tests verify the deterministic dependency-disabled response; dependency restart/recovery remains a service-backed integration requirement.

## Instrumentation status

The endpoint itself is the local assertion surface; a dedicated SDK marker can be added later if remote triage needs more detail.

## Investigation Log

- 2026-09-26: inspected `server.go`, readiness tests, and compose dependency conditions. The current readiness contract does not clearly include MinIO.
- 2026-09-27: native Zig readiness coverage now verifies the dependency-disabled contract; the Compose restart/recovery workload remains a separate service-backed gate.

## Open Questions

None for the local contract; the workload verifies both required dependencies.
