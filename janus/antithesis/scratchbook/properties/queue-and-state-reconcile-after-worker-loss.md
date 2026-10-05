# queue-and-state-reconcile-after-worker-loss

## Evidence trail

`backend/janus-api/cmd/worker.go` starts River, calls `recoverPendingJobs` before consuming, and repeats it every 30 seconds. The function lists recoverable builds and starting deployments, then schedules them. The architecture note explicitly describes this as repair for discarded jobs or lost workers.

## Failure scenario

A build/deployment row is committed, the worker stops before completing or acknowledging the job, and the API continues to expose intermediate state. After worker restoration and dependency recovery, the row must be rescheduled and complete.

## Instrumentation status

No Antithesis assertions exist. Add markers or `Sometimes`/eventual checks for recovery list, reschedule, and terminal execution. The workload must use a quiet period before final polling.

## Investigation Log

- 2026-09-26: inspected worker startup, reconciliation loop, repository interfaces, and deployment/build lifecycle types. The recovery path exists; tenant fault availability remains unknown.
- 2026-09-27: the deleted Python workload was replaced by native Zig deterministic recovery-boundary tests; live worker restart and deployment recovery remain service-backed integration work.
- 2026-09-26: extended the same workload through deployment creation and worker restart. This exposed and fixed PostgreSQL-incompatible revision allocation (`MAX(...) FOR UPDATE`) by using a project-scoped transaction advisory lock; build-to-deployment recovery now passes locally.
- 2026-09-26: worker-cancelled build source preparation is now classified as retryable instead of permanently failed, and the MinIO recovery case passes after an injected worker restart.

## Open Questions

`(needs human input)` Confirm that worker termination faults are enabled, or provide an allowed custom stop/restart mechanism.
