# Architecture standard: services over NATS JetStream

This is the structure janus-go is being moved to, and the rules new code is reviewed against. The decision is recorded in [ADR 0005](adr/0005-nats-jetstream-microservices.md), which supersedes ports and adapters ([ADR 0003](adr/0003-ports-and-adapters.md)).

**Status:** current. The single process is gone: `internal/app` and `internal/adapters` are deleted, the gateway reaches every service only over NATS, and each service can run as a process of its own with `janus serve <service>`. Section 12 records how it got here.

## 1. The idea in five lines

- janus-go is a set of **services**. A service owns one part of Janus and the data for it.
- Services reach each other **only through NATS**: request and reply for questions and commands, JetStream streams for jobs, events and Usage.
- Each service owns **one Turso database** and is the only thing that opens it. No service reads another's tables.
- Inside a service there are **no ports and no adapters**. A service calls NATS, Turso, Docker, Git, GitHub or Wasmtime directly.
- Rules that more than one service needs live in `internal/domain`, which stays pure.

## 2. Vocabulary

| term | meaning here |
|---|---|
| service | a package under `internal/services` that owns some records and answers on its subjects. It runs in any process that starts it |
| gateway | the service that serves HTTP: HTML pages, the JSON API, cookies, sign-in redirects and authorization. It owns no data |
| contract | the subjects, message types and error codes of one service, in `internal/contract/<service>`. The only thing another service may import from it |
| endpoint | one request subject a service answers, registered with `nats.go/micro` |
| event | a fact a service publishes after it changed its data, on `janus.events.<service>.<fact>`, kept in the `EVENTS` stream |
| job | work that must outlive a restart (an Import, a Build). Its message waits in a work-queue stream until a worker acknowledges it |
| hub | the control plane's NATS server, with JetStream domain `hub` |
| leaf | an Agent's own embedded NATS server, connected to the hub as a leaf node, with JetStream domain `agent-<id>` |

## 3. The services

| service | owns (Turso database) | answers and does |
|---|---|---|
| `gateway` | nothing | HTTP, sessions as cookies, OAuth redirects, JWT checks, OPA authorization. Turns each request into NATS requests |
| `identity` | `identity.db`: `users`, `local_credentials`, `oauth_identities`, `github_connections` | register, sign in, resolve Provider Identities, the GitHub Connection and its token, starting Account deletion |
| `projects` | `projects.db`: `projects`, `project_snapshots`, `project_files`, `project_import_jobs` | Projects, Snapshots, Entrypoints, the GitHub repository lists, Imports (as a job worker) |
| `builds` | `builds.db`: `project_wasm_builds`, `project_checks` | WASM Builds and Checks (as a job worker, with Docker), Artifacts |
| `runs` | nothing; runs are kept in memory as today | one-off WASM runs with Wasmtime |
| `fleet` | `fleet.db`: `enrollment_codes`, `agents`, `deployments`, `run_usage` | enrollment with Headscale, Agent state, Deployments, Usage, commands to Agents |
| `audit` | nothing | consumes `AUDIT` and writes each Audit Event through zap and OpenTelemetry logs |

The Janus Agent (`cmd/janus-agent`) is not a service of the control plane. It runs its own leaf server and talks to `fleet` only through the subjects in section 6.

A service splits further only when one of its tables has no rule in common with the rest, as Usage will when billing arrives (M3). Say why in the commit.

## 4. Messages

### Subjects

| kind | subject | example |
|---|---|---|
| request | `janus.<service>.<noun>.<verb>` | `janus.projects.snapshot.files` |
| event | `janus.events.<service>.<fact>` | `janus.events.identity.account_deleted` |
| job | `janus.jobs.<kind>` | `janus.jobs.import` |
| audit | `janus.audit.<service>` | `janus.audit.builds` |
| Agent | `janus.agent.<agent id>.<what>` | `janus.agent.7f3c….command` |

Every subject a service uses is a constant in its contract package. Nothing builds a subject from a string literal elsewhere.

