# Janus SpacetimeDB module

This crate is the production persistence module boundary for Janus. It is
separate from `janus-core` so the bounded ECS remains testable without a
database runtime. Tables are durable state; reducers are the only mutation
path. The local `LocalSpacetimeDb` implementation in `janus-core` mirrors this
contract for offline replay and pre-deployment tests.

The module follows the current SpacetimeDB 2 Rust shape:

```text
spacetime dev
spacetime publish janus-local
spacetime generate --lang rust --out-dir ./janus-rust/src/module_bindings --module-path ./janus-spacetimedb
```

The repository-level `spacetime.json` supplies these paths and selects the
local `janus-local` database. It is safe to keep checked in; personal database
names belong in the ignored `spacetime.local.json` override.

From the repository root, the checked-in Python helper runs the same
generation command when the CLI is installed:

```text
python janus-spacetimedb/tools/generate_bindings.py
python janus-spacetimedb/tools/generate_bindings.py --check-only
```

Generated bindings belong under `janus-rust/src/module_bindings/` and must be
regenerated whenever the module schema or reducer signatures change.

The project reducers include tenant-owned, retry-safe update and delete
operations in addition to creation. Deployment and generated client bindings
are intentionally a later gate; do not treat a local file store as production
persistence.
