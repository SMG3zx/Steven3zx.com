# Validation Contracts: Backend Hardening & Actor System

---

## Area: Backend Hardening

### VAL-BH-001: Database Migration Up/Down Reversibility
The golang-migrate migration tooling replaces the current raw DDL `State.migrate()` method. Running `migrate up` from a fresh database creates all tables, indexes, and sequences to the latest version. Running `migrate down` fully reverts each step without orphaned objects. Running `migrate up` again after a full down restores the schema identically.

**Pass condition:** `migrate up` succeeds on an empty database, `migrate down` succeeds back to version 0 with zero tables remaining, and a second `migrate up` succeeds with schema matching the first run.
**Fail condition:** Any migration step errors, leaves orphaned tables/sequences after down, or produces a different schema on re-apply.

Evidence: Run `migrate up`, `migrate down`, `migrate up` in sequence against a fresh PostgreSQL database. Capture exit codes, compare `pg_dump --schema-only` output from first and second up runs.

---

### VAL-BH-002: Fresh Database Schema Matches Application Expectations
A fresh database after running all migrations produces a schema that the application can use without any additional DDL. The `State.migrate()` inline DDL approach is fully removed and replaced by migration files.

**Pass condition:** `NewState(dsn)` succeeds against a database that has only had `migrate up` run. No `CREATE TABLE IF NOT EXISTS` or `ALTER TABLE ADD COLUMN IF NOT EXISTS` statements remain in `state.go`.
**Fail condition:** `NewState()` fails, or raw DDL statements still exist in `state.go` or any Go code outside the migration files.

Evidence: `grep -rn "CREATE TABLE IF NOT EXISTS\|ALTER TABLE.*ADD COLUMN" backend/janus-api/internal/` returns zero matches outside migration files. `go test ./...` passes with a migration-only database.

---

### VAL-BH-003: Repository Pattern Interfaces Defined
All database access goes through repository interfaces. Each domain entity (users, projects, builds, deployments, operations, runners, telemetry, auth challenges) has a corresponding Go interface in the `core` package defining its data access methods.

**Pass condition:** Each entity has a named interface (e.g., `UserRepository`, `ProjectRepository`) with all CRUD methods declared. The `State` struct implements all interfaces. No handler or actor code calls `s.db.Exec/Query` directly.
**Fail condition:** Raw `*sql.DB` calls exist outside repository implementations, or any entity lacks a defined interface.

Evidence: `grep -rn "\.db\.Exec\|\.db\.Query\|\.db\.QueryRow" backend/janus-api/internal/` shows matches only in files implementing the repository pattern (core package). Interfaces are declared and verified by Go compiler (`go build ./...`).

---

### VAL-BH-004: No Raw SQL in Handlers or Actors
HTTP handlers (`internal/api/handler/`) and actor code (`internal/actors/`) contain zero direct SQL statements. All data access is mediated through the `State` or repository interfaces.

**Pass condition:** No file in `handler/` or `actors/` contains `sql.`, `db.Exec`, `db.Query`, `SELECT `, `INSERT `, `UPDATE `, or `DELETE ` SQL keywords used as query strings.
**Fail condition:** Any handler or actor file contains direct SQL execution.

Evidence: `grep -rn "db\.Exec\|db\.Query\|sql\.\|\"SELECT \|\"INSERT \|\"UPDATE \|\"DELETE " backend/janus-api/internal/api/handler/ backend/janus-api/internal/actors/` returns zero results.

---

### VAL-BH-005: Structured Error Responses on All API Error Paths
All API error responses (4xx, 5xx) return a consistent JSON envelope: `{"error": {"code": "<error_code>", "message": "<human_readable_message>"}}`. No raw Go panic stack traces, SQL error messages, or unstructured error strings leak to clients.

**Pass condition:** Sending malformed requests (bad JSON, missing fields, invalid IDs, wrong HTTP methods) to every API route returns the structured error envelope. No response body contains SQL keywords (`pq:`, `pgx:`, `duplicate key`, `foreign key`), Go runtime messages (`runtime error`, `goroutine`), or raw error strings without the envelope.
**Fail condition:** Any API error response lacks the `{"error": {...}}` envelope, or exposes internal implementation details.

