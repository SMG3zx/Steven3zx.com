# Validation Contracts: E2E Pipeline & Domains

## Area: E2E Pipeline

> Full user journey: sign up → create project → import repo → WASM build → deploy → access running service.

### Authentication & Account Creation

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-E2E-001 | `POST /api/v1/auth/signup` with valid email/password/name returns HTTP 202 with an operation envelope (`operationId`, `status: "pending"`, `pollUrl`). Polling the `pollUrl` until `status: "succeeded"` yields a `result.user` object containing `id`, `email`, and `name`. | Proves the async signup flow completes and produces a usable user identity via the operation envelope pattern. |
| VAL-E2E-002 | `POST /api/v1/auth/signin` with the credentials from VAL-E2E-001 returns HTTP 202. After polling the operation to `succeeded`, exchanging via `POST /api/v1/auth/session-exchange` sets a `janus_access` HTTP-only cookie whose JWT contains the correct `sub` (user ID) and `email` claims. | Proves credential-based authentication issues a valid session token that subsequent authenticated requests can use. |
| VAL-E2E-003 | `GET /api/v1/auth/me` with the session cookie from VAL-E2E-002 returns HTTP 200 with the authenticated user's `id`, `email`, and `name`. Without the cookie, the same endpoint returns HTTP 401. | Proves session-gated endpoints enforce authentication and correctly resolve the caller's identity. |

### Project CRUD

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-E2E-004 | `POST /api/v1/projects` with `{ name, slug, description }` returns HTTP 202 with an operation envelope. Polling to `succeeded` yields a `result.project` with matching fields and a generated `id`. `GET /api/v1/projects` subsequently includes the newly created project in its list. | Proves project creation persists through the async operation pipeline and is immediately queryable. |
| VAL-E2E-005 | `PATCH /api/v1/projects/{id}` with updated `name` and `description` returns HTTP 202. After the operation succeeds, `GET /api/v1/projects` reflects the updated values. `DELETE /api/v1/projects/{id}` returns HTTP 202 and after operation success the project no longer appears in the list. | Proves the full project update and delete lifecycle works end-to-end via the operation envelope pattern. |

### Repository Import & Build

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-E2E-006 | `POST /api/v1/repo-import` with a valid GitHub HTTPS URL and an existing `projectId` returns HTTP 202. Polling the operation to `succeeded` yields a `result.buildJob` with `status` progressing through `pending → cloning → building → built`. | Proves the repo import triggers a build that proceeds through all expected status transitions to completion. |
| VAL-E2E-007 | `POST /api/v1/repo-import` with a malformed URL returns HTTP 400 with `error: "repoUrl must be a valid URL"`. Importing with `provider: "gitlab"` returns HTTP 400 with `error: "unsupported provider"`. Importing without `projectId` returns HTTP 400. | Proves input validation rejects invalid repository import requests before entering the build pipeline. |
| VAL-E2E-008 | After a successful build (VAL-E2E-006), `GET /api/v1/builds?projectId={id}` returns the build with `buildStrategy` set to one of the recognised strategies (`go_wasip1`, `rust_wasip1`, `prebuilt_wasm`) and `artifactBucket`/`artifactKey` populated. | Proves the build system correctly detects the language, selects the appropriate WASM build strategy, and stores the compiled artifact. |
| VAL-E2E-009 | Importing a repository that contains no `go.mod`, `Cargo.toml`, or pre-built `.wasm` file results in the build operation reaching `failed` status with `buildStrategy: "unknown"` and a non-empty `errorSnippet` explaining the unsupported language. | Proves the build pipeline rejects non-WASM-compilable repositories with a clear error rather than silently failing. |

