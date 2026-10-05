# User Testing

Testing surface, validation tools, and resource cost classification.

**What belongs here:** How to test user-facing behavior, what tools to use, concurrency limits, testing gotchas.

---

## Validation Surface

### Primary: Browser (Next.js Web App)
- **Tool**: agent-browser (v0.17.1)
- **Location**: C:\Users\steve\.factory\bin\agent-browser.cmd
- **URL**: http://localhost:3000
- **Surfaces**: Landing page, auth flows (sign-in, sign-up, magic-code, reset-password), dashboard (projects, builds, deployments, domains, monitoring, billing)
- **Setup**: Start janus-web service (port 3000) which requires janus-api (port 8080) which requires postgres (5432) and minio (9000)

### Secondary: API Endpoints
- **Tool**: curl.exe (NOT PowerShell's curl alias)
- **Base URL**: http://localhost:8080
- **Auth**: JWT cookie from /api/v1/auth/signin response
- **Key endpoints**: /healthz, /readyz, /api/v1/auth/*, /api/v1/projects, /api/v1/builds, /api/v1/deployments, /api/v1/domains, /api/v1/operations/*

### Tertiary: Go Test Output
- **Tool**: PowerShell terminal
- **Command**: cd E:\Janus\backend\janus-api; go test -count=1 -v ./...

### Quaternary: Backend CLI / Database Validation
- **Tools**: `go vet`, `go run`, `podman exec`, `psql`, schema dumps captured from PostgreSQL
- **Scope**: embedded migration reversibility, repository/static-code assertions, connection-pool configuration, raw-SQL guardrails
- **Database isolation rule**: create temporary databases with prefix `janus_ut_bh_`; never run destructive migration-down checks against the shared `janus` database used by the live API

## Validation Concurrency

### agent-browser
- **Max concurrent validators**: 5
- **Rationale**: 32GB total RAM, ~18GB baseline, 14GB free. 70% headroom = 9.8GB. Each agent-browser instance ~300MB. Dev server ~200MB. 5 instances = 1.7GB total, well within budget.

### curl.exe
- **Max concurrent validators**: 5
- **Rationale**: Negligible resource usage per curl instance.

### backend-cli
- **Max concurrent validators**: 1
- **Rationale**: migration up/down checks are destructive and must be isolated to one validator using temporary databases.

### backend-hardening round decision
- **Max concurrent validators for this milestone**: 2 total
- **Partitioning**: one `backend-cli` validator plus one `api` validator
- **Why**: the CLI validator can stay inside temporary databases while the API validator uses the shared `janus` database and live rate limiter; two validators avoid cross-test interference while still parallelizing the round.

### actor-system round decision
- **Max concurrent validators for this milestone**: 2 total
- **Partitioning**: one `backend-cli` validator plus one `api` validator may run together; additional `backend-cli` validators must run in later batches.
- **Why**: actor-system validation mixes static/source-backed assertions (`ctx.Unhandled`, DI removal, passivation, supervision wiring) with live HTTP assertions (`/readyz`, runner registration/listing). The live API validator can safely use unique runner IDs while the CLI validator owns Go test execution and source inspection.

### frontend-hardening round decision
- **Observed machine state (2026-03-14)**: ~31.2 GB total RAM, ~14.5 GB free before browser validation; heaviest processes were `vmmemWSL` (~2.10 GB) and multiple `droid` / `Code` instances under 1 GB each.
- **Max concurrent validators for this milestone**: 4 total
- **Partitioning**: one `frontend-cli` validator plus up to three `browser` validators may run together.
- **Why**: browser flows need separate auth sessions and project data, while the CLI validator owns lint/build/unit-test assertions. Three concurrent browser sessions stay comfortably within available memory headroom while avoiding dashboard-state interference.

### e2e-pipeline round decision
- **Observed machine state (2026-03-15)**: ~31.2 GB total RAM, ~12.3 GB free before validation startup; shared Janus services (`postgres`, `minio`, `janus-api`, `janus-web`) are already consuming the normal local-dev footprint.
- **Max concurrent validators for this milestone**: 3 total, with at most **2 build/deploy-heavy validators** in the same batch.
- **Partitioning**: run lighter API assertions (auth / guards / OAuth redirect checks) alongside at most one other medium validator; keep language-build and deployment-runtime validators in their own batch or paired only with one light validator.
- **Why**: `curl.exe` itself is cheap, but the underlying assertions trigger shared in-process build/deploy work inside `janus-api`, mutate common workflow tables, and can contend on build/deployment leases even when using isolated users and projects.
- **Low-memory rerun rule (2026-03-15 round 2)**: if free physical memory has fallen to roughly `4 GB` or lower because `vmmemWSL` / browsers / multiple droids are already active, reduce e2e-pipeline concurrency to **2 validators total** and avoid pairing a browser validator with another heavy build/deploy validator.

## Flow Validator Guidance: backend-cli

- Work from `E:\Janus\backend\janus-api`.
- Use temporary databases only, named `janus_ut_bh_<suffix>`.
- Create/drop temporary databases through `podman exec janus-postgres psql -U janus -d postgres`.
- If you need an executable migration harness, keep any helper file inside the module tree so it may import `internal/core`, and delete it before finishing.
- Safe evidence includes `go test`, `go vet`, `go build`, grep output, `pg_dump --schema-only`, and JSON flow reports.
- For actor-system validation, prefer targeted Go tests under `./internal/actors/...` and `./internal/api/handler/...` plus source inspection for assertions that are intentionally proved by code shape (for example `ctx.Unhandled()`, DI removal, passivation options, and execution-actor shutdown calls).
- Keep actor-system CLI batches read-only with respect to source files; they may rely on the already-running stack for readiness checks but should not restart shared services.

## Flow Validator Guidance: api

- Use `curl.exe` against `http://localhost:8080`.
- Use unique test data with the email prefix `backend-hardening-ut+` and unique slugs/IDs to avoid collisions.
- Do not stop or restart shared services.
- Keep rate-limit testing in a single loop from one process so results are attributable to your validator only.
- Save raw response bodies for structured-error and sensitive-data checks.
- For actor-system validation, use runner IDs prefixed with `actor-system-ut-` and verify both registration plus listing semantics from the live API. Reuse `/readyz` to capture role health from the running stack before and after API actions.
- For e2e-pipeline validation, use unique identities with the prefix `e2e-pipeline-ut+`, unique project slugs prefixed `e2e-pipeline-ut-`, and never reuse another validator's build IDs, deployment IDs, or uploaded artifacts.
- When exercising build/deploy assertions, wait for the operation or runtime to reach a terminal state before creating another heavy job in the same validator, so evidence stays attributable to the assigned assertion set.

## Flow Validator Guidance: browser

- Use `agent-browser` against `http://localhost:3000`.
- Each validator must stay inside its own browser session/cookie jar and use unique user emails with the prefix `frontend-hardening-ut+`.
- Use unique project names/slugs and branch/source refs per validator to avoid collisions in shared dashboard lists.
- For frontend-hardening dashboard reruns, expect large pre-seeded authenticated data sets (hundreds of projects/builds/deployments/domains); isolate your round's records with unique project slugs plus project filters instead of relying on authenticated empty states.
- Do not stop or restart shared services from a browser validator.
- Capture screenshots for major states you verify (success, loading, empty, error, 404, modal open) and save them under the assigned evidence directory.
- If validating clipboard behavior through `agent-browser`, capture any `navigator.clipboard.writeText` permission errors explicitly; Download/Close may still be independently verifiable even when clipboard permission is denied in automation.
- If an authenticated dashboard session becomes unreadable right after redirecting to `/get-started` while `localhost:3000` and `localhost:8080` remain healthy, restart only the assigned `agent-browser` daemon/session and reapply auth via `POST /api/v1/auth/session/exchange` rather than restarting the shared stack.
- The running `agent-browser` daemon may ignore a requested download path for exported log files; if a download lands under `%LOCALAPPDATA%\Temp\playwright-artifacts-*`, copy it into the assigned evidence directory and note that relocation in the flow report.
- If a flow depends on unavailable backend capabilities (for example repo import or deployment processing), record the exact blocking UI/API behavior instead of mocking the Janus app itself.

## Flow Validator Guidance: frontend-cli

- Work from `E:\Janus\frontend\web`.
- Validate `VAL-FH-001` / `VAL-FH-002` / `VAL-FH-003` using the real project commands: `npm run build`, `npm run lint`, and `npm run test`.
- Treat expected React error-boundary stack traces in Vitest output as informational if the command still exits 0 and the suite reports passing tests.
- Keep this validator read-only with respect to application source; only write its JSON flow report and evidence files.
- Do not stop or restart shared services from this validator.

## Testing Notes
- PowerShell is the shell — all commands must be PowerShell-compatible
- Use `curl.exe` not `curl` (PowerShell alias conflict)
- Frontend dev server starts on port 3000 (ready in ~1.2s)
- All services via podman-compose at E:\Janus\podman-compose.yml
- E2E pipeline live-build positive case: `https://github.com/0zAND1z/go-wasm` on branch `master` currently imports successfully and reaches `status: built` in the local stack.
- E2E pipeline negative repo-import case: `https://github.com/octocat/Hello-World` currently reaches `status: failed` with `buildStrategy: unknown`, which is useful for unsupported-language validation.
- For e2e-pipeline reruns after backend fixes, use `docker-compose.exe -f E:\Janus\podman-compose.yml build --no-cache janus-api` followed by `docker-compose.exe -f E:\Janus\podman-compose.yml up -d --force-recreate postgres minio janus-api`; the `janus-web` no-cache container rebuild still fails on `npm ci`, so keep using the local source web server on `http://localhost:3000`.
- In the current live stack, `POST /api/v1/uploads/artifacts` now returns an async `202` operation envelope and the terminal `result.buildJob.buildStrategy` is `prebuilt_wasm` for a valid `.wasm` upload.
- In the current live stack, the local-source `/get-started` browser workspace signs in correctly again, but the immediate deployment result card may remain at `status: starting` with `Waiting for runtime endpoint...` until the user manually refreshes deployments/domains.
- In the current live stack, deployment creation from a valid uploaded artifact can still fail with `resource profile not ready yet`; a successful source-bundle build is a more reliable path when a validator needs a running deployment for downstream runtime checks.
- In the current live stack, GitHub OAuth start now redirects to `github.com/login/oauth/authorize` with a matching `github_oauth_state` cookie; full callback validation still requires a legitimate authorization code from the configured GitHub app.
- For e2e-pipeline reruns that need fresh backend bits, rebuild `janus-api` rather than looking for a separate build-worker image. Do not fall back to a stale cached backend image when build or deployment behavior is under test.
- Corroborate `GET /readyz` with a fresh build or deployment when validating worker health so the check reflects both database access and the in-process execution path.
- `GET /api/v1/builds/{id}/logs/stream` can emit `snapshot` plus repeated `heartbeat` events before a build becomes active; if `log` or `status` progression never follows on a healthy stack, treat that as an in-process worker execution issue rather than an SSE transport issue.
- On the current round-3 stack, operation polling now uses the contract vocabulary (`pending`, `processing`, `succeeded`, `failed`), and the OAuth callback assertion may be closed with the orchestrator-approved override by pairing the live start/callback probe with targeted Go tests / source citations for state-cookie validation and credential persistence.
- Once `docker-compose.exe -f E:\Janus\podman-compose.yml ps` shows `janus-api` healthy after a no-cache rebuild, build-dependent assertions can be exercised again on the live stack.
- On the current stack, `GET /api/v1/builds/{id}/logs/stream` during an active build should emit `snapshot`, `log`, `status`, and `heartbeat`, and timeout enforcement remains attributable through the backend timeout environment settings on `janus-api`.
- On the current round-4 stack, the 8-language live matrix still fails for 5 toolchains: TinyGo rejects host Go `1.26`, C/C++ cannot find `clang` in `PATH`, Python `componentize-py` fails on ambiguous `wasi:http` versions, JS/TS `jco` fails without `@bytecodealliance/preview2-shim`, and .NET reports the `wasi-experimental` workload is unsupported in .NET 9.
- On the current round-4 stack, deploying a `prebuilt_wasm` artifact through the real `/get-started` flow now succeeds server-side and the backend deployment reaches `running`, but the frontend result/deployment/domain cards can remain stale even while the browser keeps polling `GET /api/v1/deployments/{id}`; `View Logs` still opens successfully.
- If an authenticated e2e-pipeline browser validator lands on an unreadable `/get-started` session, restart only the isolated browser session and continue inside the same validator boundary rather than restarting shared services.
- Existing Playwright E2E tests: 4 tests in frontend/web/tests/e2e/pipeline.spec.ts
- `.factory/services.yaml` currently exposes `commands.test-backend` rather than a generic `commands.test`; use the backend test command for backend-hardening baseline validation.
- Some scrutiny assertions use literal grep evidence over source files, and comments count as matches; if an assertion requires zero matches for a deprecated identifier or SQL fragment, remove it from comments/tests as well as executable code.
- For backend-hardening API reruns after containerized backend fixes, a no-cache rebuild is sometimes required to pick up backend changes. On this machine `podman compose` delegates to an external compose provider that did not accept `--no-cache`, so use `docker-compose.exe -f E:\Janus\podman-compose.yml build --no-cache janus-api` and then `docker-compose.exe -f E:\Janus\podman-compose.yml up -d janus-api`; plain `up -d` or cached rebuilds may leave stale binaries in the live validation stack.
- For frontend-hardening browser validation, the reliable web surface is the local source build started with `npm run start` from `E:\Janus\frontend\web` (detached via `Start-Process`). Rebuilding the `janus-web` container currently fails on `npm ci` peer-dependency resolution, and the cached image may be stale enough to miss routes like `/billing` and `/monitoring`.
- For frontend-hardening auth reruns, always corroborate any browser-observed signed-in state with `/api/v1/operations/*` polling and `/api/v1/auth/me`; round-2 evidence showed the backend auth operations still failing with `service_unavailable` / missing `janus-node-runtime`, so UI state alone was not reliable enough to treat auth-dependent assertions as passed.
- In the current local frontend-hardening stack there is no real email sender configured, but `auth.magic.request` still succeeds because the email actor skips delivery in dev/test mode. To complete end-to-end magic-code validation, recover the latest 6-digit code by reading `auth_magic_challenges.code_hash` from PostgreSQL and brute-forcing `000000`-`999999` with SHA-256 locally after the real browser request has created the challenge.
- For frontend-hardening shell-state validation, use the preview routes `/billing?preview=loading|empty|error` and `/monitoring?preview=loading|empty|error`; the default `/billing` and `/monitoring` routes still render the generic Coming Soon placeholder when no preview parameter is supplied.
