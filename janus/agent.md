# Janus Project Agent Guide
## Mission
Janus is a **Rust control-plane system**.
All new architecture, implementation, testing, verification, persistence integration, performance work, and cybersecurity hardening should focus on `janus-rust`.
The authoritative system specification is:
`Janus_Rust_System_Spec.html`
The specification and Rust implementation must remain synchronized in the same change.
Go and Zig were the migration/reference implementations. Their source is archived at
`artifacts/legacy-implementations-20261001.zip` with `legacy/MANIFEST.sha256` and
is no longer part of the active implementation tree.
The Rust tests, fixtures, assertions, and system specification now contain the
behavioral contract required for normal Janus development.
---
## 1. Source of Truth
Rust is the implementation source of truth for Janus going forward.
Use this priority order:
1. `Janus_Rust_System_Spec.html` — intended system behavior and architecture.
2. Rust implementation — current executable behavior.
3. Rust tests, fixtures, and assertions — verified behavioral contract.
4. Go/Zig — migration references for behavior not yet captured above.
If the Rust implementation and specification disagree, investigate the discrepancy rather than silently choosing one.
When legacy behavior is intentionally preserved, encode it in Rust tests or fixtures so future work no longer requires consulting the legacy implementation.
When legacy behavior is intentionally rejected or changed, document the Rust behavior in the specification.
---
## 2. Non-Negotiable Rust Invariants
### Correctness and determinism
- Preserve the actor-owned Entity Component System model.
- Keep world, mailbox, resource, trace, and simulation capacities bounded.
- Keep replay and snapshot behavior deterministic.
- Use explicit lifecycle transitions.
- Use classified errors.
- Treat command identities as idempotency keys.
- Fence worker and runtime callbacks by generation.
- Do not introduce `unsafe` Rust.
### Security and ownership
- Enforce tenant ownership, authentication, authorization, and permissions **before state mutation**.
- Deny by default at trust boundaries; grant only the minimum capability required for the operation.
- Treat all external input, persisted data, generated bindings, callbacks, and cross-tenant identifiers as untrusted until validated.
- Preserve tenant isolation across HTTP, ECS messages, persistence, caches, traces, assertions, and background work.
- Never log, trace, assert, or return secrets, credentials, session material, private keys, access tokens, or sensitive tenant payloads unless the specification explicitly requires a safe redacted form.
- Prefer fixed-width identifiers.
- Prefer explicit ownership over implicit global state.
- Do not introduce hard-coded credentials, default passwords, embedded secrets, or security-sensitive configuration that silently falls back to an insecure value.
- Do not weaken correctness, consistency, security, determinism, or required safety guarantees for performance.
- Security failures must be explicit, classified, observable, and fail closed where continuing could violate authorization, integrity, confidentiality, or tenant isolation.
### Boundedness
Core control-plane structures must have explicit capacity policies.
Do not introduce an unbounded:
- queue;
- mailbox;
- collection;
- retry loop;
- trace buffer;
- resource pool;
- simulation structure;
- persistence fallback;
- background task accumulation.
When introducing a bounded resource, define its capacity and exhaustion behavior explicitly.
---
## 3. Cybersecurity Baseline
Janus cybersecurity work must use a layered, risk-based approach. The default engineering baseline combines:
- NIST Cybersecurity Framework 2.0 for lifecycle coverage: Govern, Identify, Protect, Detect, Respond, and Recover.
- CIS Critical Security Controls for prioritized technical hygiene, beginning with applicable Implementation Group 1 safeguards and increasing rigor when the threat model or deployment requires it.
- MITRE ATT&CK for threat-informed analysis, abuse-case design, detection coverage, and adversary-behavior validation.
- ISO/IEC 27001 principles for risk-based protection of confidentiality, integrity, and availability.
- Regulatory or contractual frameworks such as CMMC, NIST SP 800-53, PCI DSS, HIPAA, SOC 2, GLBA, FISMA, or FFIEC only when Janus, its deployment, its data, or its customer obligations are actually in scope.
Do not claim compliance, certification, framework conformance, or control coverage unless the required controls and evidence have been explicitly mapped and verified.
### Govern
For every security-relevant change:
1. identify the asset, data, tenant, privilege, or trust boundary affected;
2. identify the threat or failure mode being addressed;
3. identify the applicable security requirement or control family;
4. assign an explicit owner for the control or invariant;
5. document security assumptions that would make the design unsafe if violated;
6. update the system specification when security behavior, boundaries, or verification change.
Security decisions must be traceable to explicit architecture, tests, assertions, configuration, or verification evidence. Security must not depend on undocumented operator knowledge.
### Identify
Maintain an explicit understanding of:
- externally reachable HTTP routes and services;
- privileged operations;
- authentication and authorization boundaries;
- tenant-scoped data and identifiers;
- secrets and credentials;
- persistence tables and reducers containing security-sensitive data;
- dependencies and generated artifacts;
- network and process trust boundaries;
- data flows that cross components, tenants, processes, machines, or persistence boundaries.
When a change introduces a new asset, dependency, route, privilege, data class, integration, or trust boundary, update the relevant specification and threat model in the same change.
### Protect
Apply least privilege and secure-by-default behavior.
Required rules:
- authenticate before authorizing whenever identity is required;
- authorize before mutation or disclosure;
- validate identifiers, lengths, ranges, encodings, enums, and structural invariants at trust boundaries;
- reject ambiguous, malformed, stale-generation, cross-tenant, replayed, or unauthorized requests;
- use idempotency controls for commands that may be retried;
- protect secrets at rest and in transit using platform-appropriate mechanisms;
- avoid exposing internal errors, stack details, secrets, or sensitive tenant state through HTTP responses;
- use dependency features conservatively and avoid unnecessary attack surface;
- pin, review, and update security-sensitive dependencies deliberately;
- preserve bounded resource policies to reduce denial-of-service risk;
- rate-limit, quota, backpressure, or reject work where an attacker could otherwise force unbounded CPU, memory, storage, network, retry, or task consumption;
- prefer safe parsing and typed validation over ad hoc string handling;
- do not use unsafe Rust to bypass ownership, lifetime, or memory-safety guarantees.
### Detect
Security-relevant behavior must be observable without leaking secrets.
Provide deterministic, bounded telemetry or assertion evidence for applicable:
- authentication failures;
- authorization and tenant-isolation failures;
- repeated malformed or rejected requests;
- replay or duplicate-command handling;
- generation-fence violations;
- capacity exhaustion and rate-limit events;
- persistence integrity failures;
- suspicious lifecycle transitions;
- security control failures.
Logs and assertion events must contain enough stable context for investigation while excluding secrets and unnecessary sensitive payloads.
### Respond
Security failures must have explicit handling paths.
For applicable incidents or detected violations:
- contain the affected operation or tenant boundary;
- fail closed when authorization, integrity, or confidentiality is uncertain;
- preserve forensic evidence in bounded logs/assertions;
- avoid destructive cleanup that erases the cause before it is recorded;
- classify whether retry is safe, unsafe, or requires operator intervention;
- define revocation, fencing, invalidation, or restart behavior for compromised credentials, workers, generations, or sessions where applicable.
Do not automatically retry authentication, authorization, integrity, or policy failures as if they were transient infrastructure failures.
### Recover
Recovery behavior must preserve security invariants.
Applicable recovery tests must verify:
- snapshots do not restore stale authority or cross-tenant state;
- replay preserves authorization and idempotency semantics;
- credential or session invalidation remains effective after restart;
- generation fencing prevents stale workers from regaining authority;
- persistence recovery does not bypass validation or authorization;
- snapshot restore explicitly requeues running builds and starting deployments,
  clears stale process claims, and preserves already-running runtime identity;
