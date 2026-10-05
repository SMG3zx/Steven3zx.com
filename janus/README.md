# Janus

Janus is a code-to-runtime control plane for self-hosted and distributed deployments.

It imports application code, builds a runnable WASM artifact, deploys it to a runtime target, and exposes status, logs, domains, and telemetry through a web UI and HTTP API.

## Repo Layout

- `janus-rust/` - Rust API, bounded ECS, application use cases, persistence ports, build execution, and runtime launcher
- `janus-spacetimedb/` - Rust SpacetimeDB module, reducers, and generated binding source
- `antithesis/` - account-free Rust verification, assertion events, fixtures, and local gates
- `janus-rust/web/` - Actix-served HTML/CSS/JavaScript frontend
- `docs/` - architecture, runbooks, and project documentation
- `.factory/` - validation and research artifacts
- `Janus.js` - root Bun development CLI
- `janus-rust/src/bin/janus.rs` - dedicated user-facing operator CLI

The frontend is served directly by the Rust Actix Web boundary; there is no separate frontend runtime or Qwik build.

When SpacetimeDB is configured, the API opens one managed generated-client
connection and readiness includes its connection state:

```text
JANUS_SPACETIME_URI=ws://127.0.0.1:3000
JANUS_SPACETIME_DATABASE=janus-local
JANUS_SPACETIME_TOKEN=<optional-token>
JANUS_SPACETIME_REQUIRED=true
```

`JANUS_SPACETIME_REQUIRED=true` makes startup fail closed when the authority
is not configured or cannot be reached. The current migration still hydrates
some domain projections through local adapters; those adapters are being
removed as reducer/query wiring is completed.

## Quick Start

```powershell
bun Janus.js run
```

Local endpoints:

- API: <http://localhost:8080>
- Health: <http://localhost:8080/healthz>
- Readiness: <http://localhost:8080/readyz>

Useful commands:

```powershell
bun Janus.js help
bun Janus.js backend-test
bun Janus.js stack-up
bun Janus.js web-dev
bun Janus.js tech-debt --dry-run
bun Janus.js operator --help

# Operator CLI (local API)
cargo run --manifest-path janus-rust/Cargo.toml --bin janus -- help
cargo run --manifest-path janus-rust/Cargo.toml --bin janus -- health
cargo run --manifest-path janus-rust/Cargo.toml --bin janus -- projects --json
cargo run --manifest-path janus-rust/Cargo.toml --bin janus -- projects create --name demo --slug demo --repo-url https://github.com/example/demo
cargo run --manifest-path janus-rust/Cargo.toml --bin janus -- builds submit --project-id 1 --repo-url https://github.com/example/demo
cargo run --manifest-path janus-rust/Cargo.toml --bin janus -- deployments create --project-id 1 --build-id 1
cargo run --manifest-path janus-rust/Cargo.toml --bin janus -- operations get --operation-id build.submit-1
cargo run --manifest-path janus-rust/Cargo.toml --bin janus -- runners heartbeat --runner-id 1 --lease-seconds 60
```

The operator CLI uses `JANUS_URL` (default `http://127.0.0.1:8080`) and
`JANUS_TOKEN` for authenticated requests. `janus login --email ...
--password ...` saves a local token under
`janus-rust/artifacts/operator-token` (override with `JANUS_TOKEN_FILE`).
`--json` keeps output suitable for scripts and CI. Mutation commands accept
`--idempotency-key`; retries with the same key and payload replay the accepted
result, while conflicting payloads are rejected. Set `JANUS_OPERATION_FILE` on
the Actix API to persist the local command ledger across restarts.

## Technical-debt analysis

`bun Janus.js tech-debt` scans every `.go` file under the selected directory, sends each file's source and local metrics to Jev, and returns typed technical-debt judgments with probabilities and confidence. The context includes Go package/file role, imports, sibling-test presence, deterministic risk signals, and source. Questions are atomic and fanned out per file with bounded concurrency (`TYPESAFE_CONCURRENCY`, default `4`), then Janus computes the action and priority in code. The default output is a concise actionable report; add `--json` when the full machine-readable answers and probabilities are needed. Set `TYPESAFE_API_KEY` in `.env` or `.env.local`; use `TYPESAFE_MODEL` to override the default `jev-latest` model. Pass a directory to scan a narrower scope, for example `bun Janus.js tech-debt backend`. Use `--dry-run` to inspect the file metadata and question schema without making an API request.

## Runtime Model

- `janus-api` is the single Actix Web control-plane binary and serves the frontend at `/` and `/admin`.
- The protected `/api/v1/admin/spec` endpoint serves the canonical System Specification to control-plane administrators.
- SpacetimeDB is the durable persistence target; local file stores remain deterministic development adapters.
- MinIO stores uploaded source bundles and built artifacts.
- WASM workloads are launched through the configured runtime adapter.

## Full Local Stack

```powershell
bun Janus.js stack-up
bun Janus.js stack-logs
bun Janus.js stack-down
```

The Compose stack contains SpacetimeDB, MinIO, and `janus-api`. The Actix frontend is embedded in the API image.

## Maintenance Rule

When layout, commands, or architecture change, update this README and the relevant architecture documentation in the same change.
