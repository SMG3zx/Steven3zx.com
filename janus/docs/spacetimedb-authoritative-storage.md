# SpacetimeDB Authoritative Storage Boundary

## Decision

SpacetimeDB is the authoritative store for every bounded Janus control-plane
record. Actix must not treat an in-memory service or local file as a second
production authority. Local services may remain as bounded test doubles and
startup caches, but mutations must be admitted by SpacetimeDB reducers and
reads must be hydrated from SpacetimeDB subscriptions or queries.

Large or continuously streaming bytes are the exception. Source bundles,
compiled artifacts, log bodies, trace bodies, and profile bodies remain in an
object or telemetry store. SpacetimeDB stores their tenant ownership, object
key, checksum, size, content type, lifecycle, retention, and correlation
metadata.

## State inventory

| State | SpacetimeDB target | Current state |
| --- | --- | --- |
| Tenants, accounts, memberships, roles, permissions | normalized tables and reducers | tenant/account/membership tables and reducers exist; signup is live-tested against the local module |
| Sessions and MFA state | session, credential, MFA, revocation, and private OTP-challenge tables | durable session issue/revoke, private credential/MFA, and private email-OTP challenge reducers exist; password verification and MFA enrollment/verification/disable mutations use private SpacetimeDB reducers |
| Projects | project table and lifecycle reducers | generated reducer path and startup ECS hydration exist; authenticated project listing, repository validation, repository-build authorization, and upload authorization read the subscribed durable projection when configured |
| Builds | build, source, artifact, log-index tables | build, artifact, and log-index reducers exist; repository builds and source uploads now write durable build projections with source/object keys and source refs, while source and artifact uploads also write object/artifact metadata through the generated Actix adapter; authenticated build listing, detail, and log reads use the subscribed durable build projection when configured |
| Deployments | deployment, runtime, route, health tables | deployment, route, health, and domain projections use durable deployment/project subscriptions for authenticated reads when configured |
| Runners | runner, lease, capability, claim-history tables | runner registration, heartbeat, tenant-scoped listing, claim, release, startup lease expiration, and hydration now use the generated SpacetimeDB client when configured |
| Jobs and operations | job, operation, command, retry/dead-letter tables | operation hydration and HTTP command admission/completion use the generated SpacetimeDB client; command completion resolves directly from durable command identity; durable jobs carry tenant, target, and generation fences, expose enqueue/claim/complete/fail adapters, reclaim expired leases through a startup reducer, and run deterministic `build.enqueue` and `deployment.create` lifecycle handlers through reducer-owned worker seams; unsupported external provider kinds remain retryable |
| Events and audit | versioned event and audit tables | idempotent command completion plus non-idempotent operation pending/succeeded phases append redacted lifecycle events and actor/correlation-scoped audit records; durable email delivery intents now use the outbox reducer with provider claim/retry transitions; background worker lifecycle wiring remains |
| Metrics, traces, profiles, logs | bounded indexes and samples in SpacetimeDB; bodies external | telemetry samples plus tenant-scoped trace spans, searchable logs, and profile indexes are served from the authenticated telemetry endpoint; large bodies remain external |
| Objects | object metadata and retention records | uploaded object bytes are accompanied by durable object and artifact metadata with status, creation time, and retention deadline; failed uploads delete both durable projections, and startup expires object metadata before deleting external bodies, preserving metadata when cleanup fails for retry |

## Required completion sequence

1. Add normalized identity and authorization records; artifact, runtime, route,
   observability-index, and audit records now have schema and reducers.
2. Add tenant/status/time indexes and explicit retention policies.
3. Extend generated bindings and typed reducer/query adapters.
4. Implement an Actix SpacetimeDB connection with startup hydration and
   subscription-driven projections. The live connection, readiness gate,
   project/build/deployment/runner/operation hydration, and durable signup,
   signin, session issuance, signout, and private password-verification path have been exercised against a
   local SpacetimeDB 2.10.2 module. Auth hydration, private credential query
   replacement, durable operation lookup, durable upload metadata, the
   generation-fenced job reducer/runtime boundary, durable bounded telemetry,
   and private MFA mutation projection are now present. Object retention and
   startup external-body cleanup are also reducer-backed. Uploads now populate
   the artifact metadata projection and clean both metadata rows on failure.
   Build listing/detail/log, deployment listing/detail, and domain projections
   now read durable projections when configured. Claim/settlement and retry
   reconciliation are reducer-backed; deterministic build and deployment
   lifecycle execution is wired, while external provider/runner handlers remain.
5. Route every production mutation through SpacetimeDB before updating any
   local cache or ECS projection. Signup now performs this ordering for tenant,
   account, membership, and private credential records; apply the same boundary
   to sessions, MFA transitions, builds, deployments, runners, jobs, uploads,
   and operations.
6. Add restart, reconciliation, tenant-isolation, replay, and failure tests.
7. Remove production reliance on `FileAuthStore`, `OperationBackend`, and
   in-memory domain stores; retain them only for local deterministic tests.

## Live local evidence

On 2026-10-02, the local SpacetimeDB 2.10.2 server was started on
`127.0.0.1:3000`, the `janus` module was published, and the Actix API was
started with `JANUS_SPACETIME_REQUIRED=true`. `/readyz` returned HTTP 200.
Signup returned HTTP 201, signin returned HTTP 200, `/api/v1/auth/me`
returned the durable tenant/account identity, and signout returned HTTP 200.
Direct SQL inspection confirmed tenant, account, and membership rows in the
published module. A fresh API process also authenticated an account created by
the previous process through the private password reducer, and a project
request with an idempotency key was admitted, completed, replayed, and observed
as `succeeded` in the durable `command_record` row. This is local integration
evidence, not a claim that all remaining domain stores have been migrated.
The updated module was republished to the same local database with
`--delete-data=never`; the new `reconcile_jobs` reducer and generated bindings
compiled and published successfully without clearing existing data.
The later private email-OTP and outbox schema was published and verified against a fresh
local `janus-verify-otp` database because the existing anonymous database
owner was not authorized to update its schema; the existing data was left
untouched. The outbox-enabled module was subsequently published to a fresh
local `janus-verify-outbox` database. The object lifecycle schema and generated
bindings were then published and verified against fresh local
`janus-verify-objects-final`, `janus-verify-artifacts-final`, and
`janus-verify-artifact-retention-final`, and `janus-verify-runners-final`; all verification databases preserve the existing
`janus-local` data. The earlier `janus-verify-objects` database was left
untouched after its anonymous owner correctly rejected a cross-identity update.
The worker-target schema was subsequently published successfully to the fresh
local `janus-worker-verify-1002` database. Direct SQL inspection confirmed the
`job` table includes the durable `target_id` column; the existing `janus-local`
database was not modified.

## Non-goals

SpacetimeDB should not become a blob store for large source, artifact, log,
trace, or profile contents. Those bodies belong in bounded external storage;
the durable index and lifecycle authority belong in SpacetimeDB.
