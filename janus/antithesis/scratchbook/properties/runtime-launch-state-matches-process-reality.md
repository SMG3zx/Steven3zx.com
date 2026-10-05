# runtime-launch-state-matches-process-reality

## Evidence trail

`internal/adapters/outbound/runtime/wasmer/launcher.go` reserves an endpoint, starts a runtime subprocess, waits for TCP, parses the response format, streams output, and uses a mutex-protected ring buffer. The deployment executor calls the launcher and persists running or failed state.

## Failure scenario

The process starts slowly, exits after the TCP wait, returns malformed output, or is terminated after the database says it is running. The deployment should expose failure or unavailable state rather than an indefinitely usable-looking endpoint.

## Local implementation

The deployment executor now emits a stable `Always` assertion, `running deployments have runtime identity and endpoint`, before persisting a running status. The workload probes the domain proxy and verifies deletion converges to stopped.

## Instrumentation status

Launch identity and endpoint assertions are implemented; process cleanup is checked by the worker process-count workload. Independent delayed/failing runtime fixtures remain open.

## Investigation Log

- 2026-09-26: inspected launcher process, TCP wait, response parser, deployment executor, and runtime tests. Tests cover parsing and isolated executor outcomes, not independent process faults.

## Open Questions

`(partial: cleanup behavior is distributed across launcher helpers)` Determine the intended maximum stale-running window.