Evidence: Curl each API endpoint with known-bad inputs (empty body, invalid JSON, nonexistent IDs, wrong methods). Parse response JSON and verify `error.code` and `error.message` fields exist. Verify no raw SQL or Go errors appear in response bodies.

---

### VAL-BH-006: API Returns Proper HTTP Status Codes
Each API endpoint returns semantically correct HTTP status codes: 200 for successful GETs, 201/202 for creates, 400 for validation errors, 401 for missing/invalid auth, 404 for missing resources, 405 for wrong methods, 409 for conflicts, 429 for rate limits, 500 for internal errors.

**Pass condition:** Exercising each known error condition produces the expected HTTP status code (e.g., duplicate signup → 409 via operation failure, missing project → 404, no auth cookie → 401, wrong method → 405).
**Fail condition:** Any endpoint returns an incorrect status code for a known error condition (e.g., 200 for an error, 500 for a client error).

Evidence: Curl each endpoint with valid and invalid inputs. Record HTTP status codes. Compare against expected values from handler code analysis.

---

### VAL-BH-007: Go Test Suite Passes with Zero Failures
Running `go test ./...` from `backend/janus-api` completes with all tests passing and no test panics.

**Pass condition:** `go test ./...` exits with code 0, all test functions report PASS.
**Fail condition:** Any test fails, panics, or the command exits with a non-zero code.

Evidence: Capture full `go test -v ./...` output. All packages show `ok` status.

---

### VAL-BH-008: No Duplicated Query Logic Across Repositories
Each SQL query pattern (e.g., "find user by email", "list projects", "claim build job") exists in exactly one method. No copy-paste query duplication across different files or methods.

**Pass condition:** Searching for key SQL fragments (e.g., `FROM users WHERE email`, `FROM projects WHERE id`, `FROM build_jobs WHERE status`) shows each appearing in at most one location (the canonical repository method).
**Fail condition:** The same SQL query pattern appears in multiple methods or files.

Evidence: `grep -rn` for characteristic SQL fragments across `internal/core/`. Count occurrences per pattern. Each canonical query should appear once.

---

### VAL-BH-009: Efficient Data Structures Replace Linear Scans
Code that previously used linear scans (slice iteration for lookups) now uses maps or indexed lookups where appropriate. Specifically: git provider lookups use `map[string]GitProvider`, project lookups by ID/slug use database indexes, and role checks use map-based or set-based lookups.

**Pass condition:** `State.gitProviders` is typed as `map[string]GitProvider`. Role checking functions (`hasRole`, `hasAnyRole`) or their replacements use efficient lookups. Database queries for single-entity lookups use indexed columns (primary keys, unique constraints).
**Fail condition:** Git provider lookups iterate a slice. Role checks use nested loops without short-circuiting. Single-entity DB lookups lack proper WHERE clauses on indexed columns.

Evidence: Review `state.go`, `node.go` for data structure types. Verify `CREATE INDEX` or `UNIQUE` constraints exist for all columns used in WHERE clauses for single-row lookups.

---

### VAL-BH-010: Proper Go Error Wrapping Throughout
All error returns use `fmt.Errorf("context: %w", err)` wrapping pattern or sentinel errors. No bare `err` returns without context. All exported functions that return errors include sufficient context for debugging.

**Pass condition:** `go vet ./...` passes. Error returns in exported functions include descriptive wrapping with `%w`. No `return err` without wrapping in functions that add domain-level context.
**Fail condition:** `go vet` reports issues. Exported functions return bare `err` without context wrapping where meaningful context could be added.

Evidence: Run `go vet ./...`. Grep for `return err` and `return nil, err` patterns; verify each has appropriate wrapping or is in a trivial passthrough context.

---

### VAL-BH-011: Database Connection Pooling Configured
The PostgreSQL connection pool is configured with appropriate limits: max open connections, max idle connections, and connection max lifetime are explicitly set rather than using Go defaults.

