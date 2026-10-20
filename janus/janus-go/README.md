# Janus Go

Janus Go is a small GoAkt application with an HTTP edge and actor-owned authentication, authorization, accounting, and telemetry services over a SQL store.

## Run locally

Copy `.env.example` to `.env`, set `JANUS_JWT_SECRET` to a private random value of at least 32 bytes, then run `go run .`. The example deliberately leaves it blank, so the app fails startup until you set one. Keep `.env` out of version control. Set `JANUS_COOKIE_SECURE=true` when serving over HTTPS. The default database is the local Turso file `data/janus.db`. It is a local-only database file; no cloud sync is configured.

Google sign-in is optional. Set `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and `GOOGLE_CALLBACK_URL` together to enable it; the homepage then shows a **Continue with Google** button. Register the callback URL with the Google OAuth client. For the public Janus site, use `https://janus.steven3zx.com/auth/google/callback` and set `JANUS_COOKIE_SECURE=true`.

GitHub sign-in and repository import are optional. Set `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, and `GITHUB_CALLBACK_URL` together. Register the configured callback URL (for example `https://janus.steven3zx.com/auth/github/callback`) in the GitHub OAuth App. GitHub sign-in requests `read:user` and `user:email`; connecting GitHub from Projects requests the broader `repo` scope so private repositories can be imported. Sign-in identities are matched by GitHub's provider ID, never by email. If the verified email already belongs to a Janus account, sign in there and connect GitHub from `/projects`.

## Projects and repository imports

Signed-in users can create an empty project at `/projects/new` or create a project and import a GitHub repository. The project page lists repositories, branches, and the 50 newest commits for a branch. Imports run asynchronously and show their current stage. Janus fetches the selected commit with pure-Go Git, scans without running repository code, and stores tracked file contents in the local Turso database. The per-snapshot limits are 256 MiB total uncompressed file content, 50,000 files, and 16 MiB per file. The five newest successful snapshots are retained per project; interrupted imports are cleaned up on startup. The project page lists retained snapshots and lets the owner reactivate an earlier revision without reimporting it.

The initial scanner recognizes Go, Python, Rust, and JavaScript/TypeScript conventions. It suggests entrypoint candidates for the user to confirm. Repository metadata, selected branch and commit, language, stars, forks, file count, and bytes are shown after import. GitHub access tokens are encrypted in the local database with AES-GCM using a key derived from `JANUS_JWT_SECRET`; changing that secret requires reconnecting GitHub. Disconnecting GitHub removes the stored token.

Project pages are `/projects`, `/projects/new`, and `/projects/{id}`. Authenticated JSON endpoints include `GET/POST /api/projects`, `GET/PUT/DELETE /api/projects/{id}`, `GET /api/github/repos`, `GET /api/github/branches?repository=owner/repo`, `GET /api/github/commits?repository=owner/repo&branch=main`, `POST /api/projects/{id}/imports`, `GET /api/imports/{job_id}`, `GET /api/projects/{id}/snapshots`, `POST /api/projects/{id}/snapshots/{snapshot_id}/activate`, `POST /api/projects/{id}/entrypoint`, `POST /api/projects/{id}/wasm-builds`, `GET /api/wasm-builds/{job_id}`, and `GET /api/wasm-builds/{job_id}/artifact`. All project reads, build status, and artifacts are scoped to the signed-in owner.

### Rust to WebAssembly builds

Imported Rust snapshots can be built from the project page for `wasm32-unknown-unknown` or `wasm32-wasip1`. Imported Go snapshots can be built for `wasip1-wasm` when the repository has a root `go.mod` and root package. Build jobs run asynchronously, show bounded compiler output and status, and store the resulting `.wasm` files in a Zstandard-compressed ZIP (`.zip.zst`) in the local database. Decompress the Zstandard frame to recover the ZIP archive. Import does not execute repository code; builds run explicitly in the isolated builder containers.

The `DockerControllerActor` is the application's single owner for Docker daemon checks and container execution. At startup it checks the engine and, when unavailable, runs `docker desktop start --timeout 60s`, then verifies the engine again. Startup fails if Docker Desktop cannot be started or the engine remains unavailable; this requires a Docker CLI with the Docker Desktop CLI plugin. The actor also runs requests from specialized workload actors with fixed isolation settings. Rust and Go builds use separate actors and separately configured trusted local images: `JANUS_WASM_BUILDER_IMAGE` and `JANUS_GO_WASM_BUILDER_IMAGE`. Build them with `docker build -f build/Dockerfile.wasm-rust -t janus-rust-wasm-builder:local .` and `docker build -f build/Dockerfile.wasm-go -t janus-go-wasm-builder:local .`. Each container has no network, a read-only root filesystem, a read-only source mount, no writable host mount during compilation, dropped Linux capabilities, a PID limit, 2 GiB memory, a 1 GiB temporary build filesystem, and a 10-minute deadline. Cargo is offline and locked; Go module lookup is also offline. Repositories must vendor dependencies or the trusted image must include dependency caches. The compressor actor wraps the generated ZIP in a Zstandard frame at level 19 and stores a `.zip.zst` artifact. The output archive is limited to 128 MiB. JavaScript and TypeScript projects (the Node and Bun templates) use a third image, `JANUS_JS_WASM_BUILDER_IMAGE` (`janus-js-wasm-builder:local`, built from `build/Dockerfile.wasm-js`): Bun bundles the Entrypoint into one file and Javy compiles it with the QuickJS interpreter to a `wasm32-wasip1` module. Node and Bun APIs such as `node:fs` are not available, and dependencies must be vendored. Their runs start with a 100 billion fuel limit because the interpreter spends far more fuel per operation. Other source languages and project-specific build settings are not supported yet.

