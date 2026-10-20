package application

import (
	"context"
	"fmt"

	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	authactor "janus/actors/authentication"
	authzactor "janus/actors/authorization"
	configactor "janus/actors/config"
	githubactor "janus/actors/github"
	"janus/actors/gitsource"
	telemetryactor "janus/actors/telemetry"
	wasmruntime "janus/actors/wasmruntime"
	"janus/internal/adapters/auditlog"
	"janus/internal/adapters/dockerbuild"
	"janus/internal/adapters/httpapi"
	"janus/internal/adapters/sqlstore"
	"janus/internal/app"
)

// Actor starts and stops the configured Janus services.
type Actor struct {
	system         goakt.ActorSystem
	telemetry      *telemetryactor.Telemetry
	httpServer     *httpapi.Server
	auditLog       *auditlog.Log
	audit          app.Audit
	authentication *authactor.Service
	authorization  *authzactor.Policy
	database       *sqlstore.Store
	store          app.Store
	github         *githubactor.Service
	imports        *app.Imports
	builds         *app.Builds
	accounts       *app.Accounts
	runs           *app.Runs
	logger         *zap.Logger
	processErrors  chan error
}

var _ goakt.Actor = (*Actor)(nil)

// New creates an application lifecycle actor using logger for lifecycle logs.
func New(logger *zap.Logger) *Actor { return &Actor{logger: logger} }

func (*Actor) PreStart(*goakt.Context) error { return nil }

func (a *Actor) Receive(ctx *goakt.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *Start:
		a.processErrors = msg.Errors
		err := a.start(ctx)
		if err != nil {
			a.account(ctx.Context(), "error", "application.start.failed", map[string]string{"error": err.Error()})
			shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
			if stopErr := a.stop(shutdownCtx); stopErr != nil {
				err = fmt.Errorf("%w; cleanup after startup failure: %v", err, stopErr)
			}
			cancel()
		} else {
			a.account(ctx.Context(), "info", "application.started", map[string]string{"name": "janus-go"})
		}
		ctx.Response(&Started{Err: err})
	case *Stop:
		a.account(ctx.Context(), "info", "application.shutdown.started", nil)
		ctx.Response(&Stopped{Err: a.stop(msg.Context)})
		ctx.Shutdown()
	default:
		ctx.Unhandled()
	}
}

func (a *Actor) start(ctx *goakt.ReceiveContext) (retErr error) {
	a.system = ctx.ActorSystem()
	if err := configactor.LoadEnvironment(); err != nil {
		return fmt.Errorf("load environment: %w", err)
	}
	config, err := configactor.Load(ctx.Context())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if a.logger == nil {
		a.logger = zap.NewNop()
	}
	a.auditLog = auditlog.New(a.logger)
	audit := a.auditLog
	a.audit = audit
	a.telemetry, err = telemetryactor.Start(ctx.Context(), config.PyroscopeServerAddress, func(level, message string) {
		audit.Record(context.Background(), app.AuditEvent{Level: level, Name: "pyroscope.sdk", Fields: map[string]string{"message": message}})
	})
	if err != nil {
		return fmt.Errorf("initialize telemetry: %w", err)
	}
	a.account(ctx.Context(), "info", "pyroscope.profiling.started", map[string]string{"application": "janus-go"})

	startupCtx, startupSpan := telemetryactor.StartSpan(ctx.Context(), "application.start", trace.WithSpanKind(trace.SpanKindInternal))
	startupSpan.SetAttributes(attribute.String("application.name", "janus-go"))
	defer func() {
		telemetryactor.RecordError(startupSpan, retErr)
		startupSpan.End()
	}()

	a.database, err = sqlstore.Open(config.DatabasePath, sqlstore.WithTokenSecret(config.JWTSecret))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	a.store = a.database
	a.github = githubactor.NewService(a.store, githubactor.NewClient())
	a.authentication = authactor.New([]byte(config.JWTSecret), config.JWTIssuer, config.JWTExpiry)
	a.authorization, err = authzactor.New(startupCtx)
	if err != nil {
		return err
	}
	a.imports = app.NewImports(a.store, a.github.API, gitsource.New(), a.audit)
	if err = dockerbuild.EnsureRunning(startupCtx); err != nil {
		return fmt.Errorf("ensure Docker Engine is running: %w", err)
	}
	a.logger.Info("Docker daemon is ready")
	images := dockerbuild.Images{Rust: config.WASMBuilderImage, Go: config.GoWASMBuilderImage, JavaScript: config.JSWASMBuilderImage}
	built, err := dockerbuild.PrepareImages(startupCtx, images)
	if err != nil {
		return fmt.Errorf("prepare WASM builder images: %w", err)
	}
	for _, image := range built {
		a.logger.Info("built missing WASM builder image", zap.String("image", image))
	}
	compiler := dockerbuild.NewDocker(images)
	a.builds = app.NewBuilds(a.store, compiler, a.audit)
	a.runs = app.NewRuns(a.store, wasmruntime.Wasmtime{})
	a.accounts = app.NewAccounts(a.store, a.imports, a.builds)

	if err = a.resumeInterruptedJobs(startupCtx); err != nil {
		return err
	}
	a.httpServer = httpapi.New(httpapi.Config{
		Addr: config.HTTPAddress, Authentication: a.authentication, Authorization: a.authorization,
		Audit: a.audit, Store: a.store, GitHub: a.github, Imports: a.imports, Builds: a.builds, Accounts: a.accounts, Runs: a.runs, JWTExpiry: config.JWTExpiry,
		CookieSecure: config.CookieSecure, GoogleEnabled: config.GoogleEnabled,
		GoogleClientID: config.GoogleClientID, GoogleSecret: config.GoogleSecret,
		GoogleCallback: config.GoogleCallback, JWTSecret: config.JWTSecret,
		GitHubEnabled: config.GitHubEnabled, GitHubClientID: config.GitHubClientID,
		GitHubSecret: config.GitHubSecret, GitHubCallback: config.GitHubCallback,
	})
	_, serverSpan := telemetryactor.StartSpan(startupCtx, "http.server.start", trace.WithSpanKind(trace.SpanKindInternal))
	serverSpan.SetAttributes(attribute.String("server.address", config.HTTPAddress))
	err = a.httpServer.Start()
	telemetryactor.RecordError(serverSpan, err)
	serverSpan.End()
	if err != nil {
		a.httpServer = nil
		return fmt.Errorf("start HTTP server: %w", err)
	}
	go a.watchHTTPServer(a.httpServer.Failed())
	return nil
}

