# Janus Go

Janus Go is a set of services that talk over NATS JetStream: a gateway that serves HTTP, and the identity, projects, builds, runs, fleet and audit services behind it, each with its own Turso database where it keeps records. `cmd/janus` starts any set of them, in one process or one process each.

## Run locally

Copy `.env.example` to `.env`, set `JANUS_JWT_SECRET` to a private random value of at least 32 bytes, then run `go run ./cmd/janus` (the same as `go run ./cmd/janus serve all`, which runs every service in one process). The example deliberately leaves it blank, so the app fails startup until you set one. Keep `.env` out of version control. Set `JANUS_COOKIE_SECURE=true` when serving over HTTPS. Users, their sign-in methods and their GitHub Connections are kept by the identity service in its own Turso file, `data/identity.db` (`JANUS_IDENTITY_DATABASE_PATH`), and Projects, Snapshots and Imports by the projects service in `data/projects.db` (`JANUS_PROJECTS_DATABASE_PATH`). All are local-only database files; no cloud sync is configured.

Janus also needs NATS with JetStream. By default it embeds a server listening on `127.0.0.1:4222` (`JANUS_NATS_ADDRESS`) with its data in `data/nats` (`JANUS_NATS_DIR`). To use a standalone server instead, run `docker compose up -d nats` and set `JANUS_NATS_URL=nats://127.0.0.1:4222`; its configuration is `nats/nats.conf`. Either way the JetStream domain is `hub`.

Each service can run as a process of its own against that server: `go run ./cmd/janus serve identity`, then `serve projects`, `serve builds`, `serve runs`, `serve fleet`, `serve audit` and `serve gateway`, or several names in one process. Every process needs `JANUS_NATS_URL` and the same `JANUS_JWT_SECRET`; only `serve all` embeds a server. `docker compose --profile services up -d` starts one container per service, built from the `Dockerfile`, on the `nats` container.

Google sign-in is optional. Set `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and `GOOGLE_CALLBACK_URL` together to enable it; the homepage then shows a **Continue with Google** button. Register the callback URL with the Google OAuth client. For the public Janus site, use `https://janus.steven3zx.com/auth/google/callback` and set `JANUS_COOKIE_SECURE=true`.

GitHub sign-in and repository import are optional. Set `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, and `GITHUB_CALLBACK_URL` together. Register the configured callback URL (for example `https://janus.steven3zx.com/auth/github/callback`) in the GitHub OAuth App. GitHub sign-in requests `read:user` and `user:email`; connecting GitHub from Projects requests the broader `repo` scope so private repositories can be imported. Sign-in identities are matched by GitHub's provider ID, never by email. If the verified email already belongs to a Janus account, sign in there and connect GitHub from `/projects`.

## Agents (optional)