**Pass condition:** `sql.Open` or the `*sql.DB` object has `SetMaxOpenConns`, `SetMaxIdleConns`, and `SetConnMaxLifetime` called with sensible values (e.g., max open 25, max idle 5, lifetime 5min).
**Fail condition:** Default Go connection pool settings are used with no explicit configuration.

Evidence: Grep for `SetMaxOpenConns`, `SetMaxIdleConns`, `SetConnMaxLifetime` in `state.go` or database initialization code. Verify values are set.

---

### VAL-BH-012: Health and Readiness Endpoints Report Accurate Status
`GET /healthz` returns 200 with `{"ok": true}` when the server is running. `GET /readyz` returns 200 when database and actor system are healthy, and 503 with failing checks when any dependency is down.

**Pass condition:** `/healthz` always returns 200. `/readyz` returns 200 with all checks "ok" when system is healthy. When database is unreachable, `/readyz` returns 503 with `database` check showing "down".
**Fail condition:** `/healthz` or `/readyz` return incorrect status codes or inaccurate check results.

Evidence: Curl `/healthz` and `/readyz` during normal operation and verify response structure. Verify `/readyz` response includes `database`, `actors.actor_system`, and `actors.command_gateway` checks.

---

### VAL-BH-013: API Rate Limiting Enforced
The `/api/v1/` endpoints are protected by rate limiting. Exceeding the rate limit returns HTTP 429 with a structured error response.

**Pass condition:** Sending rapid requests to a rate-limited endpoint eventually returns HTTP 429 with `{"error": {"code": "rate_limited", ...}}`.
**Fail condition:** Rate limiting never triggers, or triggers with a non-429 status code or unstructured response.

Evidence: Send 100+ rapid requests to an API endpoint (e.g., `/api/v1/auth/signup`) via curl in a loop. Verify at least one response has status 429 with the correct error envelope.

---

### VAL-BH-014: Migrations Are Idempotent
Running `migrate up` when the database is already at the latest version is a no-op that succeeds without errors. Individual migration files are idempotent (use `IF NOT EXISTS`, `IF EXISTS` guards).

**Pass condition:** Running `migrate up` twice in a row both succeed with exit code 0. No "already exists" or "duplicate" errors.
**Fail condition:** Second `migrate up` fails with duplicate object errors.

Evidence: Run `migrate up` followed by `migrate up` again. Capture both exit codes and stderr output. Both should be clean.

---

### VAL-BH-015: No Sensitive Data in API Error Responses
API error responses never expose database connection strings, internal file paths, password hashes, JWT secrets, or environment variable values.

