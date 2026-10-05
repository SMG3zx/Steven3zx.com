# Janus Rust transition architecture

Janus Rust is a deterministic orchestration core, not a direct line-by-line
translation of the Zig HTTP server.

## Ownership

Actors own ECS worlds. Systems may mutate only the world owned by their actor.
Cross-actor work uses bounded commands and events. External resources are
capabilities supplied to effect handlers; domain systems do not access the
filesystem, process table, network, or database directly.

## State and safety

Every operational entity carries a tenant boundary and a generation. Commands
carry idempotency identities. A command is applied at most once, and a result
from an old generation cannot mutate newer state. All queues, entities, output,
and external work receive explicit capacities.

## Execution modes

Production uses Tokio-backed actors, real clocks, real processes, and durable
persistence. Simulation uses the same ECS systems and command/event protocol,
but replaces scheduling, time, process execution, and persistence with
deterministic implementations. A simulation failure must preserve its seed,
commands, scheduler decisions, events, and trace records for replay.

## First vertical slice

```text
submit build
  -> validate tenant and capacity
  -> create Pending entity
  -> transition to Running
  -> emit RunBuild effect
  -> receive generation-fenced completion
  -> transition to Succeeded or Failed
```

The next slices add supervision, persistence/recovery, real process execution,
HTTP compatibility, deployment/runtime entities, and production tracing.

The first Rust slice now includes the compatibility route table, bounded
supervisor/mailbox primitives, validated startup capacities and capabilities,
deployment/runtime transitions, and replay records for dropped or duplicated
effects. The route table is deliberately an adapter contract; it does not make
the ECS core depend on an HTTP framework.

Snapshots preserve component state, control mode, and command idempotency. A
versioned event journal provides the persistence port; a later PostgreSQL actor
can implement the same append and restore contracts without entering the ECS
systems. Actor restart policy and mailbox capacity are explicit state rather
than implicit runtime behavior.

The Tokio adapter wraps the same synchronous world tick in a bounded async
mailbox and emits effects through another bounded channel. The simulator and
the production adapter therefore share the command, event, effect, and
generation-fencing logic.

Every tick audits tenant ownership, build/deployment relationships, quotas,
and queue capacities. Violations are programmer errors and fail fast; expected
operational failures remain represented as typed events and rejection reasons.

Simulation transcripts contain external commands, fault plans, persisted-event
counts, and effect counts. `Simulator::from_replay` regenerates the run and
rejects the first divergent tick, making failures portable to CI or a local
debugger. Simulation steps return a typed error when the bounded event journal
cannot retain a tick, so persistence loss is visible rather than silently
dropped.

Verification is split by toolchain availability: local development uses
formatting, all-target type checking, Clippy, and rustdoc; the repository's
Windows CI job runs native test binaries and the simulator with the Windows
linker/runtime available.

## Requirement evidence

| Concern | Implementation evidence | Verification evidence |
| --- | --- | --- |
| ECS and actor isolation | `World`, bounded commands, Tokio control actor | lifecycle and runtime adapter tests |
| Security and tenancy | route/principal admission, tenant quotas, tenant-fenced claims | authorization and boundary tests |
| Versioning and recovery | command envelopes, event journal, snapshots | version rejection and restore tests |
| Operational control | pause, resume, drain, shutdown, supervisor states | control-mode and supervisor tests |
| Resource ownership | generation-fenced deployment claims and registry | stale-owner and invariant tests |
| Simulation and faults | deterministic simulator, replay transcript, fault plan | replay equivalence tests |
| Observability | `tracing` adapter, trace records, performance samples | rustdoc, Clippy, and runtime adapter checks |
| Compatibility | complete route contract table and route authorization | compatibility route tests |
