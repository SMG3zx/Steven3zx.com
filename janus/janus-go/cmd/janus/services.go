package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"janus/internal/adapters/agentgrpc"
	"janus/internal/adapters/auditnats"
	"janus/internal/adapters/dockerbuild"
	"janus/internal/adapters/headscale"
	"janus/internal/adapters/httpapi"
	"janus/internal/adapters/identitynats"
	"janus/internal/adapters/projectevents"
	"janus/internal/adapters/projectsnats"
	"janus/internal/adapters/sqlstore"
	"janus/internal/adapters/tailnode"
	"janus/internal/adapters/wasmtime"
	"janus/internal/app"
	"janus/internal/platform/natsx"
	auditservice "janus/internal/services/audit"
	identityservice "janus/internal/services/identity"
	projectsservice "janus/internal/services/projects"
)

// application is the composition root: it builds the adapters, hands them to
// the use cases, and starts and stops them in order through a lifecycle table.
type application struct {
	logger *zap.Logger
	// processErrors receives a failure of the HTTP server after startup.
	processErrors chan error
	lifecycle     *lifecycle

	settings  settings
	auditLog  *auditnats.Publisher
	audit     app.Audit
	telemetry *telemetry
	nats      *natsx.Hub
	// auditService writes the Audit Events published to the AUDIT stream.
	auditService *auditservice.Service
	database     *sqlstore.Store
	store        app.Store
	// identityService owns identity.db; identities reaches it over NATS.
	identityService *identityservice.Service
	identities      app.Identities
	// projectsService owns projects.db and runs Imports; catalog reaches it
	// over NATS, and projectEvents applies its deletions to the Store.
	projectsService *projectsservice.Service
	catalog         app.Catalog
	projectEvents   *projectevents.Events
	identity        *app.Identity
	projects        *app.Projects
	github          *app.GitHub
	agents          *app.Agents
	tailnet         app.Tailnet
	tailnode        *tailnode.Node
	agentLink       *agentgrpc.Link
	fleet           *app.Fleet
	// stopFleet ends the Fleet's reconcile loop; fleetDone closes when it has.
	stopFleet      context.CancelFunc
	fleetDone      chan struct{}
	authentication *app.Authentication
	authorization  *app.Policy
	imports        *app.Imports
	builds         *app.Builds
	deployments    *app.Deployments
	accounts       *app.Accounts
	runs           *app.Runs
	httpServer     *httpapi.Server

	// startCtx carries the application.start span once telemetry is running.
	startCtx  context.Context
	startSpan trace.Span
	// shutdownCtx carries the application.shutdown span; stopErr is the first
	// shutdown error so far, recorded on that span before telemetry stops.
	shutdownCtx  context.Context
	shutdownSpan trace.Span
	stopErr      error
}

func newApplication(logger *zap.Logger, processErrors chan error) *application {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &application{logger: logger, processErrors: processErrors}
}

// Start loads the environment and settings, then starts every component. A
// failure part-way stops what was started and returns the cause.
func (a *application) Start(ctx context.Context) error {
	if err := a.load(); err != nil {
		return err
	}
	a.startCtx = ctx
	a.lifecycle = newLifecycle(a.groups()...)
	a.lifecycle.startDone = a.finishStart
	if err := a.lifecycle.Start(ctx); err != nil {
		return err
	}
	a.record(ctx, "info", "application.started", map[string]string{"name": "janus-go"})
	return nil
}

func (a *application) load() error {
	if err := loadEnvironment(); err != nil {
		return fmt.Errorf("load environment: %w", err)
	}
	loaded, err := loadSettings(context.Background())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	a.settings = loaded
	return nil
}

// Stop stops every started component in reverse order and waits for running
// builds and imports.
func (a *application) Stop(ctx context.Context) error {
	a.record(ctx, "info", "application.shutdown.started", nil)
	if a.lifecycle == nil {
		return nil
	}
	a.shutdownCtx, a.shutdownSpan = app.StartSpan(ctx, "application.shutdown", trace.WithSpanKind(trace.SpanKindInternal))
	a.shutdownSpan.SetAttributes(attribute.String("application.name", "janus-go"))
	err := a.lifecycle.Stop(a.shutdownCtx)
	a.endShutdownSpan()
	return err
}

