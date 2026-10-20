package main

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"janus/internal/adapters/auditlog"
	"janus/internal/adapters/dockerbuild"
	"janus/internal/adapters/githubapi"
	"janus/internal/adapters/gitsource"
	"janus/internal/adapters/httpapi"
	"janus/internal/adapters/sqlstore"
	"janus/internal/adapters/wasmtime"
	"janus/internal/app"
)

// application is the composition root: it builds the adapters, hands them to
// the use cases, and starts and stops them in order through a lifecycle table.
type application struct {
	logger *zap.Logger
	// processErrors receives a failure of the HTTP server after startup.
	processErrors chan error
	lifecycle     *lifecycle

	settings       settings
	auditLog       *auditlog.Log
	audit          app.Audit
	telemetry      *telemetry
	database       *sqlstore.Store
	store          app.Store
	identity       *app.Identity
	projects       *app.Projects
	github         *app.GitHub
	authentication *app.Authentication
	authorization  *app.Policy
	imports        *app.Imports
	builds         *app.Builds
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
			{name: "store", start: a.openStore, stop: a.tracked(a.closeStore)},
		}},
		{name: "identity", components: []component{
			{name: "identity use cases", start: func(context.Context) error {
				a.identity = app.NewIdentity(a.store)
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
		}},
		{name: "projects and builds", components: []component{
			{name: "projects", start: func(context.Context) error {
				a.projects = app.NewProjects(a.store)
				return nil
			}},
			{name: "imports", start: func(context.Context) error {
				a.github = app.NewGitHub(a.store, githubapi.NewClient())
				a.imports = app.NewImports(a.store, a.github.API, gitsource.New(), a.audit)
				return nil
			}, stop: a.tracked(func(ctx context.Context) error {
				return wrap("drain project imports", a.imports.Drain(ctx))
			})},
			{name: "builds", start: a.startBuilds, stop: a.tracked(func(ctx context.Context) error {
				return wrap("drain WASM builds", a.builds.Drain(ctx))
			})},
			{name: "accounts", start: func(context.Context) error {
				a.accounts = app.NewAccounts(a.store, a.imports, a.builds)
				return nil
			}},
			{name: "resume", start: a.resumeInterruptedJobs},
		}},
		{name: "runtime", components: []component{
			{name: "runs", start: func(context.Context) error {
				a.runs = app.NewRuns(a.store, wasmtime.Wasmtime{})
				return nil
			}, stop: a.tracked(func(ctx context.Context) error {
				return wrap("drain WASM runtime", a.runs.Drain(ctx))
			})},
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

func (a *application) startAudit(context.Context) error {
	a.auditLog = auditlog.New(a.logger)
	a.audit = a.auditLog
	return nil
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

func (a *application) openStore(context.Context) error {
	var err error
	a.database, err = sqlstore.Open(a.settings.DatabasePath, sqlstore.WithTokenSecret(a.settings.JWTSecret))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	a.store = a.database
	return nil
}

func (a *application) closeStore(context.Context) error { return a.database.Close() }

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
	a.builds = app.NewBuilds(a.store, dockerbuild.NewDocker(images), a.audit)
	return nil
}

// resumeInterruptedJobs restarts the imports and WASM builds that the previous
// shutdown interrupted. It runs before the routes accept requests.
func (a *application) resumeInterruptedJobs(context.Context) error {
	imports, err := a.imports.Resume(a.startCtx)
	if err != nil {
		return fmt.Errorf("resume project imports: %w", err)
	}
	builds, err := a.builds.Resume(a.startCtx)
	if err != nil {
		return fmt.Errorf("resume WASM builds: %w", err)
	}
	a.logger.Info("resumed interrupted jobs", zap.Int("imports", imports), zap.Int("wasm_builds", builds))
	return nil
}

func (a *application) startHTTP(context.Context) error {
	cfg := a.settings
	server := httpapi.New(httpapi.Config{
		Addr: cfg.HTTPAddress, Authentication: a.authentication, Authorization: a.authorization,
		Audit: a.audit, Identity: a.identity, Projects: a.projects, GitHub: a.github, Imports: a.imports, Builds: a.builds, Accounts: a.accounts, Runs: a.runs,
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