### Encoding

Bodies are JSON, one Go struct per request, reply and event in the contract package. JSON keeps the `nats` command line useful for debugging, and the volumes are small. The Agent protocol may move to protobuf later if its traffic shows up in profiles.

### Errors

A failed request answers with the `nats.go/micro` error headers. The code is one of `400` (invalid), `403` (not allowed), `404` (not found or not the caller's), `409` (conflict, such as a job already active), `503` (a dependency is down) and `500`. The contract package turns each code back into a typed Go error, so a caller writes `errors.Is(err, contract.ErrNotFound)`. A request with no responder is `503`.

### Ownership

Every request about an Owner's records carries `owner_id`. The gateway takes it from the verified session and nothing else; other services trust it because only control-plane credentials can publish on `janus.*` (section 7). A record that is not the caller's answers `404`, never `403`, as today.

### Large values

A NATS message stays under 1 MiB. Snapshot files are paged (`janus.projects.snapshot.files` takes a cursor). Artifacts and Deployment modules go into the JetStream Object Store, and messages carry the object name. Turso stays the record of truth; an object is a copy for transport and may be deleted and rebuilt from Turso.

### Tracing

Every request, event and job carries the W3C trace context in NATS headers (`traceparent`, `tracestate`). `internal/platform/natsx` injects it on publish and starts a span from it on receive, so one HTTP request is one trace across services.

## 5. JetStream

| name | kind | subjects | retention | consumer |
|---|---|---|---|---|
| `JOBS` | stream | `janus.jobs.>` | work queue | `projects` (imports) and `builds` (builds), each a durable pull consumer on its own subject |
| `EVENTS` | stream | `janus.events.>` | limits, 7 days | one durable consumer per interested service |
| `AUDIT` | stream | `janus.audit.>` | limits, 7 days | `audit` |
| `artifacts` | object store | | | written by `builds`, read by `runs` and `fleet` |
| `modules-<owner id>` | object store | | | written by `fleet`, read by that Owner's Agents |

The job rules:

- Turso keeps the job's status and its state machine (`domain/jobs`, compare-and-swap on `status`, as today). The stream only holds the work.
- A worker keeps a job it is running alive with in-progress acknowledgements, and acknowledges it only after its final status is stored. A worker that dies leaves the job unacknowledged, and JetStream hands it to another worker after `AckWait`. This replaces `Resume` at startup.
- A job message carries the job ID only. A worker that receives a job whose stored status is already final acknowledges it and does nothing, so redelivery is safe.
- `MaxDeliver` bounds retries. A job that runs out of deliveries is marked failed by the advisory consumer of the owning service.
- Cancelling an Owner's jobs is an event (`janus.events.identity.account_deleting`); each worker stops the matching running jobs and marks the queued ones cancelled.

The event rules:

- A service publishes an event after its transaction commits. If publishing fails, the change stands and the service retries the publish from an `outbox` table in its own database, so an event is never lost and may arrive twice.
- A consumer applies each event idempotently, keyed by the event's `Nats-Msg-Id`.

## 6. Agents

The Agent embeds a NATS server with JetStream (domain `agent-<id>`) and connects it to the hub as a leaf node over the tailnet. Its local stream `USAGE` keeps Usage while the hub is out of reach, which replaces `Requeue` and the gRPC stream.

| what | how |
|---|---|
| commands (deploy, stop, pause, resume) | request to `janus.agent.<id>.command`, answered by the Agent. No responder means the Agent is offline, as today |
| module bytes | the deploy command names an object in `modules-<owner id>`; the Agent reads it through domain `hub` |
| state | the Agent puts its state (paused, hosted Deployments) under key `<id>` of the hub KV bucket `agent_state`; `fleet` watches the bucket |
| Usage | the Agent publishes to its local `USAGE` stream; `fleet` reads it through domain `agent-<id>` with one durable consumer per Agent, and acknowledges each report after it is stored |
| edge requests | unchanged: HTTP to port 7101 on the tailnet (ADR 0002, ADR 0004) |

tsnet gives a `net.Listener` and a dialer, not a socket the NATS server can bind. Each side bridges: the hub accepts leaf connections on its tsnet node and copies them to the hub's local leaf port; the Agent's leaf remote points at a local port that dials the hub through its own tsnet node. Only nodes tagged `tag:ctrl` are reachable from Agents, as the Headscale policy already enforces.

## 7. Security

- NATS uses decentralised JWT authentication. The operator and the `JANUS` account keys are configuration of the hub; `fleet` holds the signing key of the `AGENTS` account.
- Control-plane services connect as users of `JANUS`, which may publish and subscribe on `janus.>` and the JetStream API.
- At enrollment, `fleet` issues the Agent a user JWT in `AGENTS` that allows only `janus.agent.<id>.>`, its own reply inbox, the `agent_state` key `<id>`, and reading `modules-<owner id>`. The `JANUS` account imports `janus.agent.>` from `AGENTS`. An Agent cannot see another Agent's subjects, a service's subjects or another Owner's modules.
- An Agent's credentials are revoked when it is removed or its Account is deleted, by adding its user key to the account's revocation list.

## 8. Layout

```
cmd/janus/                  one binary. `janus serve <service>...` starts the named services against
                            the NATS server in JANUS_NATS_URL; `janus serve all`, which plain `janus`
                            also means, runs every service and embeds a NATS server
cmd/janus-agent/            the Agent
internal/
  domain/                   pure types and rules, unchanged: jobs, artifact, cgi, templates, scan,
                            usage, manifest, deployments
  contract/<service>/       subjects, message types, error codes of one service
  platform/
    natsx/                  connect, the embedded server, micro helpers, trace headers, stream setup
    natstest/               starts an embedded JetStream server for a test
    tursodb/                open a service's database, run its migrations, the outbox
    tracing/                spans on Janus's shared instrumentation scope
    tailnode/               a Janus process as a node of its own on the tailnet
    wasmrun/                Wasmtime runs of WASI modules (cgo)
  services/
    gateway/  identity/  projects/  builds/  runs/  fleet/  audit/
    gateway/<name>client/   the gateway's NATS clients of the other services, and the
                            in-memory fakes (<name>test) its tests use
  agent/                    what runs on an Agent: disk (local state), edge (the HTTP
                            endpoint), link (the NATS link, both ends), wasmtime (the Runner),
                            and the test doubles linktest, statetest and runnertest
                            (the Agent's runtime itself is internal/domain/agent)
```

Rules for the tree:

- A service package holds its endpoints, its SQL and schema, and its work. Split it into sub-packages only when it is hard to navigate.
- A service is built with `New(Config)` and exposes `Start(ctx, *nats.Conn) error` and `Stop(ctx) error`. Configuration arrives through `Config`; a service does not read environment variables.
- Package names say what the package provides. No `util`, `common`, `types` or `model`.

## 9. Dependency rules

1. `domain` imports only the standard library and pure libraries.
2. `contract` imports the standard library and `domain`.
3. `platform` imports no service and no contract.
4. A service imports `contract`, `domain` and `platform`. **A service never imports another service.** It may import another service's contract.
5. `cmd/janus` is the only package that imports services. `cmd/janus-agent` imports `agent`, `platform`, `domain` and `contract/fleet`.
6. `agent` packages import `domain`, `contract`, `platform` and each other, never a service or `cmd`. Only `cmd` imports them (and a test may import a test double).

`internal/archtest` enforces these over the `go list` import graph, and the guardrails count the remaining debt: SQL clocks, error text compared by string, and exported interfaces in a service. The gateway declares the interfaces it calls (`fleetService`, `runsService` and the rest) unexported, in the contract's and the domain's types, and its clients in `services/gateway/*client` implement them.

## 10. Concurrency inside a service

The measured rules from ADR 0003 still hold inside one process:

- Shared state gets a mutex with short critical sections; never do I/O or a NATS request while holding one.
- Counters and flags are atomics; a channel is for a real hand-off.
- Each database has one connection, so a service's writes take turns. That is now a limit per service, not for the whole of Janus.
- Every goroutine has an owner that waits for it, and `Stop` waits for running work. A subscription is drained, not closed, on `Stop`, so a message being handled finishes.
- A handler that runs untrusted or fallible work recovers panics and answers `500` or fails the job.

Across services there is no lock. A rule that spans services is a sequence of events, and each step must be safe to repeat.

## 11. Testing standard

**Service tests** start an embedded JetStream server (`natstest.Start(t)`), a temp Turso file and the service, then drive it through its contract: requests, published events and job messages. They are the main tests. Outside systems are replaced at the wire: `httptest` servers for GitHub and Headscale, a temporary Git repository for sources, and the real Wasmtime. Nothing polls with `time.Sleep`; wait on the reply, a published event or the job's final status.

**Flow tests** start every service in one process with `janus serve all` against an embedded server and drive them through the gateway's HTTP API, as the end-to-end tests do now. `TestEachServiceRunsInItsOwnProcess` (`cmd/janus`) builds the binary and starts the audit, identity, projects and runs services and the gateway as five processes on one server, so each answer the gateway gives crosses processes. The gateway's own tests run it against the real identity service (`cmd/janus/identityfixture`), the real fleet service (`cmd/janus/fleetfixture`) and fakes of projects and builds that answer in the contract's types.

**Live tests** are tagged `live` and use the real outside systems: Docker builders, GitHub with a test Account, a real Headscale, the real Agent binary as a leaf on a real tailnet. They clean up what they create.

Each contract package has a test that encodes and decodes every message, so a field rename is caught before two services disagree.

## 12. Migration

Done: all ten steps. The notes below say what each step did when it shipped, in the words of the time, so they still mention `janus.db`, the Owner registry and `janus migrate split`. Step 10 removed those with the monolith. An install that still has its data in `janus.db` must run `janus migrate split` once from a build before step 10 (the last such commit is `d023ea2`), then start the current build.

Each step ships on its own and keeps the HTTP end-to-end tests green. A step moves a part of `internal/app` into a service and points the old code, or the gateway's handlers, at it over NATS. The tickets are under ENG-114, one per step (ENG-115 to ENG-124).

1. **NATS foundation (ENG-115).** Embed a JetStream server in `cmd/janus` (or connect to `JANUS_NATS_URL`), add `platform/natsx` and `platform/natstest`, trace propagation, a `nats` service in `docker-compose.yml`, and the archtest rules for the new layers.
2. **Audit (ENG-116).** Audit Events are published to `AUDIT`; the `audit` service writes them. `auditlog` goes away.
 Done: `contract/audit` holds the subjects and the message, `services/audit` declares `AUDIT` and writes each event with a durable consumer, and `adapters/auditnats` is the `app.Audit` the old code records through. It writes through the logger until the audit service starts and whenever a publish fails, so no event is lost; `platform/auditlog` is the writer both share. `cmd/janus` starts the service right after NATS and stops it before NATS, leaving unwritten events in the stream for the next start. `audittest` stays until ENG-124, for the old tests.
3. **Identity (ENG-117).** `identity` owns `identity.db`. `janus migrate split` copies its tables out of `janus.db`. The gateway's sign-in and GitHub-connection handlers call it.
 Done: `services/identity` answers the subjects in `contract/identity` from `identity.db` (schema by `platform/tursodb` migrations; GitHub tokens encrypted under the same key as before, so copied tokens still decrypt). The old core reaches it through a new `Identities` port, implemented by `adapters/identitynats` with `identitytest.Memory` and `identitytest.Run` beside it; the `Store` port lost its identity methods. Because every owned table in `janus.db` still refers to `users(id)` and Account deletion relies on that cascade, `janus.db` keeps `users` as a registry of Owners: `Store.AddOwner` records one, and `app.Identity` calls it whenever a User is created or signs in. `app.Accounts.Delete` deletes the Owner's records in the Store first and the User in `identity` last, so a failure leaves an Account that can still sign in and finish deleting itself. The registry goes away as the owned tables move to their own services. `janus` refuses to start while `janus.db` holds Users that `identity.db` lacks, and `janus migrate split` runs once. Requests and endpoints use `natsx.Call` and `natsx.AddEndpoint` (JSON over `nats.go/micro`, statuses from `contract.Error`).
4. **Projects and Imports (ENG-118).** `projects` owns `projects.db`; Imports become jobs on `JOBS`. The GitHub token comes from `identity` on request.
 Done: `services/projects` answers `contract/projects` from `projects.db` and runs Imports. An Import and its job message are written in one transaction through the outbox (`platform/outbox`), a worker takes the job from `JOBS` (durable consumer `projects-imports`, one message per worker at a time, in-progress acks while it runs), and the job is acknowledged only once the Import is finished. A redelivered job resumes the Import from its Staging Snapshot; its last delivery fails it rather than leave it queued. The GitHub client and the Git clone moved into the service with the import pipeline, and `adapters/githubapi`, `adapters/gitsource` and `adapters/hosttest` are gone. Snapshot files are paged with a content offset, so a file larger than a message comes in pieces. Deleting a Project, an Owner's Projects or a Snapshot dropped by the five-Snapshot limit publishes `project_deleted` or `snapshot_deleted` on `EVENTS`. The old core reaches the service through the `Catalog` port (`adapters/projectsnats`, with `catalogtest.Memory` and `catalogtest.Run`). Because WASM Builds and Deployments in `janus.db` still refer to `projects` and `project_snapshots`, those tables stay there as references, like the Owner registry of step 3: `Builds.Request` records the Project and Snapshot it builds, and `adapters/projectevents` applies the deletion events, which removes the Builds and what refers to them. `janus migrate split` now also copies the project tables and queues the unfinished Imports; it skips a service whose database exists. The single-connection contention measurement moved to the projects service (`go test -tags contention ./internal/services/projects`).
5. **Builds (ENG-119).** `builds` owns `builds.db`; Builds become jobs; Artifacts go to the `artifacts` object store; source files are paged from `projects`.
 Done: `services/builds` answers `contract/builds` from `builds.db` and runs Builds. A Build and its job message are written in one transaction through the outbox. A worker takes the job from `JOBS` (durable consumer `builds-builds`), reads the source a page at a time from `projects`, compiles it with the Docker compiler in `services/builds/docker`, and runs its Check. The Artifact is kept in `builds.db` and copied to the `artifacts` object store under the Build ID; a copy that is missing is restored from `builds.db` when it is read. Deleting a Project, a Snapshot or an Owner deletes its Builds and Artifacts; `builds-projects` consumes the projects events. The single process reaches the service through the `WASMBuilds` port (`adapters/buildsnats`, with `buildstest.Fake`). `adapters/buildevents` applies `build_changed` and `check_changed` to the Store, which keeps the references Deployments refer to, so Runs and the Fleet read Artifacts through the same port. `janus migrate split` copies `project_wasm_builds` and `project_checks` into `builds.db` and queues the unfinished Builds. `adapters/dockerbuild`, the Builder and Checker ports and the in-process job runner are gone.
6. **Runs (ENG-120).** `runs` reads Artifacts from the object store.
   Done: `services/runs` answers `contract/runs` on `janus.runs.run.request` and `janus.runs.run.get`. It asks `builds` whether the Build is the Owner's and complete (a Build that is not the Owner's is `404`), reads the Artifact from the `artifacts` store, restoring a missing object from `builds.db` as `fleet` does, and takes its limits from the Snapshot's template in `projects`. It runs the module with Wasmtime in the process. The engine moved to `platform/wasmrun`, which imports no app or service type, so `adapters/wasmtime` only translates the app types. `adapters/runsnats` is the `app.WASMRuns` port the gateway uses. Runs stay in memory with the same limits: two at once, the newest 100 kept, Template fuel and memory. `internal/app/runs.go` keeps only the port and the Run type. A service test runs a real WASI module over NATS.