// groups is the table of components, in start order: foundation, identity,
// projects and builds, runtime, web. Stop runs it in reverse.
func (a *application) groups() []group {
	cfg := &a.settings
	return []group{
		{name: "foundation", components: []component{
			{name: "audit", start: a.startAudit, stop: a.tracked(a.stopAudit)},
			{name: "telemetry", start: a.startTelemetry, stop: a.tracked(a.stopTelemetry)},
			{name: "nats", start: a.startNATS, stop: a.tracked(a.stopNATS)},
			{name: "audit service", start: a.startAuditService, stop: a.tracked(a.stopAuditService)},
			{name: "store", start: a.openStore, stop: a.tracked(a.closeStore)},
		}},
		{name: "identity", components: []component{
			{name: "identity service", start: a.startIdentityService, stop: a.tracked(func(ctx context.Context) error {
				return wrap("stop the identity service", a.identityService.Stop(ctx))
			})},
			{name: "identity use cases", start: func(context.Context) error {
				a.identity = app.NewIdentity(a.store, a.identities)
				return nil
			}},
			{name: "authentication", start: func(context.Context) error {
				a.authentication = app.NewAuthentication([]byte(cfg.JWTSecret), cfg.JWTIssuer, cfg.JWTExpiry)
				return nil
			}},
			{name: "authorization", start: func(context.Context) error {
				var err error
				a.authorization, err = app.NewPolicy(a.startCtx)
				return err
			}},
			{name: "agents", start: a.startAgents},
		}},
		{name: "projects and builds", components: []component{
			{name: "projects service", start: a.startProjectsService, stop: a.tracked(func(ctx context.Context) error {
				return wrap("stop the projects service", a.projectsService.Stop(ctx))
			})},
			{name: "projects", start: func(context.Context) error {
				a.projects = app.NewProjects(a.catalog)
				a.github = app.NewGitHub(a.identities, a.catalog)
				a.imports = app.NewImports(a.catalog)
				return nil
			}},
			{name: "builds", start: a.startBuilds, stop: a.tracked(func(ctx context.Context) error {
				return wrap("drain WASM builds", a.builds.Drain(ctx))
			})},
			{name: "project events", start: a.startProjectEvents, stop: a.tracked(func(ctx context.Context) error {
				return wrap("stop applying project events", a.projectEvents.Stop(ctx))
			})},
			{name: "resume", start: a.resumeInterruptedJobs},
		}},
		{name: "runtime", components: []component{
			{name: "runs", start: func(context.Context) error {
				a.runs = app.NewRuns(a.store, wasmtime.Wasmtime{})
				return nil
			}, stop: a.tracked(func(ctx context.Context) error {
				return wrap("drain WASM runtime", a.runs.Drain(ctx))
			})},
			{name: "fleet", start: a.startFleet, stop: a.tracked(a.stopFleetLoop)},
			{name: "deployments", start: a.startDeployments, stop: a.tracked(func(ctx context.Context) error {
				return wrap("drain Deployments", a.deployments.Drain(ctx))
			})},
			// Accounts come last in runtime: deleting one stops its Deployments first.
			{name: "accounts", start: func(context.Context) error {
				a.accounts = app.NewAccounts(a.store, a.identities, a.catalog, a.builds).WithAgents(a.agents).WithDeployments(a.deployments)
				return nil
			}},
		}},
		{name: "web", components: []component{
			{name: "http", start: a.startHTTP, stop: a.tracked(func(ctx context.Context) error {
				return a.httpServer.Shutdown(ctx)
			})},
		}},
	}
}

// wrap prefixes a non-nil err with what was being done.
func wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}

// tracked remembers the first error a stop returns, for the shutdown span.
func (a *application) tracked(stop func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		err := stop(ctx)
		if err != nil && a.stopErr == nil {
			a.stopErr = err
		}
		return err
	}
}

// startAudit makes the Audit every component records through. It writes
// through the logger until the audit service starts.
func (a *application) startAudit(context.Context) error {
	a.auditLog = auditnats.New(a.logger, "janus")
	a.audit = a.auditLog
	return nil
}

// startAuditService starts the audit service on NATS and then sends Audit
// Events to it through the AUDIT stream.
func (a *application) startAuditService(ctx context.Context) error {
	service := auditservice.New(auditservice.Config{Logger: a.logger})
	if err := service.Start(ctx, a.nats.Conn()); err != nil {
		return fmt.Errorf("start the audit service: %w", err)
	}
	js, err := a.nats.JetStream()
	if err != nil {
		return errors.Join(fmt.Errorf("open JetStream: %w", err), service.Stop(ctx))
	}
	a.auditService = service
	a.auditLog.Connect(js)
	return nil
}