- the local `FileRecoveryQueue` persists recovery jobs with tenant and
  generation metadata, uses distinct build/deployment identities, treats the
  same generation as idempotent, and rejects stale-generation reuse;
- `FileAuthStore` must receive its encryption secret from the application
  configuration adapter; persistence code must not read `JANUS_*` variables
  directly. The single process-environment adapter is explicitly marked and
  remains a TigerStyle review boundary.
- degraded or fallback modes do not silently disable required security controls.
### Threat modeling
Perform lightweight threat modeling for every change that adds or materially changes:
- an HTTP route;
- authentication or authorization;
- tenant-visible data;
- persistence schema or reducer;
- external integration;
- background worker;
- generated client boundary;
- privileged command;
- file, network, or process I/O;
- cryptographic or secret-handling behavior.
At minimum, consider:
- spoofing or identity confusion;
- cross-tenant access;
- tampering or integrity loss;
- replay and duplicate execution;
- privilege escalation;
- information disclosure;
- denial of service or resource exhaustion;
- stale-generation or confused-deputy behavior;
- unsafe recovery or rollback;
- dependency or supply-chain compromise.
Use MITRE ATT&CK techniques when they help turn realistic adversary behavior into tests, detections, or validation scenarios.
### Security evidence
Security controls are not complete merely because code exists.
For each applicable control, keep evidence through one or more of:
- Rust tests;
- scenario tests;
- fault-injection tests;
- JSONL assertions;
- configuration checks;
- dependency or vulnerability scans;
- reproducible verification commands;
- specification sections;
- documented operator procedures when automation is not possible.
Security verification must be repeatable and suitable for later audit or incident review.
---
## 4. Required Workflow
### Before editing
1. Inspect existing user changes before modifying overlapping files.
2. Read the relevant Rust modules and tests.
3. Read the relevant section of `Janus_Rust_System_Spec.html`.
4. Identify the invariants affected by the change.
5. Determine the required tests, assertions, and verification commands.
6. Identify affected trust boundaries, privileges, tenant data, secrets, and externally controlled inputs.
7. Determine whether the change requires threat-model updates, security tests, dependency review, or compliance-control evidence.
Consult Go or Zig only if Rust and the specification do not provide enough information to determine the intended behavior.
Do not overwrite or undo unrelated user changes.
### During implementation
- Keep changes bounded to the requested scope.
- Use `apply_patch` for source and documentation edits.
- Prefer Python for new project automation scripts.
- Do not add PowerShell automation scripts.
- Keep functions bounded and readable.
- Follow the repository's TigerStyle requirements.
- Add tests alongside new behavior.
- Add negative and abuse-case tests for new trust boundaries, authorization rules, parsers, limits, or privileged behavior.
- Keep secrets out of source, fixtures, logs, assertions, snapshots, and example configuration.
- Treat new dependencies, features, network listeners, and external integrations as security-scope increases that require justification.
- Update the system specification in the same change when required.
Prefer improving the Rust architecture over reproducing accidental limitations of a legacy implementation.
---
## 5. Rust Architecture Contract
### ECS and execution
Rust must preserve the actor-owned ECS architecture.
State mutation must remain:
- explicit;
- bounded;
- ownership-aware;
- generation-safe;
- deterministic where required.
Avoid hidden mutation through global state or uncontrolled background work.
### HTTP
Use Actix Web for the production HTTP server.
Keep the framework-independent `HttpService` contract independently testable.
Transport and framework concerns should remain outside core control-plane behavior where practical.
Core behavior must not require an HTTP server to be tested.
HTTP handlers must treat all request-controlled data as untrusted.
For security-sensitive routes:
- authenticate and authorize before reading or mutating protected tenant state;
- enforce explicit body, header, identifier, and request-size limits;
- validate content types and structured payloads;
- use bounded timeouts and cancellation behavior;
- avoid reflecting secrets or internal error details;
- apply rate limits, quotas, or backpressure where abuse could create resource exhaustion;
- test unauthorized, cross-tenant, malformed, oversized, replayed, and stale-generation requests.
### Persistence
SpacetimeDB is the production persistence target.
Durable mutations belong behind explicit:
- tables;
- reducers;
- generated client bindings.
Every bounded control-plane record must ultimately be authoritative in
SpacetimeDB, including identity, authorization, lifecycle, queue, command,
idempotency, audit, telemetry-index, runtime, and object-metadata state. Actix
must hydrate from SpacetimeDB and route production mutations through reducers;
in-memory services and file stores are local deterministic test fallbacks only.
Large source bundles, artifacts, log bodies, trace bodies, and profile bodies
may remain external, but their ownership, checksum, lifecycle, retention, and
correlation metadata must be stored in SpacetimeDB. See
`docs/spacetimedb-authoritative-storage.md` for the inventory and migration
sequence.
Object uploads must write durable metadata with tenant ownership, checksum,
size, content type, status, creation time, and retention deadline. The external
body is not authoritative: failed uploads remove the metadata, while startup
retention reconciliation marks expired metadata before deleting the external
body and removes the durable row only after body cleanup succeeds. Cleanup
failures must leave the row available for a later retry; never silently orphan
or delete the durable lifecycle record first. Source and artifact uploads must
also populate the tenant-scoped artifact metadata projection, and its retention
status must be reconciled with a reducer before external cleanup.
At Actix startup, expired durable job leases must be reconciled by a
SpacetimeDB reducer before projections are hydrated. HTTP command completion
must resolve the durable command record directly; a process-local operation
projection cannot be required for durable completion after restart. Durable
worker execution must claim, perform, and complete work under the reducer-owned
lease and generation fence.
Email OTP challenge hashes and expirations are private SpacetimeDB state when
the generated client is configured; the in-memory challenge store is allowed
only for offline deterministic fallback tests.
Email delivery intents must be admitted to the durable outbox and provider
claims must transition through SpacetimeDB; failed delivery returns to
pending or terminally failed according to the reducer-owned retry budget.
Runner registration, heartbeat, listing, claim, and release must use the
durable runner reducers and subscribed runner projection whenever SpacetimeDB
is configured; the local runner registry is only a deterministic offline
fallback/cache. Startup must run the durable runner-expiration reducer before
hydrating the local projection.
Authenticated project listing and project-owned authorization checks must read
the subscribed durable project projection when SpacetimeDB is configured; the
local ECS service is not an independent read authority.
Authenticated build listing, build detail, and build-log reads must use the
subscribed durable build projection when SpacetimeDB is configured. Authenticated
deployment listing, detail, and domain projections must use the subscribed
durable deployment/project projections under the same rule. Worker execution
has a reducer-owned `DurableJobPort`/`DurableJobHandler` seam in
`durable_worker.rs`; deterministic `build.enqueue` and `deployment.create`
handlers are wired to generation-fenced lifecycle and operation reducers. Do
not claim the worker migration complete until external provider/runner handlers
are wired to the same seam and update lifecycle projections through reducers.
Bounded file stores are local deterministic fallbacks only. Do not evolve them into an alternative production persistence architecture.
Source uploads also create the durable build projection with the uploaded
object key and source reference before admitting the generation-fenced build
job; the local build service is only the bounded compatibility projection.
`janus-spacetimedb/tools/generate_bindings.py` is the single binding-generation entry point.
After a schema or reducer-signature change:
1. regenerate bindings;
2. run relevant Rust verification;
3. record the SpacetimeDB CLI version in the verification report.
Do not manually maintain generated bindings when the generator is authoritative.
Persistence security requirements:
- authorize durable reads and mutations at the boundary that owns the data;
- preserve tenant identity and ownership in durable records where required for isolation;
- validate data again when crossing a persistence trust boundary rather than assuming stored data is safe forever;
- do not persist plaintext secrets unless the specification explicitly requires it and the storage design provides appropriate protection;
- make migrations and reducers fail explicitly rather than silently dropping security-relevant fields or constraints;
- test rollback, replay, duplicate submission, stale-generation, and cross-tenant persistence behavior where applicable.
---
## 6. Rust Testing and Assertions
Every new behavior should have Rust-native verification appropriate to its boundary.
Use:
- unit tests for local behavior;
- integration tests for component boundaries;
- scenario tests for workflows and lifecycle behavior;
- differential fixtures only while migrating behavior that still requires a legacy reference.
### Assertion contract
Important invariants must emit local JSONL assertion events through `AssertionWriter` or the local runner.
- Record assertion failures **before** terminating because of them.
- Keep assertion messages stable and descriptive.
- Treat assertion schema changes as specification changes.
Tests should cover applicable:
- positive behavior;
- negative behavior;
- capacity boundaries;
- lifecycle transitions;
- replay;
- idempotency;
- generation fencing;
- permission failures;
- fault injection;
- authentication failures;
- authorization bypass attempts;
- cross-tenant access attempts;
- malformed and adversarial input;
- oversized input and resource exhaustion;
- replay and duplicate requests;
- secret-redaction behavior;
- insecure fallback prevention;
- recovery after security-sensitive state changes.
### Legacy migration
When consulting Go or Zig to recover behavior:
1. identify the behavior being migrated;
2. encode it as a Rust test, fixture, or assertion;
3. implement or verify the Rust behavior;
4. update the specification if necessary.
Once behavior is captured by the Rust contract, future work should use the Rust contract rather than repeatedly consulting the legacy implementation.
---
## 7. Specification Synchronization
For every Rust implementation change, determine whether it changes:
- entity or component;
- command;
- event;
- effect;
- lifecycle transition;
- public module;
- binary;
- route;
- handler behavior;
- authentication;
- authorization;
- capacity;
- persistence behavior;
- error policy;
- assertion;
- adapter boundary;
- verification procedure;
- threat model;
- trust boundary;
- secret or credential handling;
- rate limit, quota, timeout, or backpressure policy;
- security telemetry or incident-response behavior;
- applicable cybersecurity framework or compliance mapping.
If the existing specification would become inaccurate or incomplete, update `Janus_Rust_System_Spec.html` in the same change.
The specification must remain current for:
- Rust architecture;
- public Rust modules and binaries;
- ECS entities and components;
- lifecycle transitions;
- HTTP behavior;
- security boundaries;
- capacity policies;
- persistence architecture;
- JSONL assertion schema;
- verification commands;
- supported behavior;
- known incomplete or pending Rust scope.
Do not preserve legacy implementation details in the specification unless they remain relevant to the Rust architecture.
---
## 8. Performance and Scalability
Optimize Janus as a Rust system, but investigate architecture before implementation details.
Before optimizing, ask:
> **Is the Rust implementation slow, or does the interface make it impossible for the implementation to be fast?**
Investigate in this order:
**Workload → Interface → Coordination → Data movement → Architecture → Algorithm → Rust implementation → Hardware**
### Required analysis
Before implementing a performance or scalability change:
1. Model the real workload:
   - concurrency;
   - contention;
   - latency;
   - traffic distribution;
   - data movement;
   - batching opportunities;
   - durability and replication requirements;
   - expected growth.