**Pass condition:** Triggering every known error path (bad auth, missing resources, DB constraint violations, internal errors) produces responses that contain no sensitive substrings (`postgres://`, `password_hash`, file paths like `E:\`, environment variable names like `JANUS_JWT_SECRET`).
**Fail condition:** Any error response contains sensitive information.

Evidence: Collect all error responses from API testing. Search each for patterns matching connection strings, file paths, hash values, or secret names.

---

## Area: Actor System

### VAL-AS-001: Actor System Starts Without Errors
Calling `StartActorNode()` with valid configuration creates and starts the GoAkt actor system, spawns role-appropriate actors (gateway, email, build, runner), and returns a non-nil `*Node` with no error.

**Pass condition:** `StartActorNode()` returns `(node, nil)` where `node.system.Running()` is true. All expected actors for the configured roles exist in the system.
**Fail condition:** `StartActorNode()` returns an error, or `node.system.Running()` is false after return.

Evidence: `go test` with `TestActorNodeWorkflow` or equivalent test that calls `StartActorNode()` and checks `node.ReadyChecks()`. All checks report "ok".

---

### VAL-AS-002: Actor System Shuts Down Cleanly Within Timeout
Calling `node.Stop(ctx)` with a reasonable context deadline stops the actor system without hanging. All actors terminate and the runtime registry is cleaned up.

**Pass condition:** `node.Stop(ctx)` returns nil within the configured `ShutdownTimeout` (default 30s). After stop, `nodeRuntimeRegistry` no longer contains the node's runtime key.
**Fail condition:** `Stop()` hangs past the timeout, returns an error, or leaves stale entries in the runtime registry.

Evidence: Test calls `node.Stop()` with a 10s timeout context. Verify it returns in <10s. Verify the runtime registry is empty for that node's key after stop.

---

### VAL-AS-003: Unknown Messages Handled with ctx.Unhandled()
All actor `Receive` and grain `OnReceive` methods handle the `default` case using `ctx.Unhandled()` (GoAkt framework method) instead of silent `log.Printf` drops. This ensures the framework's dead-letter and supervision mechanisms can observe unhandled messages.

**Pass condition:** Every actor and grain that has a `default:` case in its message switch uses `ctx.Unhandled()`. No actor silently drops messages with only a log statement.
**Fail condition:** Any actor's default case uses `log.Printf` followed by return without calling `ctx.Unhandled()`. Currently, `commandGatewayActor.Receive`, `emailDispatchActor.Receive`, `buildNodeActor.Receive`, `runnerNodeActor.Receive`, `buildExecutionActor.Receive`, and `deploymentExecutionActor.Receive` all use silent logging.

Evidence: Grep for `log.Printf.*ignored message` in actor files. After fix, these should be replaced with `ctx.Unhandled()`. Verify by searching for `Unhandled()` calls in all actor/grain default cases.

---

### VAL-AS-004: Supervision Strategies Configured for Critical Actors
Critical actors (command gateway, email dispatch, build node, runner node) have supervision strategies that restart them on failure rather than letting the system stop. Supervision is configured using GoAkt's `actor.WithSupervisorStrategy()` or equivalent.

**Pass condition:** Spawning critical actors includes supervision configuration. If a critical actor panics or returns an error, it is restarted by its supervisor rather than staying dead.
**Fail condition:** Critical actors are spawned without supervision, meaning a panic or fatal error kills the actor permanently.

Evidence: Grep for `WithSupervisorStrategy`, `WithRestartStrategy`, or equivalent GoAkt supervision options in `node.go` spawn calls. Verify each critical actor has a restart policy.

---

### VAL-AS-005: Email Dispatch Uses Non-Blocking Retry
The `emailDispatchActor.Receive` method does not block the actor's message processing with `time.Sleep()` for retry delays. Instead, retries use GoAkt scheduling (`ctx.Schedule()` or `system.Schedule()`) to re-deliver the message after a delay.

**Pass condition:** No `time.Sleep` call exists in `emailDispatchActor.Receive()`. Retry logic uses actor-system scheduling to defer retry attempts. The email actor remains responsive to other messages during retry waits.
**Fail condition:** `time.Sleep(retryDelayForAttempt(attempt))` exists in the email dispatch receive path, blocking the actor goroutine. Currently the code has a blocking `time.Sleep` retry loop.

Evidence: Grep for `time.Sleep` in `control_actors.go` within the email dispatch actor. After fix, verify scheduling-based retry. Run a test that sends an email with a failing sender and verifies the actor remains responsive.

---

### VAL-AS-006: ctx.Err() Not Misused for Transient Errors
Actor code uses `ctx.Err(err)` only for fatal actor-level errors that should trigger supervision/restart, not for transient operational errors (like a DB query failure or network timeout). Transient errors are logged or returned via `ctx.Response()` without calling `ctx.Err()`.

**Pass condition:** `ctx.Err(err)` is called only when the error represents a fundamental actor failure (e.g., corrupted state, permanent configuration error). Transient errors from DB calls, network calls, or business logic are handled via logging or response messages.
**Fail condition:** `ctx.Err(err)` is called for routine transient errors like failed DB queries or claim failures, potentially triggering unnecessary actor restarts. Currently `buildNodeActor.claimBuilds` and `runnerNodeActor.claimDeployments/reportHeartbeat` call `ctx.Err()` for operational DB errors.

Evidence: Grep for `ctx.Err(` in actor files. Review each call site to verify it's only used for fatal errors. After fix, transient error paths should use logging or response-based error reporting.

---

### VAL-AS-007: Grain Passivation Timeouts Configured
Virtual grains (authGrain, projectGrain, buildWorkflowGrain, deploymentWorkflowGrain, operationGrain) have passivation timeouts configured so idle grains are deactivated and their resources freed. The `PassivationWindow` from `ActorNodeConfig` is applied to grain configuration.

**Pass condition:** Grain registration in `newActorSystem()` includes passivation timeout configuration. Idle grains are deactivated after the configured `PassivationWindow` (default 5 minutes). `OnDeactivate` is called on idle grains.
**Fail condition:** Grains remain active indefinitely, consuming memory. No passivation timeout is set in the cluster/grain configuration.

Evidence: Check `newActorSystem()` for grain passivation configuration. Verify `actor.NewClusterConfig()` chain includes passivation settings. Test that a grain created during a test and left idle is eventually deactivated (can be verified in integration test by checking system grain count after timeout).

---

### VAL-AS-008: Global sync.Map Runtime Registry Replaced with DI
The `nodeRuntimeRegistry` global `sync.Map` is replaced with GoAkt dependency injection. Actors and grains receive their dependencies (state, email sender, config) through the framework's DI mechanism rather than looking them up in a global map.

**Pass condition:** `nodeRuntimeRegistry` global variable is removed from `node.go`. The `lookupRuntime()` function is removed or replaced with DI-based dependency resolution. Actors receive their dependencies through constructor injection or GoAkt's DI container.
**Fail condition:** `sync.Map` global registry still exists and is used for runtime lookups.

Evidence: Grep for `nodeRuntimeRegistry` and `sync.Map` in actor package files. After fix, these should not exist. Verify actors use constructor parameters or GoAkt DI instead.

---

### VAL-AS-009: Command Gateway Routes All Domain Commands
The `commandGatewayActor` correctly routes all command types (auth signup/signin/magic/reset, project create/update/delete, build enqueue, deploy create) to the appropriate grain via `AskGrain`. Each command type resolves to the correct grain identity and factory.

**Pass condition:** Sending each command type through `SubmitOperation()` results in the correct grain being activated and the operation reaching `succeeded` or `failed` status (not stuck in `pending`). The `grainIdentity()` method handles all message types without falling through to the error default case.
**Fail condition:** Any command type is not routed (falls into the gateway's default case), or is routed to the wrong grain type.

Evidence: Integration test (`TestActorNodeWorkflow`) or unit tests that submit each command type and verify the operation reaches a terminal status. Verify all message types in `grainIdentity()` switch are covered.

---

### VAL-AS-010: Operation Lifecycle State Machine Correct
Operations follow the state machine: `pending` → `running` → `succeeded`|`failed`|`dead_lettered`. The gateway marks operations as `running` before dispatching to grains, and marks `succeeded` or `failed` based on grain response. Invalid transitions are rejected.

**Pass condition:** Submitting an operation produces status transitions visible via `GET /api/v1/operations/:id`: initially `pending`, then `running`, then `succeeded` or `failed`. Failed operations include `errorCode` and `errorMessage`. Succeeded operations include `result`.
**Fail condition:** Operations get stuck in `pending` or `running`. Failed operations lack error details. Succeeded operations lack results.

Evidence: Submit operations via API, poll `/api/v1/operations/:id` repeatedly, record status transitions. Verify terminal states include appropriate payloads.

---

### VAL-AS-011: Build Polling Claims and Executes Jobs
The `buildNodeActor` receives periodic `buildPollTick` messages, claims pending build jobs from the database, spawns `buildExecutionActor` children, and those children execute the build. After execution, the build job status transitions to `success` or `failure`.

**Pass condition:** After enqueueing a build via the actor system, the build job status progresses from `queued` → `running` → `success` within the poll interval. Build logs are recorded.
**Fail condition:** Build jobs remain `queued` indefinitely. Build execution actor is not spawned. Build status never reaches `success`.

Evidence: Integration test submits a build, then polls `state.GetBuild(buildID)` until status is `success`. Verify the transition happens within reasonable time (< 10s with 50ms poll interval in tests).

---

### VAL-AS-012: Runner Polling Claims and Executes Deployments
The `runnerNodeActor` receives periodic `runnerPollTick` messages, claims pending deployments from the database, spawns `deploymentExecutionActor` children, and those children execute the deployment. After execution, the deployment status transitions to `running`.

**Pass condition:** After creating a deployment via the actor system, the deployment status progresses from `queued` → `starting` → `running` within the poll interval.
**Fail condition:** Deployments remain `queued` indefinitely. Deployment execution actor is not spawned. Deployment status never reaches `running`.

Evidence: Integration test submits a deployment, then polls deployment status until `running`. Verify within reasonable time.

---

### VAL-AS-013: Runner Heartbeat Reports Health
The `runnerNodeActor` sends periodic heartbeat reports via `runnerHeartbeatTick`. The heartbeat updates the runner's `last_seen_at` and status in the database.

**Pass condition:** After a runner actor is started, querying `runner_heartbeats` shows a recent `updated_at` timestamp for the runner. The heartbeat interval matches configuration.
**Fail condition:** No heartbeat rows exist. `updated_at` is stale (older than 2x heartbeat interval).

Evidence: Start a runner node, wait 2-3 heartbeat intervals, query `runner_heartbeats` table for the runner ID. Verify `updated_at` is recent and `status` is "healthy".

---

### VAL-AS-014: Ready Checks Reflect Actual Actor Health
`node.ReadyChecks()` returns accurate health status for all configured role-dependent components: `actor_system`, `discovery`, `command_gateway`, `build_node`, `runner_node`.

**Pass condition:** After successful startup, `ReadyChecks()` returns `(true, checks)` where all applicable checks show "ok". If an actor is missing or the system is stopped, the relevant check shows "down" and the overall result is `false`.
**Fail condition:** `ReadyChecks()` reports "ok" when actors are actually down, or reports "down" when actors are healthy.

Evidence: Call `ReadyChecks()` after startup (expect all ok). Call after `Stop()` (expect failures). Verify checks map keys match configured roles.

---

### VAL-AS-015: TestKit-Based Actor Unit Tests Pass
Dedicated unit tests using GoAkt's TestKit exercise individual actor and grain behaviors in isolation without requiring a live database or network. Tests cover message handling, error responses, and lifecycle callbacks.

**Pass condition:** `go test ./internal/actors/...` passes. Tests exist for: grain key generation, command routing, error handling for unknown messages, and grain activation/deactivation.
**Fail condition:** Actor tests fail, or no TestKit-based tests exist.

Evidence: Run `go test -v ./internal/actors/...`. Verify test functions exist that use GoAkt TestKit patterns. All tests pass.

---

### VAL-AS-016: Port Collision Recovery Works
When actor system ports are already in use, the `StartActorNode()` function automatically bumps ports and retries up to 5 times. Recovery succeeds without manual intervention.

**Pass condition:** If the configured ports are occupied (e.g., by another test), `StartActorNode()` successfully starts on alternative ports after retry. Logs show port collision detection and recovery messages.
**Fail condition:** `StartActorNode()` fails with "address already in use" without attempting retry, or exhausts retries and returns an error when free ports exist.

Evidence: Start two actor nodes with the same initial port configuration. Verify the second one starts successfully on bumped ports. Check logs for retry messages.

---

### VAL-AS-017: Execution Actors Self-Terminate After Completion
`buildExecutionActor` and `deploymentExecutionActor` call `ctx.Shutdown()` after completing their work. They do not leak as idle long-lived actors.

**Pass condition:** After a build or deployment execution completes, the child actor is no longer present in the actor system. `ctx.Shutdown()` is called at the end of the execution path.
**Fail condition:** Execution actors remain alive after completing work, accumulating over time and consuming resources.

Evidence: Grep for `ctx.Shutdown()` in `runner_actors.go` within execution actor receive methods. In integration test, verify actor count does not grow unboundedly after multiple build/deploy cycles.
