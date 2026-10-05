# AntiThesisLocal

`AntiThesisLocal.py` is an account-free local approximation of the Antithesis loop. It runs repeatable Rust workload trials, captures Antithesis-style JSONL events, optionally applies simple Docker Compose faults between trials, and reports assertion failures.

The Rust-native scenario is the migration gate for the replacement control
plane. It exercises deterministic lifecycle progress, duplicate and dropped
effects, replay equality, and invariant assertions without requiring Go,
Zig, Docker, or an Antithesis account:

```text
rustup run stable-x86_64-pc-windows-msvc cargo run --manifest-path janus-rust/Cargo.toml --bin antithesis-local -- --output janus-rust/artifacts/rust-assertion-events.jsonl
python .\antithesis\AntiThesisLocal.py validate-jsonl janus-rust/artifacts/rust-assertion-events.jsonl
```

Compare the Rust assertion properties with the preserved migration reference fixtures:

```text
rustup run stable-x86_64-pc-windows-msvc cargo run --manifest-path janus-rust/Cargo.toml --bin antithesis-validate -- --input janus-rust/artifacts/rust-assertion-events.jsonl
rustup run stable-x86_64-pc-windows-msvc cargo run --manifest-path janus-rust/Cargo.toml --bin antithesis-diff -- --reference antithesis/fixtures/go-operation-status.jsonl --reference antithesis/fixtures/go-deployment-runtime.jsonl --candidate janus-rust/artifacts/rust-assertion-events.jsonl
```

The comparator requires every reference property to exist in Rust, preserves
exact lifecycle status and endpoint evidence, and treats opaque runtime IDs as
implementation-specific values that must only be present and well-formed.

## Commands

From the project root:

```powershell
python .\antithesis\AntiThesisLocal.py doctor
python .\antithesis\AntiThesisLocal.py run
python .\antithesis\AntiThesisLocal.py run --trials 5
python .\antithesis\AntiThesisLocal.py report
python .\antithesis\AntiThesisLocal.py validate-jsonl
python .\antithesis\AntiThesisLocal.py hypothesis
```

Run the Rust-native workload with multiple trials:

```powershell
python .\antithesis\AntiThesisLocal.py run --trials 2
```

If the local Compose stack is running, restart a service before each trial:

```powershell
python .\antithesis\AntiThesisLocal.py run --compose-up --trials 5 --fault-service janus-worker --fault-action restart
```

Generated event files are written under `antithesis/local-events/` and ignored by Git. This tool is not a full replacement for Antithesis: it does not provide deterministic multiverse branching, platform fault scheduling, or distributed search across timelines. It is intended to turn the SDK assertions and local workload into a repeatable development loop.

## Rust property integration

The `hypothesis` command name is retained as a compatibility alias, but it
now runs the Rust-native deterministic and assertion suite. The properties are
implemented in the Rust simulator, assertion runner, replay verifier, and
JSONL validator. No Python test dependency or Antithesis account is required.

Before committing backend changes, run the Rust-native gates:

```text
python antithesis/AntiThesisLocal.py check
```

Add `--http` when the local Docker stack is running on `JANUS_LOCAL_API` to include dependency-fault testing:

```powershell
$env:JANUS_LOCAL_API = "http://127.0.0.1:18080"
python .\antithesis\AntiThesisLocal.py check --http
```

The native suite is intentionally deterministic and local-only. Docker-backed
dependency recovery and live HTTP workload testing remain separate integration
gates; the active implementation and all pre-commit verification are Rust.

For the production persistence gate, require both the CLI and generated client
output:

```text
python janus-spacetimedb/tools/generate_bindings.py --check-only --require-installed --verify-output
```
