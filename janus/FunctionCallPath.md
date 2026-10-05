# Function Call Path

This document maps the runtime call paths starting from `main.go` and following the major execution paths in `janus-api`.

## Scope

- API process entry: `backend/janus-api/cmd/janus-api/main.go`
- Core downstream flows: routing, handlers, state, and in-process build/deploy processing

## 1. API Process Boot Path (`backend/janus-api/cmd/janus-api/main.go`)

```text
main()
  -> cli.Run(...)
       -> bootstrap.Run(serve)
            -> config.FromEnv()
            -> BuildContainer(cfg, serve)
                 -> telemetry.Setup(...)
                 -> postgres.OpenDB(...)
                 -> postgres.ApplyMigrations(...)
                 -> objectstore.NewFromEnv()
                 -> postgres.AssembleStateWithBackends(...)
                 -> NewRuntime(...)
                 -> http.NewServerWithDependencies(...)
            -> Runtime.Start(ctx)
                 -> background.Supervisor.Start(ctx)
            -> http.Server.ListenAndServe()
```

### Shutdown path

```text
ctx.Done() (signal)
  -> httpServer.Shutdown(timeout=10s)
  -> apiServer.Shutdown(timeout=10s)
  -> state.Close()
```

Note: build/deploy execution is coordinated by the in-process worker supervisor inside `janus-api`. Operations remain durable in Postgres.

## 2. Request Execution Stack

For all HTTP traffic handled by the API server:

```text
incoming request
  -> otelhttp wrapper
  -> logging middleware
       -> set X-Request-Id
       -> call next handler
       -> record Prometheus metrics
       -> state.RecordTrace(route, method, status, duration)
  -> domainProxyOrNext(mux)
       -> if /api/* or /healthz or /metrics: route to mux
       -> else try state.ResolveDomainRuntimeEndpoint(host)
            -> if found: reverse proxy to runtime endpoint
            -> if not: continue to mux
  -> matched route handler
```

## 3. Route-to-Handler-to-State Paths

## Auth and health

```text
GET /healthz
  -> Handler.Healthz
  -> writeJSON({ok:true, ts:...})

POST /api/v1/auth/signup
  -> Handler.AuthSignup
  -> validateEmail + validatePassword
  -> Handler.submitOperation
  -> state.CreateOperation(...)
  -> state.CreateUser

POST /api/v1/auth/signin
  -> Handler.AuthSignin
  -> Handler.submitOperation
  -> state.CreateOperation(...)
  -> auth use case

POST /api/v1/auth/signout
  -> Handler.AuthSignout
  -> clear janus_access cookie

POST /api/v1/auth/refresh
  -> Handler.AuthRefresh
  -> Handler.UserFromRequest
  -> Handler.SetAccessCookie

GET /api/v1/auth/me
  -> Handler.AuthMe
  -> Handler.UserFromRequest
  -> state.FindUserByID
  -> writeJSON(user)
```

## Projects

```text
GET /api/v1/projects
  -> Handler.ProjectsRoute
  -> state.ListProjects

POST /api/v1/projects
  -> Handler.ProjectsRoute
  -> Handler.submitOperation
  -> state.CreateOperation(...)
  -> state.CreateProject

PATCH /api/v1/projects/{id}
  -> Handler.ProjectByIDRoute
  -> Handler.submitOperation
  -> state.CreateOperation(...)
  -> state.UpdateProject

DELETE /api/v1/projects/{id}
  -> Handler.ProjectByIDRoute
  -> Handler.submitOperation
  -> state.CreateOperation(...)
  -> state.DeleteProject
```

## Build and deployment APIs

```text
POST /api/v1/repos/import
  -> Handler.RepoImport
  -> Handler.submitOperation
  -> state.CreateOperation(...)
  -> state.CreateBuild(...)

GET /api/v1/builds
  -> Handler.BuildsRoute
  -> state.ListBuilds(projectId?)

GET /api/v1/builds/{buildId}/logs
  -> Handler.BuildLogsRoute
  -> state.GetBuild(buildId)
  -> state.GetBuildLogs(buildId)
  -> state.GetBuildStatusEvents(buildId)

POST /api/v1/deployments
  -> Handler.DeploymentsRoute
  -> Handler.submitOperation
  -> state.CreateOperation(...)
  -> state.CreateDeployment(...)

GET /api/v1/deployments
  -> Handler.DeploymentsRoute
  -> state.ListDeployments(projectId?)

GET /api/v1/domains
  -> Handler.DomainsRoute
  -> state.ListDomains()
```

## 4. In-Process Worker Path

```text
Runtime.Start(ctx)
  -> supervisor.Start(ctx)
       -> registerRunner(...)
       -> schedule build/deployment polling loops
       -> periodic heartbeat updates
       -> claimBuilds / claimDeployments
       -> background use cases execute jobs in-process
```

## 5. Build Execution Path

```text
supervisor.claimBuilds
  -> state.ClaimBuildJob
  -> state.ExecuteBuildJob
       -> startBuildHeartbeat goroutine
       -> validateBuildSource(provider, repoURL)
       -> state.runBuildWithContext
            -> state.providerFor(provider)
            -> provider.Clone(...)
            -> resolveDockerfileWithStrategy(...)
            -> state.setBuildStrategy
            -> state.buildExecutor.Build(...)
            -> state.setBuildStatusWithTag(... success ...)
            -> insert resource_profiles row
       -> on any error: state.failBuild + append logs/events
```

## 6. Deployment Execution Path

```text
supervisor.claimDeployments
  -> state.ClaimDeployment
  -> state.ExecuteDeployment
       -> startDeploymentHeartbeat goroutine
       -> state.GetBuild(buildJobId)
       -> state.runtime.Launch(...)
            -> runContainerOutputCommand("podman", "run", ...)
            -> waitForContainerRunning
            -> detectContainerEndpoint
       -> state.setDeploymentStatus("running")
       -> state.upsertDeploymentRuntime(... endpoint ...)
       -> on error:
            -> setDeploymentStatusWithError("failed", err)
            -> upsertDeploymentRuntime(... failed ...)
            -> append deployment failure log line to build log stream
```

## 7. Reverse Proxy Runtime Resolution Path

For non-API requests, the API server can proxy to deployment runtime endpoints:

```text
Server.domainProxyOrNext
  -> state.ResolveDomainRuntimeEndpoint(request.Host)
       -> query domain_bindings + deployment_runtimes
       -> require runtime_status == "running"
  -> if found: httputil.NewSingleHostReverseProxy(target)
```

## 8. One-Line Mental Model

```text
`janus-api` serves HTTP requests, persists operations, and runs the worker supervisor that claims queued DB work and executes build/deploy jobs in-process.
```
