# Architecture standard: ports and adapters

This is the structure janus-go is built to, and the rules new code is reviewed against. The decision is recorded in [ADR 0003](adr/0003-ports-and-adapters.md); the evidence is in [`Research/hexagonal-ports-and-adapters.md`](../../../Research/hexagonal-ports-and-adapters.md) and, for the concurrency choices, [`experiments/store-concurrency/REPORT.md`](../experiments/store-concurrency/REPORT.md).

**Status:** the target. The code today still lives in `actors/` on GoAkt. Section 11 is the migration order, and it replaces the actor-restructuring plan in ENG-61 from ENG-66 onward. Until a package has moved, the old layout applies to it; new packages follow this document.

## 1. The idea in four lines

- The **core** holds the Janus rules and use cases. It imports no I/O.
- Everything that touches the world sits behind a **port**: a small interface named for the purpose of the conversation, declared by the core.
- An **adapter** implements a port for one technology (SQL, Docker, GitHub, Wasmtime, in-memory), or drives the core from outside (HTTP, tests).
- `cmd/janus` is the only place that knows which adapters exist. It wires them to the core.

## 2. Vocabulary

| term | meaning here |
|---|---|
| core | `internal/domain` and `internal/app`. Pure Go, no I/O imports |
| use case | a method on a type in `internal/app`, such as `Imports.Request`. The application's interface to the outside |
| primary (driving) side | the world calls us: HTTP requests, tests, the startup resume. Served by driving adapters |
| secondary (driven) side | we call the world: database, Docker, GitHub, Wasmtime, audit sink. Reached through ports |
| port | a Go interface declared in `internal/app`, named for the purpose of the conversation |
| adapter | a package under `internal/adapters` that implements a port or drives a use case |
| test adapter | a real implementation of a port that runs in memory, kept in a `<port>test` package |
| composition root | `cmd/janus`: builds adapters, passes them to use cases, starts and stops in order |

These match the module vocabulary used elsewhere in this repo: a port is the **interface** of a **seam**, an adapter is a thing that satisfies it, and a deep module hides a lot behind a small interface.

## 3. Layout

```
cmd/janus/                      composition root: config, wiring, ordered start and stop
internal/
  domain/                       pure types and rules (job state machines, Artifact format,
                                Templates, scan, Usage). Standard library and pure libraries only
  app/                          use cases and the ports they need (ports.go)
  adapters/
    httpapi/                    driving: JSON API, HTML pages, HTTP server lifecycle
    sqlstore/                   driven: Store over *sql.DB (Turso)
    storetest/                  test adapter and contract suite for Store
    dockerbuild/                driven: Builder, one adapter per Template
    buildertest/                test adapter and contract suite for Builder
    wasmtime/                   driven: Runner (cgo; the no-cgo stub is a build-tagged file)
    runnertest/
    githubapi/                  driven: GitHost
    gitsource/                  driven: Source (clone and list files)
    hosttest/                   test adapter for GitHost and Source
    auditlog/                   driven: Audit (zap)
    audittest/                  recorder
```

Rules for the tree:

- Everything is under `internal/`, as Go's server-project guidance recommends. Nothing here is a public API.
- Package names say what the package provides. No `util`, `common`, `types`, `interfaces` or `model`.
- `app` is one package until it hurts. Split a use case into its own package only when its files and tests are hard to navigate, and say why in the commit.
- A port with a single adapter and no test adapter stays a concrete type until a second one exists (section 5).

## 4. Dependency rules

1. `domain` imports only the standard library and pure libraries. It imports nothing else from this repository.
2. `app` imports `domain` and pure libraries (uuid, argon2, JWT, OPA, zstd, the OpenTelemetry API). It never imports an adapter, `net/http`, `database/sql`, `os/exec`, a Docker or Git client, Wasmtime, or any actor framework.
3. An adapter imports `app` and `domain`. An adapter never imports another adapter, `cmd`, or a test adapter, except that a `<port>test` package may be imported by tests. `internal/archtest` checks non-test imports only, so that exception holds by construction.
4. `cmd/janus` is the only package that imports concrete adapters.
5. Dependencies point inward: `cmd` and adapters toward `app`, `app` toward `domain`.

`internal/archtest` ([archtest_test.go](../internal/archtest/archtest_test.go)) enforces rules 1 to 4 with a Go test over the `go list` import graph and fails the build on a violation. Fixture packages under its `testdata/` break each rule to prove the check works. The allow-list of pure libraries lives in that test, so adding one is a visible review decision.

## 5. Ports

