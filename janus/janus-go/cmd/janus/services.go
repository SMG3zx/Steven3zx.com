package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/nats-io/nats.go"
	"janus/internal/domain/authn"
	"janus/internal/domain/authz"
	"slices"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"janus/internal/agent/link"
	contractaudit "janus/internal/contract/audit"
	contractfleet "janus/internal/contract/fleet"
	"janus/internal/platform/leafbridge"
	"janus/internal/platform/natsx"
	"janus/internal/platform/tailnode"
	"janus/internal/platform/tracing"
	auditservice "janus/internal/services/audit"
	buildsservice "janus/internal/services/builds"
	"janus/internal/services/builds/docker"
	fleetservice "janus/internal/services/fleet"
	"janus/internal/services/fleet/headscale"
	"janus/internal/services/gateway"
	"janus/internal/services/gateway/auditclient"
	"janus/internal/services/gateway/buildsclient"
	"janus/internal/services/gateway/fleetclient"
	"janus/internal/services/gateway/identityclient"
	"janus/internal/services/gateway/projectsclient"
	"janus/internal/services/gateway/runsclient"
	identityservice "janus/internal/services/identity"
	projectsservice "janus/internal/services/projects"
	runsservice "janus/internal/services/runs"
)

// auditRecorder is where the process records its own Audit Events.
type auditRecorder interface {
	Record(ctx context.Context, event contractaudit.Event)
}

// application is the composition root: it builds the adapters, hands them to
// the use cases, and starts and stops them in order through a lifecycle table.
type application struct {
	logger *zap.Logger
	// services are the services this process runs; all of them unless `janus
	// serve` named some.
	services []string
	// processErrors receives a failure of the HTTP server after startup.
	processErrors chan error
	lifecycle     *lifecycle

	settings  settings
	auditLog  *auditclient.Publisher
	audit     auditRecorder
	telemetry *telemetry
	nats      *natsx.Hub
	// trust is the NATS trust the keys file opened. It is zero when the control
	// plane runs without NATS trust keys.
	trust natsTrust
	// auditService writes the Audit Events published to the AUDIT stream.
	auditService *auditservice.Service
	// identity is the gateway's client of the identity service, which owns
	// identity.db and is reached over NATS.
	identity  *identityclient.Gateway
	tailnet   *headscale.Client
	tailnode  *tailnode.Node
	agentLink *link.Link
	// agentsConn is the fleet service's connection as a user of AGENTS, which
	// reaches the Agents' state, modules and Usage. It is nil without NATS trust.
	agentsConn   *nats.Conn
	leafBridge   *leafbridge.Bridge
	fleetService *fleetservice.Service
	// fleet is the gateway's client of the fleet service; nil when Agent
	// enrollment is off.
	fleet          *fleetclient.Client
	authentication *authn.Authentication
	authorization  *authz.Policy
	builds         *buildsclient.Gateway
	runs           *runsclient.Client
	httpServer     *gateway.Server

	// startCtx carries the application.start span once telemetry is running.
	startCtx  context.Context
	startSpan trace.Span
	// shutdownCtx carries the application.shutdown span; stopErr is the first
	// shutdown error so far, recorded on that span before telemetry stops.
	shutdownCtx  context.Context
	shutdownSpan trace.Span
	stopErr      error
}

// newApplication returns an application that runs every service.
func newApplication(logger *zap.Logger, processErrors chan error) *application {
	return newServing(logger, processErrors, serviceNames)
}

// newServing returns an application that runs services.
func newServing(logger *zap.Logger, processErrors chan error, services []string) *application {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &application{logger: logger, processErrors: processErrors, services: services}
}

// serves reports whether this process runs the named service.
func (a *application) serves(name string) bool { return slices.Contains(a.services, name) }