The compression experiment can be rerun with `go run ./build/compression-exp/experiment.go`. On a 2,471,305-byte Go WASI hello-world sample, Zstandard level 19 took about 70 ms and produced 657,000 bytes; level 3 took about 10 ms and produced 723,292 bytes. Deflate best-compression produced 725,455 bytes in about 165 ms; zlib best-compression produced 725,325 bytes in about 164 ms. The production compressor uses only Zstandard level 19. Results depend on the compiled module and machine.

WASI artifacts can be run by the Wasmtime runtime actor. `POST /api/wasm-builds/:id/runs` accepts an optional JSON `args` array and returns a run ID; poll `GET /api/wasm-runs/:id` for status, captured output, and `usage` (`fuel`, `peak_memory_bytes`, `duration_ms`, and `cpu_time_us`, the active CPU time of the run). Runs are owner-scoped, limited to two concurrent executions, and use Wasmtime fuel, memory, stack, and time limits. The Wasmtime Go binding requires CGO and a supported native toolchain; CGO-disabled builds report the runner as unavailable. On Windows, install a MinGW-w64 toolchain such as `choco install winlibs` (it includes clang), then build and test with `CGO_ENABLED=1` and `CC=clang`.

## Authentication endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/auth/register` | Create a local account and issue a session cookie |
| POST | `/auth/login` | Verify a local account and issue a session cookie |
| POST | `/auth/logout` | Clear the browser session cookie |
| GET | `/auth/google` | Begin Google OAuth when configured |
| GET | `/auth/google/callback` | Complete Google OAuth and issue a session cookie |
| GET | `/api/me` | Return the authenticated account profile |

Local passwords are stored as Argon2id hashes using 19 MiB memory, two iterations, and one lane. Passwords must be 12–1024 bytes. The signed HS256 JWT cookie is HttpOnly, SameSite=Lax, and has a configurable lifetime. Its default lifetime is 30 minutes. Logout clears the cookie; it does not revoke a copied JWT, which remains valid until expiry.

Google identities are keyed by Google's stable provider subject. The application does not automatically link a Google account to an existing local account based on email. The OAuth state and temporary Goth session are held in a short-lived, HttpOnly cookie; the session cookie uses the same Secure setting as the application JWT cookie.

OPA policy is embedded in `actors/authorization/policy.rego`. The default decision is deny; the current policy permits local account registration/login, configured Google sign-in, and a user reading their own profile. Change policy deliberately and cover new permissions with authorization tests.

## Actor boundaries

This section describes the code as it is today. The structure it is moving to, ports and adapters with no actor framework, is the standard in [docs/architecture.md](docs/architecture.md) ([ADR 0003](docs/adr/0003-ports-and-adapters.md)).

- `AuthenticationActor` hashes/verifies passwords and signs/verifies JWT claims.
- `AuthorizationActor` evaluates the embedded OPA policy.
- `PersistenceActor` owns the local Turso database and account/provider-identity records.
- `AccountingActor` receives structured application audit events and writes them through zap. Passwords, OAuth codes, tokens, and raw emails are excluded from audit fields.
- `RoutesActor` builds the HTTP route table and coordinates requests to those service actors from HTTP request goroutines. The HTTP server actor owns listener lifecycle; request handling stays outside GoAkt `Receive` turns.
- `TelemetryActor` configures OpenTelemetry. Actor receives and authentication flows create spans.
- `DockerControllerActor` checks the Docker daemon and executes bounded, isolated containers requested by workload actors.

Actor messages contain values rather than live HTTP request/response objects. The database is accessed only through the `sqlstore` adapter of the `app.Store` port. GoAkt's own runtime logs use the same zap logger through GoAkt's zap adapter; application audit events are delivered to the AccountingActor.

## Observability

The application exports OTLP/HTTP traces, metrics, and zap logs to `OTEL_EXPORTER_OTLP_ENDPOINT` (default `http://localhost:4318`) and Go runtime profiles to `PYROSCOPE_SERVER_ADDRESS` (default `http://localhost:4040`). `docker compose up -d` starts the OpenTelemetry Collector, Grafana, Loki, Tempo, Pyroscope, Prometheus, and Jaeger. Grafana is at `http://localhost:3000`, Prometheus at `http://localhost:9091`, Pyroscope at `http://localhost:4040`, and Jaeger at `http://localhost:16686`. All UIs are bound to localhost. Configure the Grafana credentials in `.env` before sharing or exposing the stack. The collector exports traces to both Tempo and Jaeger, logs to Loki's native OTLP endpoint, and metrics to a Prometheus scrape endpoint. Grafana provisions Loki, Tempo, Prometheus, and Pyroscope as data sources.

Run `go test ./...` and `go build ./...` to validate the project. The browser-level project workflow tests use Playwright-Go and Chromium. Install the matching browser once with `go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 install chromium`, then run them with `go test ./e2e -v`. The E2E test skips with setup instructions when its browser runtime is missing; set `JANUS_PLAYWRIGHT_REQUIRED=1` to make a missing driver or browser fail the test.