2. Identify the serial path:
   - locks;
   - network round trips;
   - synchronization;
   - consensus;
   - shared resources;
   - dependent operations.
3. Estimate where applicable:
   - latency;
   - contention;
   - serialization cost;
   - throughput ceiling;
   - coordination cost.
4. Inspect the interface before:
   - rewriting Rust code;
   - tuning allocation or memory;
   - adding caches;
   - increasing concurrency;
   - adding shards;
   - adding queues;
   - adding retries;
   - changing hardware.
### Design rules
- Do not assume horizontal scaling fixes serialization or contention.
- Minimize coordination across boundaries.
- Avoid holding locks, transactions, or scarce resources while waiting on network communication when avoidable.
- Move complete operations to the boundary capable of executing them instead of repeatedly moving data across the boundary.
- Batch operations when semantics permit.
- Specialize high-volume interfaces when justified by measured workload.
- Benchmark realistic contention, network latency, concurrency, replication, durability, and traffic distributions.
Prefer architectural simplification over accumulating caches, queues, retries, synchronization, or low-level optimization complexity.
Performance changes must include an abuse-case review. Increasing concurrency, queue depth, cache size, retry budgets, batching, connection pools, or request limits must not create a practical denial-of-service path or weaken authentication, authorization, isolation, logging, or recovery guarantees.
---
## 9. Rust Verification
### Windows
Use the installed MSVC toolchain explicitly:
```powershell
rustup run stable-x86_64-pc-windows-msvc cargo fmt --manifest-path janus-rust/Cargo.toml -- --check
rustup run stable-x86_64-pc-windows-msvc cargo test --manifest-path janus-rust/Cargo.toml
rustup run stable-x86_64-pc-windows-msvc cargo clippy --manifest-path janus-rust/Cargo.toml --all-targets -- -D warnings
rustup run stable-x86_64-pc-windows-msvc cargo run --manifest-path janus-rust/Cargo.toml --bin antithesis-local
rustup run stable-x86_64-pc-windows-msvc cargo run --manifest-path janus-rust/Cargo.toml --bin tigerstyle
python antithesis/AntiThesisLocal.py cutover
```
TigerStyle hard findings must remain **zero**.
Narrow tests may be used during development, but they do not replace applicable final verification.
If a required command cannot be run, report it explicitly.
Never claim a check passed unless it was actually executed successfully.
### Security verification
When applicable to the change, also run or provide equivalent project-approved checks for:
- dependency vulnerability and advisory review;
- secret scanning;
- static analysis or security linting;
- authentication and authorization tests;
- tenant-isolation tests;
- malformed, oversized, replay, and resource-exhaustion tests;
- security-sensitive recovery and persistence tests.
If the repository does not yet provide an automated command for a required security check, do not invent a pass. Record the gap and add a bounded, reproducible verification method when within scope.
A vulnerability finding must be classified by reachability, impact, exploit preconditions, and affected boundary before being accepted, mitigated, or deferred.
---
## 10. Change Discipline
- Inspect existing user changes before editing overlapping files.
- Do not revert unrelated changes.
- Use `apply_patch` for source and documentation edits.
- Prefer Python for project automation.
- Do not add PowerShell automation scripts.
- Keep functions bounded and readable.
- Avoid unnecessary dependencies.
- Avoid introducing abstraction without a concrete boundary or repeated use case.
- Prefer explicit state and ownership.
- Prefer deterministic behavior.
- Prefer compile-time enforcement when practical.
- Keep failure behavior explicit.
- Keep resource consumption bounded.