**Declare ports where they are used.** A port is a Go interface in `internal/app/ports.go`, because `app` is its consumer. It lists only the methods `app` calls, and the implementing adapter returns its concrete type.

**Name a port for the purpose of its conversation**, not for the technology: `Store`, `Builder`, `Runner`, `Source`, `GitHost`, `Audit`. The count is not a target; add a port when a new kind of conversation appears.

**A seam earns a port when something varies across it.** One real adapter and no test adapter means the seam is hypothetical: use a concrete type. Two adapters, a real one and a test adapter, or two real ones, make it real. Authentication (argon2, JWT) and authorization (OPA) have one implementation each and stay concrete modules inside `app`.

**Keep interfaces small and document the contract:** ordering, error values, what absence looks like (a found flag, not an error), concurrency expectations, and timeouts. A port's contract is tested by its contract suite (section 9).

### The current ports

| port | purpose | real adapter | test adapter |
|---|---|---|---|
| `Store` | persist Accounts, Projects, Snapshots, jobs, Builds | `sqlstore` | `storetest.Memory` |
| `Builder` | compile a workspace to an Artifact | `dockerbuild` | `buildertest.Fake` |
| `Runner` | run a WASM module with limits | `wasmtime` (cgo, no-cgo stub) | `runnertest.Fake` |
| `Source` | fetch a repository at a commit | `gitsource` | `hosttest.Source` (local directory) |
| `GitHost` | list repositories, branches, commits | `githubapi` | `hosttest.Host` |
| `Audit` | record an Audit Event | `auditlog` | `audittest.Recorder` |

### One Store

Persistence is one `Store` port. It stays one until splitting proves its worth. Revisit when any of these happens:

- a new family of records with no references to the others arrives (the billing ledger in M3 is the expected one) and would add methods to every test adapter;
- a use case needs only a small fraction of `Store` and its tests spend most of their setup on the rest;
- one family needs a second storage technology.

Splitting follows ENG-67 (identity, projects and files, jobs) and keeps `DeleteAccount` as a method of the port that owns the cascade.

### Job state

The legal moves of an import or build live in `domain` as data and a `CanMove(from, to)` rule. The port only compares and swaps: `Transition(id, from, to, ...)` succeeds if the stored status is still `from`. The SQL adapter does `UPDATE ... WHERE status = ?` and the in-memory adapter does a plain compare. Neither adapter owns the state machine.

## 6. Use cases (the primary side)

- A use case is a method on a struct in `app` (`Accounts`, `Projects`, `Imports`, `Builds`, `Runs`). The struct holds its ports and is built with a constructor that takes them. Its methods are the application's interface; tests and HTTP call the same ones.
- No interface for a use case until a second caller needs to substitute it. HTTP is not a reason.
- `context.Context` is the first parameter and is never stored in a struct. Methods return domain values and typed errors, never HTTP types, SQL rows or adapter errors.
- A use case does not read the environment or the clock directly; take a function or a value when time matters.
- Rules live in `domain` or in the use case, never in an adapter.

## 7. Adapters

- An adapter translates: it maps domain values to its technology and its failures to the port's documented errors. It contains no business rules.
- A driving adapter (`httpapi`) turns a request into a use-case call and a result into a response. It owns sessions-as-cookies, status codes, pagination parameters and HTML. Authorization is a use-case concern.
- Configuration arrives through the constructor. An adapter does not read environment variables; `cmd/janus` does.
- Isolation settings for untrusted code (no network, read-only root, capped memory and processes) belong to the adapter that runs it, with a test that pins them.
- A build-tagged alternative (Wasmtime with cgo, the stub without) is one adapter package with tagged files, not two adapters.
- Where a real client has a server, test it against a test server (`httptest`, a temp Git repository) rather than a hand-written client double.

## 8. Concurrency inside the core

GoAkt is not used. The rules come from [the measurements](../experiments/store-concurrency/REPORT.md):

- **Shared state gets a mutex.** Keep critical sections to map or field operations. Never call a port, do I/O or block on a channel while holding a lock.
- **Counters and flags are atomics** (`atomic.Int32`, `atomic.Bool`). Use compare-and-swap when a limit is involved.
- **A channel is for a real hand-off:** a queue with a drop-or-block policy, a shutdown signal, a semaphore. Do not build a goroutine that owns a map and answers over a channel; that is a slower mutex.
- **Atomic slots and copy-on-write snapshots are for hot, read-mostly, single-record data** such as the edge-proxy route table or per-Deployment usage counters. They cannot protect a rule that spans records; those use a lock or a transaction.
- **The single database connection is the throughput limit** for anything backed by `Store`; do not add sharding or caching in front of it without a measurement.
- Every goroutine has an owner that can wait for it. Types that start work expose `Drain(ctx)` or `Close()`, and `cmd/janus` stops them in reverse order of starting.
- A goroutine that runs untrusted or fallible work recovers panics and reports them as a failed job; an unrecovered panic in the process is a bug.

