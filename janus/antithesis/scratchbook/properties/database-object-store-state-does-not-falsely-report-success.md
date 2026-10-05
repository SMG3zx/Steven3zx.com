# database-object-store-state-does-not-falsely-report-success

## Evidence trail

PostgreSQL stores build/deployment metadata, bucket names, and object keys. MinIO stores the referenced bytes. Worker code constructs object-store and PostgreSQL adapters independently, so a dependency fault can split metadata from bytes.

## Failure scenario

An artifact upload or read is interrupted while the database transition proceeds, or MinIO becomes unreachable after metadata is committed. The API must not expose a success state that cannot be used to launch or retrieve the artifact.

## Local implementation

The native Zig property target verifies the deterministic fail-closed artifact path boundary. The service-backed MinIO deletion workflow is retained as a separate integration requirement until it has a native Zig client.

## Instrumentation status

The deployment launcher already fails closed when object download fails. The local workload exercises this boundary; a dedicated production-side SDK assertion around artifact verification remains a refinement.

## Investigation Log

- 2026-09-26: inspected object-store adapter references, build upload persistence, deployment executor interfaces, and compose dependencies. The canonical public verification endpoint is not obvious from local code.
- 2026-09-26: removed a built artifact from MinIO with `mc` and verified the asynchronous deployment converges to failed with an error message.

## Open Questions

`(needs human input)` Identify the canonical artifact download/status endpoint for workload verification.