func (a *Actor) stop(ctx context.Context) error {
	shutdownCtx, span := telemetryactor.StartSpan(ctx, "application.shutdown", trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(attribute.String("application.name", "janus-go"))
	spanEnded := false
	endSpan := func() {
		if !spanEnded {
			span.End()
			spanEnded = true
		}
	}
	defer endSpan()

	var firstErr error
	if a.httpServer != nil {
		firstErr = a.httpServer.Shutdown(shutdownCtx)
	}
	if a.imports != nil {
		if err := a.imports.Drain(shutdownCtx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("drain project imports: %w", err)
		}
	}
	if a.builds != nil {
		if err := a.builds.Drain(shutdownCtx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("drain WASM builds: %w", err)
		}
	}
	if a.runs != nil {
		if err := a.runs.Drain(shutdownCtx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("drain WASM runtime: %w", err)
		}
	}
	if a.database != nil {
		if err := a.database.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	telemetryactor.RecordError(span, firstErr)
	if a.auditLog != nil {
		if err := a.auditLog.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if a.telemetry != nil {
		// End before shutting down the provider so its batch processor flushes this span.
		endSpan()
		if err := a.telemetry.Stop(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// watchHTTPServer reports an unexpected HTTP serve failure to the process.
func (a *Actor) watchHTTPServer(failed <-chan error) {
	for err := range failed {
		ctx := context.Background()
		_, span := telemetryactor.StartSpan(ctx, "http.server.failure", trace.WithSpanKind(trace.SpanKindInternal))
		telemetryactor.RecordError(span, err)
		span.End()
		a.account(ctx, "error", "http.server.failed", map[string]string{"error": err.Error()})
		if a.processErrors != nil {
			select {
			case a.processErrors <- err:
			default:
			}
		}
	}
}

func (a *Actor) account(ctx context.Context, level, name string, fields map[string]string) {
	if a.audit == nil {
		return
	}
	a.audit.Record(ctx, app.AuditEvent{Level: level, Name: name, Fields: fields})
}

func (*Actor) PostStop(*goakt.Context) error { return nil }

// resumeInterruptedJobs restarts the imports and WASM builds that the previous
// shutdown interrupted. It runs before the routes accept requests.
func (a *Actor) resumeInterruptedJobs(ctx context.Context) error {
	imports, err := a.imports.Resume(ctx)
	if err != nil {
		return fmt.Errorf("resume project imports: %w", err)
	}
	builds, err := a.builds.Resume(ctx)
	if err != nil {
		return fmt.Errorf("resume WASM builds: %w", err)
	}
	a.logger.Info("resumed interrupted jobs", zap.Int("imports", imports), zap.Int("wasm_builds", builds))
	return nil
}