## 9. Testing standard

Two layers, both required.

**Fast tests** drive the core through the use-case methods with test adapters behind the ports. They are deterministic, need no Docker, network or database file, and do not poll with `time.Sleep`: use the use case's own wait or drain method. Driving adapters are tested with `httptest` against use cases built on test adapters.

**Live tests** exercise each real adapter against the real thing: Turso, Docker builders, Wasmtime, GitHub with a dedicated test Account and repository, and, for M1, a real Headscale, the real Janus Agent on a real tailnet and real Cloudflare DNS and TLS on a test subdomain (ENG-79). They are tagged so the normal suite skips them, and they clean up what they create. A test adapter never counts as the live test of the behaviour it stands in for.

**Contract suites.** Each port with more than one adapter has a suite in its `<port>test` package, shaped like `testing/fstest`: the package ships a test adapter and an exported `Run(t, newAdapter)` that checks the port's documented contract. Every adapter, real and test, runs it. `storetest.Run` is the suite for `Store`, and `sqlstore` and `storetest.Memory` both call it. A behaviour that differs between adapters is a bug in one of them or a gap in the contract; fix the suite first.

**Test adapters are real implementations**, not hooks inside production packages. Production packages export nothing only for tests.

## 10. Adding something new

1. Is it a rule or a type with no I/O? Put it in `domain`.
2. Is it a new thing the application does? Add or extend a use case in `app`, driven by a test through its method.
3. Does it need the outside world? Check the table in section 5. Reuse a port, or add one named for the conversation, with a contract and a test adapter in the same change.
4. Add the adapter and wire it in `cmd/janus`. Nothing else imports it.
5. Run the architecture test. Add a live test if the adapter is new.

## 11. Migration

Each step ships on its own and keeps the HTTP end-to-end tests green. Until the last step the old and new layouts coexist.

1. **Guard rail:** add `internal/archtest` with today's violations allow-listed, so the rules only tighten.
2. **Domain:** move the job state machines, Artifact packing, Templates and scan into `internal/domain` (pure moves). Done in ENG-103 as the subpackages `domain/artifact`, `domain/templates`, `domain/jobs`, `domain/scan` and `domain/usage`; the old packages keep aliases where callers still use them.
3. **Ports:** declare `Store`, `Builder`, `Runner`, `Source`, `GitHost` and `Audit` in `internal/app/ports.go`, starting from the interfaces that already exist. Move the in-memory Store and its contract tests (added by ENG-66) to `storetest`.
4. **Store:** replace the persistence actor with `sqlstore` over `*sql.DB`; delete the messages, the client and the actor switch (ENG-66, ENG-67).
5. **Use cases:** turn the `projects`, `wasm` and `wasmruntime` actors into `Imports`, `Builds` and `Runs`; keep `jobrunner` as shared support inside `app`. Account deletion becomes its own use case (ENG-69).
6. **HTTP:** move `routes` and `web` to `adapters/httpapi`, calling use cases directly.
7. **Root:** build `cmd/janus` with an ordered component list (ENG-71), rename accounting to audit (ENG-70), delete the `application` and `httpserver` actors, and remove GoAkt from `go.mod`. The `actors/` folder is gone.

Current packages map as follows: `persistence` to `sqlstore` and `storetest`; `projects`, `wasm`, `wasmruntime`, `jobrunner` to `app`; `artifact`, `templates` to `domain`; `builder` and `docker` to `dockerbuild`; `github` to `githubapi` and `gitsource`; `accounting` to `Audit` and `auditlog`; `authentication` and `authorization` to `app`; `routes`, `httpserver` and `web` to `httpapi`; `application` and `config` to `cmd/janus`; `telemetry` helpers stay importable (OpenTelemetry API only) with setup in `cmd/janus`.

## 12. Open points

- **Package split of `app`.** Revisit when a use case's files and tests are hard to navigate.
- **Store split.** See section 5 for the triggers.
- **Multi-node control plane.** GoAkt's clustering was the one thing actors offered that Janus does not have elsewhere. If the Distributed Network (ENG-56) needs more than one control-plane node, decide the mechanism then; it will be an adapter or a new port, not a reason to put actors back in the core.