// embedsNATS reports whether this process may start its own NATS server: only
// when it runs every service, since the others would have no server to find.
func (a *application) embedsNATS() bool { return len(a.services) == len(serviceNames) }

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
	if loaded.NATSURL == "" && !a.embedsNATS() {
		return errors.New("JANUS_NATS_URL is required: only `janus serve all` embeds a NATS server for the services to share")
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
	a.shutdownCtx, a.shutdownSpan = tracing.StartSpan(ctx, "application.shutdown", trace.WithSpanKind(trace.SpanKindInternal))
	a.shutdownSpan.SetAttributes(attribute.String("application.name", "janus-go"))
	err := a.lifecycle.Stop(a.shutdownCtx)
	a.endShutdownSpan()
	return err
}

// groups is the table of components for the services this process runs, in
// start order: foundation, identity, projects and builds, runtime, web. Stop runs
// it in reverse. A group none of whose services run here is left out.
func (a *application) groups() []group {
	all := []group{
		{name: "foundation", components: a.foundationComponents()},
		{name: "identity", components: a.ifServing("identity", a.hostService("identity", a.newIdentityService))},
		{name: "projects and builds", components: slices.Concat(
			a.ifServing("projects", a.hostService("projects", a.newProjectsService)),
			a.ifServing("builds", a.hostService("builds", a.newBuildsService)),
			a.ifServing("runs", a.hostService("runs", a.newRunsService)),
		)},
		{name: "runtime", components: a.ifServing("fleet", component{name: "fleet", start: a.startFleet, stop: a.tracked(a.stopFleet)})},
		{name: "web", components: a.webComponents()},
	}
	var groups []group
	for _, g := range all {
		if len(g.components) > 0 {
			groups = append(groups, g)
		}
	}
	return groups
}

// ifServing returns the components when this process runs the named service, and
// none otherwise.
func (a *application) ifServing(name string, components ...component) []component {
	if !a.serves(name) {
		return nil
	}
	return components
}

// foundationComponents are the audit, telemetry and NATS components. The audit
// component is the service itself when this process runs it, and otherwise the
// connection to the AUDIT stream that another process's service writes.
func (a *application) foundationComponents() []component {
	components := []component{
		{name: "audit", start: a.startAudit, stop: a.tracked(a.stopAudit)},
		{name: "telemetry", start: a.startTelemetry, stop: a.tracked(a.stopTelemetry)},
		{name: "nats", start: a.startNATS, stop: a.tracked(a.stopNATS)},
	}
	if a.serves("audit") {
		return append(components, component{name: "audit service", start: a.startAuditService, stop: a.tracked(a.stopAuditService)})
	}
	return append(components, component{name: "audit stream", start: a.connectAudit, stop: a.tracked(a.disconnectAudit)})
}