### Trace verification metrics

The trace viewer in `Janus_Rust_System_Spec.html` must display evidence from the latest local Rust verification run, not hand-maintained counts. `python antithesis/AntiThesisLocal.py check` updates `janus-rust/artifacts/trace-metrics.json` from the Rust test output, TigerStyle audit, assertion JSONL, and differential verifier, then synchronizes the embedded metrics snapshot in the spec HTML. When adding or changing verification commands, update that metrics extraction path and keep the generated snapshot reproducible. Do not edit the embedded metrics values manually.

The specification's warning/error ledger is a live work queue, not a historical summary. It must expose aggregate counts plus every non-zero TigerStyle rule, Clippy diagnostic category, and individual strict-lint diagnostic, with the current remediation or exit condition. A ledger row may be hidden only when the latest local gate measures that category at zero; it must reappear when a later run finds it again. SpacetimeDB binding/tool prerequisites follow the same rule. Never replace an unresolved finding with prose that implies completion, and never use an allow, baseline refresh, or stale embedded metric to make the ledger appear clean.

- Prefer least privilege and deny-by-default behavior.
- Do not weaken validation, authentication, authorization, isolation, encryption, auditing, or recovery controls for convenience.
- Minimize new dependencies and review the security impact of dependency features.
- Never commit credentials, access tokens, private keys, production secrets, or sensitive customer data.
- Keep security-relevant configuration explicit; insecure fallback must not occur silently.
When choosing between preserving a legacy implementation detail and producing a cleaner Rust architecture, preserve the required **behavioral contract**, not accidental implementation structure.
---
## 11. Frontend
The Actix Web frontend is part of the Rust control-plane boundary.
When a task includes frontend changes, follow the installed `modern-frontend-design` skill and keep the shared embedded design system in:
`janus-rust/web/app.css`, `janus-rust/web/index.html`, and `janus-rust/web/app.js`.
### Requirements
- Define colors as OKLCH tokens.
- Do not add hardcoded hex, RGB, or HSL colors to TSX components.
- Reuse shared liquid-glass, bento, and spacing primitives.
- Prefer native CSS scroll-driven animation and View Transitions where appropriate.
- Include reduced-motion behavior.
- Include keyboard focus states.
- Use semantic labels.
- Use touch-sized controls.
- Do not expose secrets, privileged tokens, internal-only identifiers, or sensitive backend error details in client code.
- Treat browser-controlled state as untrusted; backend authorization remains authoritative.
- Preserve CSRF, origin, session, and authentication protections appropriate to the deployment model.
- Avoid unsafe HTML injection and sanitize or structurally render untrusted content.
- Do not weaken Content Security Policy or related browser protections without a documented reason and review.
After frontend changes, run the Rust API tests, maximum Clippy, and the local verification gate. The frontend is compiled into the Actix binary through `include_str!`; no Node/Qwik build is required.
Frontend work must not unnecessarily alter the Rust control-plane architecture.
---
## 12. Definition of Done
A Rust change is complete only when all applicable conditions are satisfied:
- requested Rust behavior is implemented;
- architecture remains consistent with the Rust system specification;
- relevant authentication, authorization, tenant-isolation, and validation checks occur before protected read, disclosure, or mutation;
- affected trust boundaries and threat scenarios were reviewed;
- boundedness is preserved;
- deterministic behavior is preserved where required;
- lifecycle transitions remain explicit;
- idempotency behavior is preserved;
- generation fencing is preserved where applicable;
- new behavior has Rust-native tests;
- important invariants have assertion coverage;
- applicable abuse cases and negative security paths have Rust-native tests;
- secrets and sensitive payloads are excluded or redacted from logs, traces, assertions, fixtures, and responses;
- new or changed external inputs have explicit validation and bounded size/resource policies;
- new dependencies or dependency features were security-reviewed;
- applicable security verification was executed and evidence recorded;
- legacy behavior consulted during migration has been captured in Rust tests or fixtures;
- generated bindings were regenerated when required;
- the system specification is synchronized;
- `cargo fmt` passes;
- `cargo test` passes;
- `cargo clippy -- -D warnings` passes;
- `janus-rust/Cargo.toml` keeps the built-in `all`, `pedantic`, `nursery`, and
  `cargo` Clippy lint groups at `deny`; do not add broad `allow` entries to hide
  failures. Fix every diagnostic or document a narrowly scoped architectural
  exception for review.