7. **Fleet (ENG-121).** `fleet` owns `fleet.db`: enrollment, Agents, Deployments, Usage. It still reaches Agents through `agentgrpc`.
 Done: `services/fleet` answers `contract/fleet` from `fleet.db` and owns the Headscale client (`services/fleet/headscale`). Deployments read their Build and Check from `builds` and their Snapshot's template limits from `projects` over NATS, and read the module from the `artifacts` store. A Deployment keeps its Snapshot ID, so `project_deleted` and `snapshot_deleted` stop and remove the Deployments that refer to them. The single process reaches the service through the `Fleet` port (`adapters/fleetnats`). The fleet types are in `domain/fleet`, so `app` does not import the contract. `janus migrate split` copies the fleet tables and keeps each Deployment's Snapshot. The Agent link still goes through `agentgrpc`, as step 8 changes.
8. **Agent link (ENG-122).** The Agent becomes a leaf node with NATS credentials from enrollment; `agentgrpc` and `agentpb` go away.
 Done: the Agent runs an embedded NATS server as a leaf of the hub, in its own JetStream domain, and joins through a bridge over its tailnet node (`platform/leafbridge`). Its leaf credentials, and the credentials its own clients use, come from enrollment, which also hands it the public trust. `adapters/agentnats` carries the commands (a request on `janus.agent.<id>.command`), the state (the key of its ID in `agent_state`) and the Usage (its USAGE stream, read by the fleet from the Agent's domain). Modules are objects in `modules-<owner>`, staged by the fleet and read by the Agent. The fleet reaches the Agents as a user of AGENTS, since their streams and buckets live there. `agentgrpc` and `agentpb` are gone. The Agent listens on no host port beyond the loopback leaf bridge.
9. **Account deletion (ENG-123).** `identity` publishes `account_deleting`, each service tears down and confirms with its own event, and `identity` deletes the Account when all have confirmed. Refusal while jobs are active is a question `identity` asks `projects` and `builds` first.
10. **Retire the monolith (ENG-124).** The gateway is the last caller of `internal/app`; once it calls only NATS, `internal/app`, `internal/adapters` and the ADR 0003 archtest rules are deleted, and each service can run as its own process.
 Done: the gateway (`services/gateway`) declares the interfaces it calls, in the contract's types, and its clients (`services/gateway/*client`) implement them over NATS. Deleting an Account is a request to identity, which the gateway repeats until the User is gone. The Store, the Owner registry (`identityowners`), `buildevents`, `projectevents`, `sqlstore`, `janus migrate split`, each service's `Split` and `NeedsSplit`, and `JANUS_DATABASE_PATH` are gone, so nothing reads `janus.db`. The use cases in `internal/app` went, with the clients and fakes typed to its ports; `catalogtest` and `buildstest` now answer in the contract's types, and the identity tests run the real service. The Agent's packages moved under `internal/agent`, the tailnet node under `platform`, and spans to `platform/tracing`. The archtest keeps section 9 (rules 9.1 to 9.6) and drops ADR 0003's rules. `janus serve <service>...` runs any set of services on the server in `JANUS_NATS_URL`; `janus serve all` embeds NATS. `docker compose --profile services up -d` starts one container per service (`Dockerfile`). Not yet run with every service in its own process: the Playwright browser tests (they skip without Chromium) and the Agent live test (`-tags live`, needs Docker and Headscale); the flow test above runs the five services that need neither.

## 13. Open points

- **Hosting the hub.** One NATS server is enough for M1. A three-node cluster answers ENG-56 when the control plane needs more than one machine.
- **Turso replicas.** Each database is a local file. Turso's sync can replicate a service's database later without changing its SQL.
- **Edge over NATS.** Edge requests could become NATS requests to the Agent, which would remove port 7101. Not before the Distributed Network needs it.