// webComponents are the gateway's authentication, authorization, clients and HTTP
// server, in that order.
func (a *application) webComponents() []component {
	cfg := &a.settings
	return a.ifServing("gateway",
		component{name: "authentication", start: func(context.Context) error {
			a.authentication = authn.New([]byte(cfg.JWTSecret), cfg.JWTIssuer, cfg.JWTExpiry)
			return nil
		}},
		component{name: "authorization", start: func(context.Context) error {
			var err error
			a.authorization, err = authz.New(a.startCtx)
			return err
		}},
		component{name: "service clients", start: a.startClients},
		component{name: "http", start: a.startHTTP, stop: a.tracked(func(ctx context.Context) error {
			return a.httpServer.Shutdown(ctx)
		})},
	)
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
	a.auditLog = auditclient.New(a.logger, "janus")
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

// connectAudit sends Audit Events to the AUDIT stream, which the audit service
// in another process declares and writes. Until the stream exists an event is
// written through the logger.
func (a *application) connectAudit(context.Context) error {
	js, err := a.nats.JetStream()
	if err != nil {
		return fmt.Errorf("open JetStream: %w", err)
	}
	a.auditLog.Connect(js)
	return nil
}

func (a *application) disconnectAudit(context.Context) error {
	a.auditLog.Disconnect()
	return nil
}

func (a *application) stopAudit(context.Context) error { return a.auditLog.Flush() }

func (a *application) startTelemetry(ctx context.Context) error {
	var err error
	a.telemetry, err = startTelemetry(ctx, a.settings.PyroscopeServerAddress, func(level, message string) {
		a.audit.Record(context.Background(), contractaudit.Event{Level: level, Name: "pyroscope.sdk", Fields: map[string]string{"message": message}})
	})
	if err != nil {
		return fmt.Errorf("initialize telemetry: %w", err)
	}
	a.record(ctx, "info", "pyroscope.profiling.started", map[string]string{"application": "janus-go"})
	a.startCtx, a.startSpan = tracing.StartSpan(ctx, "application.start", trace.WithSpanKind(trace.SpanKindInternal))
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
	tracing.RecordError(a.startSpan, err)
	a.startSpan.End()
	a.startSpan = nil
}

func (a *application) endShutdownSpan() {
	if a.shutdownSpan == nil {
		return
	}
	tracing.RecordError(a.shutdownSpan, a.stopErr)
	a.shutdownSpan.End()
	a.shutdownSpan = nil
}

// startNATS connects to JANUS_NATS_URL, or embeds a JetStream server listening
// on JANUS_NATS_ADDRESS with its store in JANUS_NATS_DIR.
func (a *application) startNATS(ctx context.Context) error {
	cfg := a.settings
	config := natsx.Config{URL: cfg.NATSURL, Address: cfg.NATSAddress, StoreDir: cfg.NATSDir, Name: "janus"}
	if cfg.NATSURL == "" && cfg.KeySecret != "" {
		revoked, err := fleetservice.LeafRevocations(ctx, cfg.FleetDatabasePath)
		if err != nil {
			return fmt.Errorf("read the leaf revocations: %w", err)
		}
		trust, err := openNATSTrust(ctx, cfg.NATSKeysPath, cfg.KeySecret, revoked)
		if err != nil {
			return fmt.Errorf("open the NATS keys: %w", err)
		}
		config.Trust, config.ClientOptions = trust.trust, trust.clientOpts
		config.LeafAddress = cfg.NATSLeafAddress
		a.trust = trust
	}
	hub, err := natsx.Start(ctx, config)
	if err != nil {
		return fmt.Errorf("start NATS: %w", err)
	}
	a.nats = hub
	a.logger.Info("NATS is ready", zap.String("url", hub.URL()), zap.Bool("embedded", hub.Embedded()))
	return nil
}

func (a *application) stopNATS(ctx context.Context) error {
	if a.agentsConn != nil {
		a.agentsConn.Close()
		a.agentsConn = nil
	}
	return wrap("stop NATS", a.nats.Close(ctx))
}

// newIdentityService makes the identity service on identity.db.
func (a *application) newIdentityService(context.Context) (natsService, error) {
	cfg := a.settings
	return identityservice.New(identityservice.Config{DatabasePath: cfg.IdentityDatabasePath, TokenSecret: cfg.JWTSecret}), nil
}

// newBuildsService makes the builds service on builds.db. Before it is made, the
// Docker builder images are made ready, as the builds compile in them.
func (a *application) newBuildsService(context.Context) (natsService, error) {
	cfg := a.settings
	if err := docker.EnsureRunning(a.startCtx); err != nil {
		return nil, fmt.Errorf("ensure Docker Engine is running: %w", err)
	}
	a.logger.Info("Docker daemon is ready")
	images := docker.Images{Rust: cfg.WASMBuilderImage, Go: cfg.GoWASMBuilderImage, JavaScript: cfg.JSWASMBuilderImage}
	built, err := docker.PrepareImages(a.startCtx, images)
	if err != nil {
		return nil, fmt.Errorf("prepare WASM builder images: %w", err)
	}
	for _, image := range built {
		a.logger.Info("built missing WASM builder image", zap.String("image", image))
	}
	compiler := docker.NewDocker(images)
	return buildsservice.New(buildsservice.Config{DatabasePath: cfg.BuildsDatabasePath, Compile: compiler.Build, Check: compiler.Check}), nil
}

// startClients makes the gateway's clients of the services, which it calls over
// NATS: identity, the builds service with the artifacts store its Artifacts are
// read from, the runs service, and the fleet service when Agent enrollment is on.
// The services may run in this process or in others.
func (a *application) startClients(ctx context.Context) error {
	conn := a.nats.Conn()
	a.identity = identityclient.NewGateway(conn)
	builds, err := buildsclient.NewGateway(ctx, conn)
	if err != nil {
		return fmt.Errorf("open the artifacts store: %w", err)
	}
	a.builds = builds
	a.runs = runsclient.New(conn)
	// Without the fleet service in this process, Agent enrollment is on when
	// Headscale is configured; with it, when the service started.
	if a.serves("fleet") && a.fleetService != nil || !a.serves("fleet") && a.settings.HeadscaleURL != "" {
		a.fleet = fleetclient.New(conn)
	}
	return nil
}

// newRunsService makes the runs service. It owns no database, so it has nothing
// to split; it runs WASM Runs in this process with Wasmtime.
func (a *application) newRunsService(context.Context) (natsService, error) {
	return runsservice.New(runsservice.Config{}), nil
}

// newProjectsService makes the projects service on projects.db.
func (a *application) newProjectsService(context.Context) (natsService, error) {
	cfg := a.settings
	return projectsservice.New(projectsservice.Config{DatabasePath: cfg.ProjectsDatabasePath}), nil
}

// natsService is a service that runs on the NATS connection until it is stopped.
type natsService interface {
	Start(ctx context.Context, conn *nats.Conn) error
	Stop(ctx context.Context) error
}

// hostService is a component that makes a service, starts it on NATS and stops
// it again. The service is built at start, so a failed build or start leaves nothing
// to stop.
func (a *application) hostService(name string, build func(context.Context) (natsService, error)) component {
	var service natsService
	return component{
		name: name + " service",
		start: func(ctx context.Context) error {
			var err error
			if service, err = build(ctx); err != nil {
				return err
			}
			if err := service.Start(ctx, a.nats.Conn()); err != nil {
				return fmt.Errorf("start the %s service: %w", name, err)
			}
			return nil
		},
		stop: a.tracked(func(ctx context.Context) error {
			return wrap("stop the "+name+" service", service.Stop(ctx))
		}),
	}
}

func (a *application) startHTTP(context.Context) error {
	cfg := a.settings
	config := gateway.Config{
		Addr: cfg.HTTPAddress, Authentication: a.authentication, Authorization: a.authorization,
		Audit: a.audit, Identity: a.identity, Catalog: projectsclient.NewGateway(a.nats.Conn()), Builds: a.builds, Runs: a.runs,
		JWTExpiry: cfg.JWTExpiry, RecordRequest: recordHTTPRequest,
		CookieSecure: cfg.CookieSecure, GoogleEnabled: cfg.GoogleEnabled,
		GoogleClientID: cfg.GoogleClientID, GoogleSecret: cfg.GoogleSecret,
		GoogleCallback: cfg.GoogleCallback, JWTSecret: cfg.JWTSecret,
		GitHubEnabled: cfg.GitHubEnabled, GitHubClientID: cfg.GitHubClientID,
		GitHubSecret: cfg.GitHubSecret, GitHubCallback: cfg.GitHubCallback,
		WebUpstream: cfg.WebUpstream,
	}
	if a.fleet != nil {
		config.Fleet = a.fleet
	}
	server := gateway.New(config)
	_, span := tracing.StartSpan(a.startCtx, "http.server.start", trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(attribute.String("server.address", cfg.HTTPAddress))
	err := server.Start()
	tracing.RecordError(span, err)
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
		_, span := tracing.StartSpan(ctx, "http.server.failure", trace.WithSpanKind(trace.SpanKindInternal))
		tracing.RecordError(span, err)
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
	a.audit.Record(ctx, contractaudit.Event{Level: level, Name: name, Fields: fields})
}

// startFleet starts the fleet service: Agent enrollment, the Agents on the
// tailnet, their Deployments and their Usage. It needs Headscale; without it,
// enrollment is off and the reason is in the log. When the control plane's own
// node cannot join the tailnet, the service still runs, with an Agent link that
// reaches no Agent, so Agents can still be removed and their Accounts deleted.
func (a *application) startFleet(ctx context.Context) error {
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
	agentsConn := a.nats.Conn()
	if a.trust.fleetOpts != nil {
		var err error
		if agentsConn, err = nats.Connect(a.nats.URL(), a.trust.fleetOpts...); err != nil {
			return fmt.Errorf("connect the fleet service to the Agents' account: %w", err)
		}
		a.agentsConn = agentsConn
	}
	link, err := link.NewLink(agentsConn)
	if err != nil {
		return errors.Join(fmt.Errorf("open the Agent link: %w", err), a.stopAgentLink())
	}
	a.agentLink = link
	if node := a.joinTailnet(ctx, client); node != nil {
		a.tailnode = node
		if err := a.startLeafBridge(); err != nil {
			return errors.Join(err, a.stopAgentLink())
		}
	}
	service := fleetservice.New(fleetservice.Config{DatabasePath: cfg.FleetDatabasePath, Tailnet: client, ControlURL: cfg.HeadscaleControlURL,
		Link: link, Agents: a.trust.agents, Trust: a.trust.public, AgentsConn: a.agentsConn, OnRevoke: a.nats.UpdateAccount})
	if err := service.Start(ctx, a.nats.Conn()); err != nil {
		return errors.Join(fmt.Errorf("start the fleet service: %w", err), a.stopAgentLink())
	}
	a.fleetService = service
	return nil
}

// joinTailnet puts the control plane on the tailnet as its own node, tagged
// contractfleet.ControlTag, so it can reach Agents. It returns nil and logs the
// reason when the node cannot join.
func (a *application) joinTailnet(ctx context.Context, client *headscale.Client) *tailnode.Node {
	cfg := a.settings
	authKey := ""
	if !tailnode.Joined(cfg.TailnetDir) {
		key, err := client.CreatePreAuthKey(ctx, contractfleet.ControlTag, time.Now().Add(controlKeyTTL))
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
	return node
}

// controlKeyTTL is how long the control plane's pre-auth key is valid.
const controlKeyTTL = time.Hour

// stopFleet stops the fleet service, which waits for its Deployments, then
// closes every Agent connection and takes the control plane off the tailnet.
func (a *application) stopFleet(ctx context.Context) error {
	if a.fleetService == nil {
		return nil
	}
	return errors.Join(wrap("stop the fleet service", a.fleetService.Stop(ctx)), a.stopAgentLink())
}

// startLeafBridge carries the Agents' leaf connections from the control plane's
// tailnet node to the hub's leaf port. It runs only when the hub trusts its keys,
// since only then does the hub take leaf connections.
func (a *application) startLeafBridge() error {
	if a.trust.agents == nil || a.settings.NATSLeafAddress == "" {
		return nil
	}
	listener, err := a.tailnode.Listen(contractfleet.AgentLeafPort)
	if err != nil {
		return fmt.Errorf("listen for Agents' leaf connections: %w", err)
	}
	a.leafBridge = leafbridge.ServeHub(listener, a.settings.NATSLeafAddress)
	return nil
}

// stopAgentLink closes the leaf bridge, the Agent link and the control plane's
// node, when they were started.
func (a *application) stopAgentLink() error {
	var errs []error
	if a.leafBridge != nil {
		a.leafBridge.Close()
		a.leafBridge = nil
	}
	if a.agentLink != nil {
		errs = append(errs, a.agentLink.Close())
	}
	if a.tailnode != nil {
		errs = append(errs, a.tailnode.Close())
	}
	return errors.Join(errs...)
}