- The former strict-Clippy dependency finding (`syn 3.0.6` alongside `syn
  2.0.119`) is resolved by dependency convergence; the current maximum-profile
  run reports zero diagnostics. Keep the narrow Actix `future_not_send`
  architectural expectation documented in source, and never add broad Clippy
  allows or refresh a baseline to hide a new finding.
- The local gate stores the complete Clippy output at
  `janus-rust/artifacts/clippy-output.txt`; a failed lint run is evidence of
  unfinished work, not a reason to refresh or declare the trace metrics green.
- The same local gate runs `cargo check` and maximum-Clippy validation for
  `janus-spacetimedb`, then requires the SpacetimeDB CLI and generated Rust
  bindings for production verification. Its output is stored at
  `janus-rust/artifacts/spacetime-check-output.txt`; missing prerequisites are
  an active error and keep cutover unavailable.
- `antithesis-local` passes when applicable;
- TigerStyle reports zero hard findings;
- no unrelated user changes were overwritten;
- failures, skipped verification, and remaining work are explicitly reported.
- `python antithesis/AntiThesisLocal.py cutover` reported `cutover: ready` on
  the complete gate. The legacy source archive is the retained rollback path;
  the active tree must not recreate Go or Zig implementations.
Do not treat incomplete verification as successful completion.
---
## 13. End-State Direction
The desired end state is:
**Rust implementation + Rust tests + Rust assertions + Rust fixtures + system specification + threat model + repeatable security evidence = complete Janus development contract.**
Go and Zig are unnecessary for normal Janus development; their verified source
archive is retained only for rollback history.
New functionality belongs in Rust unless a task explicitly states otherwise.