### WASM Language Support

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-E2E-010 | For each of the 8 target languages (Rust wasip1, Rust wasip2/component, Go wasip1, TinyGo wasip2, C/C++ wasi-sdk, Python componentize-py, JS/TS jco, C#/.NET componentize-dotnet): a build triggered from a representative repository produces `status: "built"` and the `buildStrategy` field matches the expected strategy name for that language. | Proves every supported language's detection and compilation pipeline works end-to-end, satisfying the 8-language WASM support requirement. |

### Build Log Viewing

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-E2E-011 | `GET /api/v1/builds/{buildId}/logs` for a completed build returns HTTP 200 with a non-empty `lines` array and a `statusEvents` array containing at least entries for `pending`, `cloning`, `building`, and `built` (or `failed`) transitions. | Proves build logs are captured and status events are recorded throughout the build lifecycle for user observability. |
| VAL-E2E-012 | `GET /api/v1/builds/{buildId}/logs/stream` opens an SSE connection that emits a `snapshot` event, followed by incremental `log` and `status` events, and periodic `heartbeat` events. When the build completes, the final `status` event contains the terminal status. | Proves real-time log streaming works for builds in progress, enabling the frontend to show live build output. |

### Deployment & Access

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-E2E-013 | `POST /api/v1/deployments` with a valid `projectId` and the `buildJobId` from a successful build returns HTTP 202. Polling the operation to `succeeded` yields a `result.deployment` with `status: "running"`, a non-empty `runtimeEndpoint`, and a `result.domainBinding` with an auto-assigned platform subdomain. | Proves deployment creation launches the WASM runtime and assigns a reachable domain, completing the deploy step of the E2E journey. |
| VAL-E2E-014 | An HTTP GET request to the `runtimeEndpoint` returned by VAL-E2E-013 (or the assigned platform subdomain) returns a successful HTTP response (2xx) from the deployed WASM service. For wasi-http components, the response is served directly; for wasi-command modules, the HTTP shim translates the request. | Proves the deployed service is actually reachable and serving traffic, validating the entire repository-to-runtime pipeline. |

### Operation Polling

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-E2E-015 | Every mutating API endpoint (`auth.signup`, `auth.signin.password`, `project.create`, `build.enqueue`, `deploy.create`) returns HTTP 202 with an `operationId` and `pollUrl`. `GET {pollUrl}` returns the operation with `status` of `pending`, `processing`, `succeeded`, `failed`, or `dead_lettered`. Failed operations include a `failure` object with `code`, `message`, and `retryable` fields. | Proves the async operation envelope pattern is consistently applied across all mutating endpoints and that both success and failure paths expose actionable status information. |

---

## Area: Domains

> Cloudflare DNS integration for platform subdomains and custom domain management.

### Platform Subdomain Auto-Assignment

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-DOM-001 | When a deployment is created, the response includes a `domainBinding` with `type: "platform"` and a `domain` matching the pattern `{project-slug}.apps.{platform-domain}`. No Cloudflare API call is made for this assignment (wildcard DNS handles resolution). | Proves platform subdomains are assigned instantly via the wildcard DNS strategy without per-deployment API overhead, as recommended in the Cloudflare research. |
| VAL-DOM-002 | The reverse proxy / ingress layer routes an HTTP request with `Host: {slug}.apps.{platform-domain}` to the correct deployment's runtime endpoint. Requests to a non-existent subdomain under the wildcard return an appropriate error (e.g., 404 or 502) rather than routing to a random deployment. | Proves Host-header-based routing correctly maps platform subdomains to their deployments and handles unknown subdomains gracefully. |

### Custom Domain Addition Flow

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-DOM-003 | A custom domain addition API call (e.g., `POST /api/v1/domains`) with a valid hostname creates a `DomainBinding` record with `type: "custom"` and initial verification status `pending`. The response includes the CNAME target (e.g., `customers.{platform-domain}`) and TXT verification record details (`_cf-custom-hostname.{domain}` with a unique token). | Proves the custom domain onboarding flow generates the correct DNS instructions for the user and creates a Cloudflare Custom Hostname via the CF for SaaS API. |
| VAL-DOM-004 | After the user configures the required CNAME and/or TXT record at their DNS provider, the platform's verification poller detects the correct DNS configuration and transitions the domain binding status from `pending` → `dns_configured` → `verified` → `active`. | Proves the periodic verification polling correctly validates domain ownership through DNS record checks with appropriate status transitions. |

### TXT-Based Domain Verification

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-DOM-005 | The TXT verification token generated for a custom domain is unique per domain binding and matches the format `janus-verify={random-token}`. A DNS lookup for `_janus-verification.{custom-domain}` (or `_cf-custom-hostname.{custom-domain}` when using CF for SaaS) returning the expected TXT value causes verification to succeed. | Proves TXT-based ownership verification generates cryptographically unique tokens and validates them through standard DNS lookups. |
| VAL-DOM-006 | If the TXT record is not found within the verification timeout window (e.g., 7 days), the domain binding status transitions to `expired` and the associated Cloudflare Custom Hostname is cleaned up. The polling backoff schedule increases intervals from 30s (first 5 min) → 2 min (5–30 min) → 15 min (30 min–24h) → 1h (24h–7d). | Proves the verification system enforces a timeout with appropriate backoff to avoid excessive DNS queries, and cleans up stale hostname records. |

### SSL Provisioning

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-DOM-007 | After a custom domain passes verification (VAL-DOM-004), Cloudflare for SaaS automatically provisions a DV SSL certificate. The domain binding record exposes an `sslStatus` field that transitions from `initializing` → `pending_validation` → `active`. HTTPS requests to the custom domain are served with a valid certificate. | Proves the Cloudflare for SaaS integration handles automatic SSL provisioning end-to-end so that custom domains are secured without manual certificate management. |

### Domain Removal

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-DOM-008 | Deleting a custom domain via the API removes the `DomainBinding` record from the database, deletes the corresponding Cloudflare Custom Hostname, and ceases routing traffic for that hostname. Subsequent requests to the removed domain return an error (not routed to any deployment). | Proves domain removal is a complete teardown that cleans up both local state and Cloudflare resources, preventing orphaned DNS entries. |
| VAL-DOM-009 | Deleting a deployment that has an associated platform subdomain domain binding also removes or deactivates that binding. The subdomain still resolves (wildcard DNS) but returns an appropriate error page instead of serving stale content. | Proves deployment deletion cascades to domain bindings and the ingress layer stops routing to the removed deployment. |

### Edge Cases

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-DOM-010 | Attempting to add a custom domain with an invalid hostname (e.g., empty string, IP address, `localhost`, hostname exceeding 253 characters, or containing invalid characters) returns HTTP 400 with a descriptive validation error. | Proves the domain addition endpoint validates hostname format before creating Cloudflare resources, preventing invalid API calls. |
| VAL-DOM-011 | Attempting to add a custom domain that is already bound to another deployment on the platform returns HTTP 409 (conflict) with an error indicating the domain is already in use. | Proves the platform prevents duplicate domain bindings that would cause ambiguous routing, enforcing domain uniqueness. |
| VAL-DOM-012 | Attempting to add a platform subdomain pattern (e.g., `anything.apps.{platform-domain}`) as a custom domain is rejected with a clear error, preventing users from hijacking other deployments' platform subdomains. | Proves the custom domain flow blocks reservation of platform-controlled namespace to prevent subdomain takeover attacks. |
| VAL-DOM-013 | When a custom domain's CNAME record is removed by the user after the domain was previously active, the platform detects the change (via Cloudflare hostname status becoming `moved` or monitoring) and transitions the domain binding to a degraded or deactivated state. | Proves the platform monitors ongoing domain health and reflects CNAME removal rather than silently serving errors. |

### Frontend UI

| ID | Assertion | Rationale |
|----|-----------|-----------|
| VAL-DOM-014 | The project settings page in the Next.js frontend displays the auto-assigned platform subdomain for each deployment and provides a form to add a custom domain. The form shows the required CNAME target and TXT verification instructions after submission. | Proves the frontend exposes domain management capabilities with clear user guidance for the DNS configuration steps. |
| VAL-DOM-015 | The domain management UI shows real-time verification and SSL status for each custom domain (polling the backend). Domains in `pending` state show the required DNS records; `active` domains show a green status indicator; `expired` domains show a re-verify action. | Proves the frontend provides actionable status feedback throughout the custom domain lifecycle so users can diagnose and resolve DNS configuration issues. |