Agent enrollment needs a [Headscale](https://headscale.net) server (v0.29) with its policy set to database mode (`policy.mode: database`) and an API key (`headscale apikeys create`). Set `HEADSCALE_URL` and `HEADSCALE_API_KEY` together; `HEADSCALE_CONTROL_URL` is the address Agents are told to join and defaults to `HEADSCALE_URL`. At startup Janus creates the Headscale user `janus` and installs one policy that lets nodes tagged `tag:ctrl` open connections to any tagged node, and lets a tagged node reach only the control plane's leaf port (7102). An Agent cannot reach another Agent or anything else on the control plane. If Headscale cannot be reached at startup, Janus runs with Agent enrollment off and says why in the log.

A signed-in Owner calls `POST /api/agents/enrollment-codes` for a single-use code (valid 15 minutes). The Agent's command line calls `POST /api/agents/enroll` with `{"code", "name"}` and no session, and receives a single-use pre-auth key (valid one hour), the tag its node carries and the control URL. `GET /api/agents` lists the Owner's Agents with `online`, `offline`, `pending` or `unknown`, and `DELETE /api/agents/{id}` removes the Agent's node from the tailnet. Deleting an Account removes its Agents the same way.

The live tests run against a real Headscale in Docker and real `tsnet` nodes: `go test -tags live ./internal/services/fleet/headscale`. They need Docker and internet access.

### The Janus Agent

`cmd/janus-agent` is the Agent, one binary (it needs cgo for Wasmtime, like the control plane). Build it with `go build -tags ts_omit_peerapiserver ./cmd/janus-agent`: without that tag, tsnet opens a peerAPI TCP listener on every host interface, so an Agent built without it refuses to join the tailnet. On Windows the binary carries an application manifest (`manifest_windows_*.syso`, regenerated with `go generate ./cmd/janus-agent`); tsnet panics without one. On the Owner's machine:

```
janus-agent enroll -server https://janus.example -folder ~/janus-files -name laptop <code>
janus-agent run
janus-agent pause      # stop serving; the running Agent follows within a second
janus-agent resume
janus-agent status
```

`enroll` trades the code from the web UI for a pre-auth key and joins the tailnet once. `-folder` is the one host folder every Deployment may read and write; modules see it as `/` and can reach nothing outside it. The Agent keeps its settings, its tailnet node and its Deployments under `-state` (default: `janus-agent` in the user's configuration directory), so `run` after a restart serves the same Deployments, paused or not. It opens no listener on the internet. On the tailnet it listens only on port 7101 for HTTP requests to Deployments (the `Janus-Deployment` header names one), and accepts only callers tagged `tag:ctrl`. Its link to the control plane is NATS: the Agent runs an embedded NATS server as a leaf of the hub and dials the control plane's port 7102 through a loopback bridge (`internal/platform/leafbridge`). Commands, state and Usage travel on the subjects in section 6 of [docs/architecture.md](docs/architecture.md).

A Deployment is served per request (ADR 0002) through CGI/1.1 (ADR 0004): the request arrives in `REQUEST_METHOD`, `PATH_INFO`, `QUERY_STRING`, `CONTENT_TYPE`, `CONTENT_LENGTH`, `HTTP_*` and the other CGI variables, with the body on stdin; the module writes header lines (`Status: 404` sets the status), a blank line and the body to stdout. Each request gets the Template's fuel and memory, 30 seconds, and 10 MiB bodies; an Agent runs 16 at once. Every run is reported to the control plane with its fuel, peak memory, duration, CPU time, network bytes (request and response, headers and body) and status (`ok`, `failed`, `out_of_fuel` or `timeout`).

With Headscale configured, the control plane joins the tailnet as its own `tag:ctrl` node (state in `JANUS_TAILNET_DIR`, default `data/tailnet`) and accepts every enrolled Agent's leaf connection. `/agents` lists the Owner's Agents with a Pause or Resume button and issues Enrollment Codes; the API is `POST /api/agents/{id}/pause`, `POST /api/agents/{id}/resume` and `GET /api/agents/{id}/runs`, and `GET /api/agents` also shows whether each Agent is connected and paused.

`go test -tags live ./e2e/agentlive` runs the real `janus-agent` binary against Headscale in Docker: it enrolls, deploys a Go program built for WASI, serves it over the tailnet, checks the folder confinement and the usage reports, pauses and resumes from the API and the command line, and restarts the Agent.

## Projects and repository imports

Signed-in users can create an empty project at `/projects/new` or create a project and import a GitHub repository. The project page lists repositories, branches, and the 50 newest commits for a branch. Imports are jobs of the projects service (`internal/services/projects`) on the `JOBS` stream: they survive restarts, are handed to another worker if one stops, and show their current stage. The service fetches the selected commit with pure-Go Git, scans without running repository code, and stores tracked file contents in `projects.db`. The per-snapshot limits are 256 MiB total uncompressed file content, 50,000 files, and 16 MiB per file. The five newest successful snapshots are retained per project; an import interrupted part-way resumes from what it had stored, and fails after four tries. The project page lists retained snapshots and lets the owner reactivate an earlier revision without reimporting it.

The initial scanner recognizes Go, Python, Rust, and JavaScript/TypeScript conventions. It suggests entrypoint candidates for the user to confirm. Repository metadata, selected branch and commit, language, stars, forks, file count, and bytes are shown after import. GitHub access tokens are encrypted in `identity.db` with AES-GCM using a key derived from `JANUS_JWT_SECRET`; changing that secret requires reconnecting GitHub. Disconnecting GitHub removes the stored token.

The pages are served by the Qwik frontend in `janus/web`, which the gateway reaches through `JANUS_WEB_UPSTREAM`; the gateway itself serves only the JSON API and the auth endpoints. Authenticated JSON endpoints include `GET/POST /api/projects`, `GET/PUT/DELETE /api/projects/{id}`, `GET /api/github/repos`, `GET /api/github/branches?repository=owner/repo`, `GET /api/github/commits?repository=owner/repo&branch=main`, `POST /api/projects/{id}/imports`, `GET /api/imports/{job_id}`, `POST /api/imports/{job_id}/retry`, `GET /api/projects/{id}/snapshots`, `POST /api/projects/{id}/snapshots/{snapshot_id}/activate`, `POST /api/projects/{id}/entrypoint`, `POST /api/projects/{id}/wasm-builds`, `GET /api/wasm-builds/{job_id}`, `POST /api/wasm-builds/{job_id}/retry`, `GET /api/wasm-builds/{job_id}/artifact`, and `DELETE /api/account`. All project reads, build status, and artifacts are scoped to the signed-in owner.

Deployments: `POST /api/projects/{id}/deployments` deploys a WASM Build to an Agent (a redeploy replaces the live Deployment once the new one is healthy), `GET /api/deployments/{id}` reads one, `POST /api/deployments/{id}/stop` stops it, and `POST /api/deployments/{id}/rollback` deploys one of the Project's last five stopped Deployments again as a new Deployment.

### WebAssembly builds

Imported Rust snapshots can be built from the project page for `wasm32-unknown-unknown` or `wasm32-wasip1`. Imported Go snapshots can be built for `wasip1-wasm` when the repository has a root `go.mod`; the build compiles the package of the confirmed Entrypoint (for example `cmd/server/main.go`), or the root package when none is set. The Scan reports which main packages can build for WASI and why the others cannot (`internal/domain/goanalysis`). Build jobs run asynchronously, show bounded compiler output and status, and store the resulting `.wasm` files in a Zstandard-compressed ZIP (`.zip.zst`) in the local database. Decompress the Zstandard frame to recover the ZIP archive. Import does not execute repository code; builds run explicitly in the isolated builder containers.

The builds service (`internal/services/builds/docker`) is the single owner of Docker daemon checks and container execution. At startup it checks the engine and, when unavailable, runs `docker desktop start --timeout 60s`, then verifies the engine again. Startup fails if Docker Desktop cannot be started or the engine remains unavailable; this requires a Docker CLI with the Docker Desktop CLI plugin. Builds run as plain calls with fixed isolation settings, with one adapter per Template (Rust, Go, JavaScript/TypeScript) and separately configured trusted local images: `JANUS_WASM_BUILDER_IMAGE` and `JANUS_GO_WASM_BUILDER_IMAGE`. Build them with `docker build -f build/Dockerfile.wasm-rust -t janus-rust-wasm-builder:local .` and `docker build -f build/Dockerfile.wasm-go -t janus-go-wasm-builder:local .`. Each container has no network, a read-only root filesystem, a read-only source mount, no writable host mount during compilation, dropped Linux capabilities, a PID limit, 2 GiB memory, a 1 GiB temporary build filesystem, and a 10-minute deadline. Cargo is offline and locked; Go module lookup is also offline. Repositories must vendor dependencies or the trusted image must include dependency caches. The builder wraps the generated ZIP in a Zstandard frame at level 19 and stores a `.zip.zst` artifact. The output archive is limited to 128 MiB. JavaScript and TypeScript projects (the Node and Bun templates) use a third image, `JANUS_JS_WASM_BUILDER_IMAGE` (`janus-js-wasm-builder:local`, built from `build/Dockerfile.wasm-js`): Bun bundles the Entrypoint into one file and Javy compiles it with the QuickJS interpreter to a `wasm32-wasip1` module. Node and Bun APIs such as `node:fs` are not available, and dependencies must be vendored. Their runs start with a 100 billion fuel limit because the interpreter spends far more fuel per operation. Other source languages and project-specific build settings are not supported yet.

The compression experiment can be rerun with `go run ./build/compression-exp/experiment.go`. On a 2,471,305-byte Go WASI hello-world sample, Zstandard level 19 took about 70 ms and produced 657,000 bytes; level 3 took about 10 ms and produced 723,292 bytes. Deflate best-compression produced 725,455 bytes in about 165 ms; zlib best-compression produced 725,325 bytes in about 164 ms. The production compressor uses only Zstandard level 19. Results depend on the compiled module and machine.

WASI artifacts can be run by the runs service (`internal/services/runs`, over `internal/platform/wasmrun`). `POST /api/wasm-builds/:id/runs` accepts an optional JSON `args` array and returns a run ID; poll `GET /api/wasm-runs/:id` for status, captured output, and `usage` (`fuel`, `peak_memory_bytes`, `duration_ms`, and `cpu_time_us`, the active CPU time of the run). Runs are owner-scoped, limited to two concurrent executions, and use Wasmtime fuel, memory, stack, and time limits. The Wasmtime Go binding requires CGO and a supported native toolchain; CGO-disabled builds report the runner as unavailable. On Windows, install a MinGW-w64 toolchain such as `choco install winlibs` (it includes clang), then build and test with `CGO_ENABLED=1` and `CC=clang`.

## Authentication endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/auth/register` | Create a local account and issue a session cookie |
| POST | `/auth/login` | Verify a local account and issue a session cookie |
| POST | `/auth/logout` | Clear the browser session cookie |
| GET | `/auth/google` | Begin Google OAuth when configured |
| GET | `/auth/google/callback` | Complete Google OAuth and issue a session cookie |
| GET | `/auth/github` | Begin GitHub sign-in when configured |
| GET | `/auth/github/callback` | Complete GitHub sign-in or a GitHub Connection |
| GET | `/auth/github/connect` | Connect GitHub to the signed-in account with the `repo` scope |
| POST | `/auth/github/disconnect` | Remove the stored GitHub token |
| GET | `/api/me` | Return the authenticated account profile |
| GET | `/healthz` | Liveness check, no session |

Local passwords are stored as Argon2id hashes using 19 MiB memory, two iterations, and one lane. Passwords must be 12–1024 bytes. The signed HS256 JWT cookie is HttpOnly, SameSite=Lax, and has a configurable lifetime. Its default lifetime is 30 minutes. Logout clears the cookie; it does not revoke a copied JWT, which remains valid until expiry.

Google identities are keyed by Google's stable provider subject. The application does not automatically link a Google account to an existing local account based on email. The OAuth state and temporary Goth session are held in a short-lived, HttpOnly cookie; the session cookie uses the same Secure setting as the application JWT cookie.

OPA policy is embedded in `internal/domain/authz/policy.rego`. The default decision is deny. The policy names each action it permits: registration and sign-in (local, Google, GitHub), connecting GitHub, reading the own profile and deleting the own Account, the Owner's actions on their Projects (create, read, update, delete, import, build, run, deploy), and the Agent actions (create an Enrollment Code, enroll, read, control, remove). Change policy deliberately and cover new permissions with authorization tests.

## Structure

janus-go is a set of services that talk over NATS JetStream, each with its own Turso database ([ADR 0005](docs/adr/0005-nats-jetstream-microservices.md)); the services, subjects, streams and dependency rules are in [docs/architecture.md](docs/architecture.md). The earlier ports and adapters ([ADR 0003](docs/adr/0003-ports-and-adapters.md)) are gone. There is no actor framework.

- `internal/domain` holds the pure rules, including authentication, authorization and the Agent's runtime; `internal/contract/<service>` holds the subjects and messages of each service; `internal/platform` holds NATS, Turso, tracing and the other plumbing; `internal/services/<service>` holds the services.
- `cmd/janus` is the control plane's composition root, and `cmd/janus-agent` the Agent's. It loads settings, starts telemetry, and runs a table of components (foundation, identity, projects and builds, runtime, web) in order, then stops them in reverse, waiting for running builds; the projects service leaves running imports for JetStream to hand out again. A startup failure part-way stops whatever already started.
- The gateway is `internal/services/gateway`: the HTTP server and routes, with a client of each service below it (`identityclient`, `projectsclient`, `buildsclient`, `fleetclient`, `runsclient`) and `auditclient`, which publishes audit events to the `AUDIT` stream, where the `audit` service writes them through zap; passwords, OAuth codes, tokens, and raw emails are excluded from audit fields. What runs on an Agent is in `internal/agent`.
- Spans cover startup, shutdown, requests and the gateway's calls. The tracing helpers are `tracing.StartSpan` and `tracing.RecordError` in `internal/platform/tracing`; the OpenTelemetry providers and the Pyroscope profiler are started by `cmd/janus`.

## Observability

The application exports OTLP/HTTP traces, metrics, and zap logs to `OTEL_EXPORTER_OTLP_ENDPOINT` (default `http://localhost:4318`) and Go runtime profiles to `PYROSCOPE_SERVER_ADDRESS` (default `http://localhost:4040`). `docker compose up -d` starts the OpenTelemetry Collector, Grafana, Loki, Tempo, Pyroscope, Prometheus, and Jaeger. Grafana is at `http://localhost:3000`, Prometheus at `http://localhost:9091`, Pyroscope at `http://localhost:4040`, and Jaeger at `http://localhost:16686`. All UIs are bound to localhost. Configure the Grafana credentials in `.env` before sharing or exposing the stack. The collector exports traces to both Tempo and Jaeger, logs to Loki's native OTLP endpoint, and metrics to a Prometheus scrape endpoint. Grafana provisions Loki, Tempo, Prometheus, and Pyroscope as data sources.

Run `go test ./...` and `go build ./...` to validate the project. The browser-level project workflow tests use Playwright-Go and Chromium. Install the matching browser once with `go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 install chromium`, then run them with `go test ./e2e -v`. The E2E test skips with setup instructions when its browser runtime is missing; set `JANUS_PLAYWRIGHT_REQUIRED=1` to make a missing driver or browser fail the test.