// stopAuditService sends Audit Events back to the logger, then stops the
// service. Events it has not written yet stay in AUDIT for the next start.
func (a *application) stopAuditService(ctx context.Context) error {
	a.auditLog.Disconnect()
	return wrap("stop the audit service", a.auditService.Stop(ctx))
}

func (a *application) stopAudit(context.Context) error { return a.auditLog.Flush() }

func (a *application) startTelemetry(ctx context.Context) error {
	var err error
	a.telemetry, err = startTelemetry(ctx, a.settings.PyroscopeServerAddress, func(level, message string) {
		a.audit.Record(context.Background(), app.AuditEvent{Level: level, Name: "pyroscope.sdk", Fields: map[string]string{"message": message}})
	})
	if err != nil {
		return fmt.Errorf("initialize telemetry: %w", err)
	}
	a.record(ctx, "info", "pyroscope.profiling.started", map[string]string{"application": "janus-go"})
	a.startCtx, a.startSpan = app.StartSpan(ctx, "application.start", trace.WithSpanKind(trace.SpanKindInternal))
	a.startSpan.SetAttributes(attribute.String("application.name", "janus-go"))
	return nil
}

// stopTelemetry ends the shutdown span first, so the provider's batch processor
// flushes it.
func (a *application) stopTelemetry(ctx context.Context) error {
	a.endShutdownSpan()
	return a.telemetry.Stop(ctx)
}

// finishStart runs when the start phase ends, before a failed start is rolled
// back, so the failure is recorded while audit and telemetry are still running.
func (a *application) finishStart(err error) {
	if err != nil {
		a.record(a.startCtx, "error", "application.start.failed", map[string]string{"error": err.Error()})
	}
	a.endStartSpan(err)
}

func (a *application) endStartSpan(err error) {
	if a.startSpan == nil {
		return
	}
	app.RecordError(a.startSpan, err)
	a.startSpan.End()
	a.startSpan = nil
}

func (a *application) endShutdownSpan() {
	if a.shutdownSpan == nil {
		return
	}
	app.RecordError(a.shutdownSpan, a.stopErr)
	a.shutdownSpan.End()
	a.shutdownSpan = nil
}

// startNATS connects to JANUS_NATS_URL, or embeds a JetStream server listening
// on JANUS_NATS_ADDRESS with its store in JANUS_NATS_DIR.
func (a *application) startNATS(ctx context.Context) error {
	cfg := a.settings
	hub, err := natsx.Start(ctx, natsx.Config{URL: cfg.NATSURL, Address: cfg.NATSAddress, StoreDir: cfg.NATSDir, Name: "janus"})
	if err != nil {
		return fmt.Errorf("start NATS: %w", err)
	}
	a.nats = hub
	a.logger.Info("NATS is ready", zap.String("url", hub.URL()), zap.Bool("embedded", hub.Embedded()))
	return nil
}

func (a *application) stopNATS(ctx context.Context) error {
	return wrap("stop NATS", a.nats.Close(ctx))
}

