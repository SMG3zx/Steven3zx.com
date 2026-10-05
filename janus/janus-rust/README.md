# Janus Rust core

This crate is the first implementation slice of the Janus Rust transition.
The control-plane model is dependency-light: the deterministic ECS core uses
bounded standard-library data structures, while Tokio and `tracing` provide the
production actor adapter and observability boundary.

## Current slice

- actor-owned bounded world;
- build entity lifecycle;
- deployment and runtime lifecycle;
- tenant boundary checks;
- command idempotency;
- generation fencing for stale process results;
- versioned command envelopes;
- bounded external-resource ownership claims;
- generation-fenced process claims attached to deployment components;
- tenant-fenced resource release and cleanup;
- pause, resume, and drain controls;
- compatibility contracts for the current core HTTP routes;
- supervised actors and bounded FIFO mailboxes;
- validated startup capacities, tenant quotas, and effect capabilities;
- checked Tokio startup path that binds operator configuration to actor bounds;
- per-effect process, filesystem, and network capability validation;
- authenticated principal and permission checks before command admission;
- authorized tenant-scoped mailbox admission with typed failures;
- versioned event journal and crash-recovery snapshots;
- fail-fast invariant audits after every world tick;
- bounded trace, simulation-history, and supervisor-event retention;
- explicit lifecycle events and execution effects;
- deterministic simulator with injectable process failure;
- dropped and duplicated effect fault injection with replay records;
- replay construction that compares regenerated transcript steps;
- typed simulation failures when the bounded event journal is exhausted;
- structured trace records for later `tracing` integration;
- `tokio-rs/tracing` adapter for production subscribers;
- Tokio control-plane actor adapter and `janus-sim` executable;
- bounded Tokio HTTP transport and `janus-api` migration entry point;
- rustdoc contracts and invariant tests.

The world is the actor boundary: callers enqueue commands, systems mutate the
world during a bounded tick, and external work is returned as an explicit
effect. Production adapters will execute effects through Tokio-backed actors;
the simulator executes them deterministically.

## Verification

Run from this directory when Rust is installed:

```text
cargo test
cargo test --doc
cargo clippy --all-targets --all-features -- -D warnings
cargo doc --no-deps
cargo run --bin janus-sim
cargo run --bin janus-api
cargo run --bin janus -- help
cargo run --bin janus -- health
```

The `janus` binary is the dedicated operator CLI. It supports login, projects,
builds, deployments, runners, metrics, the admin specification, health, and
readiness. Set `JANUS_URL` to target another local API and `JANUS_TOKEN` for
authenticated requests; pass `--json` for machine-readable output.

The CI workflow also runs these checks on Ubuntu and Windows. On Windows,
native test binaries require the MSVC linker and Windows SDK.

## Windows and rust-analyzer troubleshooting

The project uses the stable toolchain selected by `rust-toolchain.toml`; it
does not require the GNU toolchain. On a Windows development machine, use the
installed MSVC toolchain when rust-analyzer reports that
`stable-x86_64-pc-windows-gnu` is missing:

```text
rustup default stable-x86_64-pc-windows-msvc
cargo metadata --format-version 1 --no-deps
```

Restart rust-analyzer after changing the active toolchain. A successful
`cargo metadata` command confirms that the workspace can be loaded; it avoids
making the repository depend on a platform-specific linker configuration.