func (a *application) openStore(context.Context) error {
	var err error
	a.database, err = sqlstore.Open(a.settings.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	a.store = a.database
	return nil
}

func (a *application) closeStore(context.Context) error { return a.database.Close() }

// startIdentityService starts the identity service on identity.db. It refuses
// to start while janus.db still holds Users that were never copied out,
// because they could not sign in; `janus migrate split` copies them.
func (a *application) startIdentityService(ctx context.Context) error {
	cfg := a.settings
	needed, err := identityservice.NeedsSplit(ctx, cfg.DatabasePath, cfg.IdentityDatabasePath)
	if err != nil {
		return fmt.Errorf("check whether %s needs splitting: %w", cfg.DatabasePath, err)
	}
	if needed {
		return fmt.Errorf("%s holds Users that %s does not have yet; run `janus migrate split` once, then start again", cfg.DatabasePath, cfg.IdentityDatabasePath)
	}
	service := identityservice.New(identityservice.Config{DatabasePath: cfg.IdentityDatabasePath, TokenSecret: cfg.JWTSecret})
	if err := service.Start(ctx, a.nats.Conn()); err != nil {
		return fmt.Errorf("start the identity service: %w", err)
	}
	a.identityService = service
	a.identities = identitynats.New(a.nats.Conn())
	return nil
}

func (a *application) startBuilds(context.Context) error {
	ctx := a.startCtx
	if err := dockerbuild.EnsureRunning(ctx); err != nil {
		return fmt.Errorf("ensure Docker Engine is running: %w", err)
	}
	a.logger.Info("Docker daemon is ready")
	images := dockerbuild.Images{Rust: a.settings.WASMBuilderImage, Go: a.settings.GoWASMBuilderImage, JavaScript: a.settings.JSWASMBuilderImage}
	built, err := dockerbuild.PrepareImages(ctx, images)
	if err != nil {
		return fmt.Errorf("prepare WASM builder images: %w", err)
	}
	for _, image := range built {
		a.logger.Info("built missing WASM builder image", zap.String("image", image))
	}
	docker := dockerbuild.NewDocker(images)
	a.builds = app.NewBuilds(a.store, a.catalog, docker, docker, a.audit)
	return nil
}

// startDeployments makes the Deployments use case. It hands Deployments to the
// Fleet, when Agent enrollment is on; without it Deploy is refused.
func (a *application) startDeployments(context.Context) error {
	var deployer app.AgentDeployer
	if a.fleet != nil {
		deployer = a.fleet
	}
	a.deployments = app.NewDeployments(a.store, deployer, a.audit)
	return nil
}

// resumeInterruptedJobs restarts the WASM builds that the previous shutdown
// interrupted. It runs before the routes accept requests. Imports are jobs of
// the projects service, which JetStream hands out again by itself.
func (a *application) resumeInterruptedJobs(context.Context) error {
	builds, err := a.builds.Resume(a.startCtx)
	if err != nil {
		return fmt.Errorf("resume WASM builds: %w", err)
	}
	a.logger.Info("resumed interrupted jobs", zap.Int("wasm_builds", builds))
	return nil
}

// startProjectsService starts the projects service on projects.db. It refuses
// to start while janus.db still holds Projects that were never copied out;
// `janus migrate split` copies them.
func (a *application) startProjectsService(ctx context.Context) error {
	cfg := a.settings
	needed, err := projectsservice.NeedsSplit(ctx, cfg.DatabasePath, cfg.ProjectsDatabasePath)
	if err != nil {
		return fmt.Errorf("check whether %s needs splitting: %w", cfg.DatabasePath, err)
	}
	if needed {
		return fmt.Errorf("%s holds Projects that %s does not have yet; run `janus migrate split` once, then start again", cfg.DatabasePath, cfg.ProjectsDatabasePath)
	}
	service := projectsservice.New(projectsservice.Config{DatabasePath: cfg.ProjectsDatabasePath})
	if err := service.Start(ctx, a.nats.Conn()); err != nil {
		return fmt.Errorf("start the projects service: %w", err)
	}
	a.projectsService = service
	a.catalog = projectsnats.New(a.nats.Conn())
	return nil
}

// startProjectEvents applies the projects service's deletions to the WASM
// Builds the Store keeps, from where it left off.
func (a *application) startProjectEvents(ctx context.Context) error {
	events := projectevents.New(a.builds)
	if err := events.Start(ctx, a.nats.Conn()); err != nil {
		return fmt.Errorf("apply project events: %w", err)
	}
	a.projectEvents = events
	return nil
}

func (a *application) startHTTP(context.Context) error {
	cfg := a.settings
	server := httpapi.New(httpapi.Config{
		Addr: cfg.HTTPAddress, Authentication: a.authentication, Authorization: a.authorization,
		Audit: a.audit, Identity: a.identity, Projects: a.projects, GitHub: a.github, Imports: a.imports, Builds: a.builds, Accounts: a.accounts, Agents: a.agents, Fleet: a.fleet, Runs: a.runs, Deployments: a.deployments,
		JWTExpiry: cfg.JWTExpiry, RecordRequest: recordHTTPRequest,
		CookieSecure: cfg.CookieSecure, GoogleEnabled: cfg.GoogleEnabled,
		GoogleClientID: cfg.GoogleClientID, GoogleSecret: cfg.GoogleSecret,
		GoogleCallback: cfg.GoogleCallback, JWTSecret: cfg.JWTSecret,
		GitHubEnabled: cfg.GitHubEnabled, GitHubClientID: cfg.GitHubClientID,
		GitHubSecret: cfg.GitHubSecret, GitHubCallback: cfg.GitHubCallback,
	})
	_, span := app.StartSpan(a.startCtx, "http.server.start", trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(attribute.String("server.address", cfg.HTTPAddress))
	err := server.Start()
	app.RecordError(span, err)
	span.End()
	if err != nil {
		return fmt.Errorf("start HTTP server: %w", err)
	}
	a.httpServer = server
	go a.watchHTTPServer(server.Failed())
	return nil
}

// watchHTTPServer reports an unexpected HTTP serve failure to the process.
func (a *application) watchHTTPServer(failed <-chan error) {
	for err := range failed {
		ctx := context.Background()
		_, span := app.StartSpan(ctx, "http.server.failure", trace.WithSpanKind(trace.SpanKindInternal))
		app.RecordError(span, err)
		span.End()
		a.record(ctx, "error", "http.server.failed", map[string]string{"error": err.Error()})
		if a.processErrors != nil {
			select {
			case a.processErrors <- err:
			default:
			}
		}
	}
}

func (a *application) record(ctx context.Context, level, name string, fields map[string]string) {
	if a.audit == nil {
		return
	}
	a.audit.Record(ctx, app.AuditEvent{Level: level, Name: name, Fields: fields})
}

// startAgents builds the Agents use case when a Headscale server is configured.
// A Headscale that cannot be prepared leaves Agent enrollment off and the
// control plane running, with the reason in the log.
func (a *application) startAgents(ctx context.Context) error {
	cfg := a.settings
	if cfg.HeadscaleURL == "" {
		a.logger.Info("Agent enrollment is off: HEADSCALE_URL is not set")
		return nil
	}
	client := headscale.New(headscale.Config{URL: cfg.HeadscaleURL, APIKey: cfg.HeadscaleAPIKey})
	if err := client.Prepare(ctx); err != nil {
		a.logger.Error("Agent enrollment is off: Headscale could not be prepared", zap.Error(err))
		return nil
	}
	a.tailnet = client
	a.agents = app.NewAgents(a.store, client, a.audit, cfg.HeadscaleControlURL, time.Now)
	return nil
}

// fleetInterval is how often the Fleet looks for Agents to connect to.
const fleetInterval = 15 * time.Second

// startFleet puts the control plane on the tailnet as its own node, tagged
// app.ControlTag, and keeps it connected to every enrolled Agent. It needs
// Agent enrollment; when the node cannot join, Agents stay enrolled but cannot
// be reached, and the reason is in the log.
func (a *application) startFleet(ctx context.Context) error {
	if a.agents == nil {
		return nil
	}
	cfg := a.settings
	authKey := ""
	if !tailnode.Joined(cfg.TailnetDir) {
		key, err := a.agents.ControlPlaneKey(ctx)
		if err != nil {
			a.logger.Error("Agents cannot be reached: no key for the control plane's tailnet node", zap.Error(err))
			return nil
		}
		authKey = key.Key
	}
	node, err := tailnode.Start(ctx, tailnode.Config{Dir: cfg.TailnetDir, Hostname: "janus-control", ControlURL: cfg.HeadscaleControlURL, AuthKey: authKey})
	if err != nil {
		a.logger.Error("Agents cannot be reached: the control plane did not join the tailnet", zap.Error(err))
		return nil
	}
	a.tailnode = node
	a.agentLink = agentgrpc.NewLink(node.Dial)
	a.fleet = app.NewFleet(a.store, a.tailnet, a.agentLink, a.audit)
	loopCtx, cancel := context.WithCancel(context.Background())
	a.stopFleet, a.fleetDone = cancel, make(chan struct{})
	go func() {
		defer close(a.fleetDone)
		a.fleet.Run(loopCtx, fleetInterval)
	}()
	return nil
}

// stopFleetLoop ends the reconcile loop, closes every Agent connection and
// takes the control plane off the tailnet.
func (a *application) stopFleetLoop(ctx context.Context) error {
	if a.fleet == nil {
		return nil
	}
	a.stopFleet()
	select {
	case <-a.fleetDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	return errors.Join(a.agentLink.Close(), a.tailnode.Close())
}
