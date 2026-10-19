package application

import (
	"context"
	"fmt"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	accountingactor "janus/actors/accounting"
	authactor "janus/actors/authentication"
	authzactor "janus/actors/authorization"
	"janus/actors/builder"
	configactor "janus/actors/config"
	dockeractor "janus/actors/docker"
	httpserver "janus/actors/httpserver"
	persistenceactor "janus/actors/persistence"
	projectsactor "janus/actors/projects"
	routesactor "janus/actors/routes"
	telemetryactor "janus/actors/telemetry"
	wasmactor "janus/actors/wasm"
	wasmruntime "janus/actors/wasmruntime"
)

// Actor starts and stops the configured Janus services.
type Actor struct {
	system         goakt.ActorSystem
	configActor    *goakt.PID
	routes         *goakt.PID
	telemetry      *goakt.PID
	httpServer     *goakt.PID
	accounting     *goakt.PID
	authentication *goakt.PID
	authorization  *goakt.PID
	persistence    *goakt.PID
	projects       *goakt.PID
	wasmBuilds     *goakt.PID
	wasmRuntime    *goakt.PID
	docker         *goakt.PID
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
	case *httpserver.ServeFailed:
		_, span := telemetryactor.StartSpan(ctx.Context(), "http.server.failure", trace.WithSpanKind(trace.SpanKindInternal))
		telemetryactor.RecordError(span, msg.Err)
		span.End()
		a.account(ctx.Context(), "error", "http.server.failed", map[string]string{"error": msg.Err.Error()})
		if a.processErrors != nil {
			select {
			case a.processErrors <- msg.Err:
			default:
			}
		}
	default:
		ctx.Unhandled()
	}
}

func (a *Actor) start(ctx *goakt.ReceiveContext) (retErr error) {
	a.system = ctx.ActorSystem()
	var err error
	a.configActor, err = a.system.Spawn(ctx.Context(), "config", configactor.New())
	if err != nil {
		return fmt.Errorf("spawn config actor: %w", err)
	}
	environmentResponse, err := goakt.Ask(ctx.Context(), a.configActor, &configactor.LoadEnvironment{}, StartupTimeout)
	if err != nil {
		return fmt.Errorf("load environment: %w", err)
	}
	environmentLoaded, ok := environmentResponse.(*configactor.EnvironmentLoaded)
	if !ok {
		return fmt.Errorf("load environment: unexpected response %T", environmentResponse)
	}
	if environmentLoaded.Err != nil {
		return fmt.Errorf("load environment: %w", environmentLoaded.Err)
	}

	loadedResponse, err := goakt.Ask(ctx.Context(), a.configActor, &configactor.Load{}, StartupTimeout)
	if err != nil {
		return fmt.Errorf("request config: %w", err)
	}
	loaded, ok := loadedResponse.(*configactor.Loaded)
	if !ok {
		return fmt.Errorf("load config: unexpected response %T", loadedResponse)
	}
	if loaded.Err != nil {
		return fmt.Errorf("load config: %w", loaded.Err)
	}
	config := loaded.Config

	if a.logger == nil {
		a.logger = zap.NewNop()
	}
	a.accounting, err = a.system.Spawn(ctx.Context(), "accounting", accountingactor.New(a.logger))
	if err != nil {
		return fmt.Errorf("spawn accounting actor: %w", err)
	}
	a.telemetry, err = a.system.Spawn(ctx.Context(), "telemetry", telemetryactor.New())
	if err != nil {
		return fmt.Errorf("spawn telemetry actor: %w", err)
	}
	telemetryResponse, err := goakt.Ask(ctx.Context(), a.telemetry, &telemetryactor.Setup{
		PyroscopeServerAddress: config.PyroscopeServerAddress,
		Accounting:             a.accounting,
	}, StartupTimeout)
	if err != nil {
		return fmt.Errorf("send telemetry setup: %w", err)
	}
	telemetryReady, ok := telemetryResponse.(*telemetryactor.Ready)
	if !ok {
		return fmt.Errorf("initialize telemetry: unexpected response %T", telemetryResponse)
	}
	if telemetryReady.Err != nil {
		return fmt.Errorf("initialize telemetry: %w", telemetryReady.Err)
	}
	a.account(ctx.Context(), "info", "pyroscope.profiling.started", map[string]string{"application": "janus-go"})

	startupCtx, startupSpan := telemetryactor.StartSpan(ctx.Context(), "application.start", trace.WithSpanKind(trace.SpanKindInternal))
	startupSpan.SetAttributes(attribute.String("application.name", "janus-go"))
	defer func() {
		telemetryactor.RecordError(startupSpan, retErr)
		startupSpan.End()
	}()

	a.persistence, err = a.system.Spawn(startupCtx, "persistence", persistenceactor.New(config.DatabasePath, config.JWTSecret))
	if err != nil {
		return fmt.Errorf("spawn persistence actor: %w", err)
	}
	a.authentication, err = a.system.Spawn(startupCtx, "authentication", authactor.New([]byte(config.JWTSecret), config.JWTIssuer, config.JWTExpiry))
	if err != nil {
		return fmt.Errorf("spawn authentication actor: %w", err)
	}
	a.authorization, err = a.system.Spawn(startupCtx, "authorization", authzactor.New())
	if err != nil {
		return fmt.Errorf("spawn authorization actor: %w", err)
	}
	a.projects, err = a.system.Spawn(startupCtx, "projects", projectsactor.New(a.persistence, a.accounting))
	if err != nil {
		return fmt.Errorf("spawn projects actor: %w", err)
	}
	a.docker, err = a.system.Spawn(startupCtx, "docker-controller", dockeractor.New())
	if err != nil {
		return fmt.Errorf("spawn Docker controller actor: %w", err)
	}
	dockerResponse, err := goakt.Ask(startupCtx, a.docker, &dockeractor.EnsureRunning{}, 75*time.Second)
	if err != nil {
		return fmt.Errorf("check Docker daemon: %w", err)
	}
	dockerReady, ok := dockerResponse.(*dockeractor.Healthy)
	if !ok {
		return fmt.Errorf("check Docker daemon: unexpected response %T", dockerResponse)
	}
	if dockerReady.Err != nil {
		return fmt.Errorf("ensure Docker Engine is running: %w", dockerReady.Err)
	} else {
		a.logger.Info("Docker daemon is ready")
	}
	imagesResponse, err := goakt.Ask(startupCtx, a.docker, &dockeractor.PrepareWASMBuilders{
		RustImage: config.WASMBuilderImage, GoImage: config.GoWASMBuilderImage, JSImage: config.JSWASMBuilderImage,
	}, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("prepare WASM builder images: %w", err)
	}
	imagesReady, ok := imagesResponse.(*dockeractor.WASMBuildersReady)
	if !ok {
		return fmt.Errorf("prepare WASM builder images: unexpected response %T", imagesResponse)
	}
	if imagesReady.Err != nil {
		return fmt.Errorf("prepare WASM builder images: %w", imagesReady.Err)
	}
	for _, image := range imagesReady.BuiltImages {
		a.logger.Info("built missing WASM builder image", zap.String("image", image))
	}
	builds := builder.NewDocker(builder.Images{Rust: config.WASMBuilderImage, Go: config.GoWASMBuilderImage, JavaScript: config.JSWASMBuilderImage})
	a.wasmBuilds, err = a.system.Spawn(startupCtx, "wasm-builds", wasmactor.New(a.persistence, a.accounting, builds))
	if err != nil {
		return fmt.Errorf("spawn WASM build actor: %w", err)
	}
	a.wasmRuntime, err = a.system.Spawn(startupCtx, "wasm-runtime", wasmruntime.New(a.persistence))
	if err != nil {
		return fmt.Errorf("spawn WASM runtime actor: %w", err)
	}

	if err = a.resumeInterruptedJobs(startupCtx); err != nil {
		return err
	}
	a.routes, err = a.system.Spawn(startupCtx, "routes", routesactor.New(routesactor.Dependencies{
		System: a.system, Authentication: a.authentication, Authorization: a.authorization,
		Accounting: a.accounting, Persistence: a.persistence, Projects: a.projects, WASMBuilds: a.wasmBuilds, WASMRuntime: a.wasmRuntime, JWTExpiry: config.JWTExpiry,
		CookieSecure: config.CookieSecure, GoogleEnabled: config.GoogleEnabled,
		GoogleClientID: config.GoogleClientID, GoogleSecret: config.GoogleSecret,
		GoogleCallback: config.GoogleCallback, JWTSecret: config.JWTSecret,
		GitHubEnabled: config.GitHubEnabled, GitHubClientID: config.GitHubClientID,
		GitHubSecret: config.GitHubSecret, GitHubCallback: config.GitHubCallback,
	}))
	if err != nil {
		return fmt.Errorf("spawn routes actor: %w", err)
	}
	routesResponse, err := goakt.Ask(startupCtx, a.routes, &routesactor.Build{}, StartupTimeout)
	if err != nil {
		return fmt.Errorf("request routes: %w", err)
	}
	routesBuilt, ok := routesResponse.(*routesactor.Built)
	if !ok {
		return fmt.Errorf("build routes: unexpected response %T", routesResponse)
	}
	app := routesBuilt.App

	serverCtx, serverSpan := telemetryactor.StartSpan(startupCtx, "http.server.start", trace.WithSpanKind(trace.SpanKindInternal))
	serverSpan.SetAttributes(attribute.String("server.address", config.HTTPAddress))
	a.httpServer, err = a.system.Spawn(serverCtx, "http-server", httpserver.New(config.HTTPAddress, app, ctx.Self()))
	telemetryactor.RecordError(serverSpan, err)
	serverSpan.End()
	if err != nil {
		return fmt.Errorf("spawn HTTP server actor: %w", err)
	}
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
		response, err := goakt.Ask(shutdownCtx, a.httpServer, &httpserver.Stop{Context: shutdownCtx}, ShutdownTimeout)
		if err != nil {
			firstErr = err
		} else if result, ok := response.(*httpserver.Stopped); !ok {
			firstErr = fmt.Errorf("stop HTTP server: unexpected response %T", response)
		} else if result.Err != nil {
			firstErr = result.Err
		}
	}
	if a.projects != nil {
		if err := projectsactor.WaitDrained(shutdownCtx, a.projects); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("drain project imports: %w", err)
		}
	}
	if a.wasmBuilds != nil {
		if err := wasmactor.WaitDrained(shutdownCtx, a.wasmBuilds); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("drain WASM builds: %w", err)
		}
	}
	if a.wasmRuntime != nil {
		response, err := goakt.Ask(shutdownCtx, a.wasmRuntime, &wasmruntime.Drain{}, ShutdownTimeout)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("drain WASM runtime: %w", err)
		} else if result, ok := response.(*wasmruntime.Drained); !ok && err == nil && firstErr == nil {
			firstErr = fmt.Errorf("drain WASM runtime: unexpected response %T", response)
		} else if ok && result.Err != nil && firstErr == nil {
			firstErr = result.Err
		}
	}
	if a.persistence != nil {
		response, err := goakt.Ask(shutdownCtx, a.persistence, &persistenceactor.CloseDatabase{}, ShutdownTimeout)
		if err != nil && firstErr == nil {
			firstErr = err
		} else if result, ok := response.(*persistenceactor.DatabaseClosed); !ok && err == nil && firstErr == nil {
			firstErr = fmt.Errorf("close persistence database: unexpected response %T", response)
		} else if ok && result.Err != nil && firstErr == nil {
			firstErr = result.Err
		}
	}
	telemetryactor.RecordError(span, firstErr)
	if a.accounting != nil {
		response, err := goakt.Ask(shutdownCtx, a.accounting, &accountingactor.Flush{}, ShutdownTimeout)
		if err != nil && firstErr == nil {
			firstErr = err
		} else if result, ok := response.(*accountingactor.Flushed); ok && result.Err != nil && firstErr == nil {
			firstErr = result.Err
		}
	}
	if a.telemetry != nil {
		// End before shutting down the provider so its batch processor flushes this span.
		endSpan()
		response, err := goakt.Ask(shutdownCtx, a.telemetry, &telemetryactor.Shutdown{Context: shutdownCtx}, ShutdownTimeout)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else if result, ok := response.(*telemetryactor.Stopped); !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("shutdown telemetry: unexpected response %T", response)
			}
		} else if result.Err != nil && firstErr == nil {
			firstErr = result.Err
		}
	}
	return firstErr
}

func (a *Actor) account(ctx context.Context, level, name string, fields map[string]string) {
	if a.accounting == nil {
		return
	}
	_ = goakt.Tell(ctx, a.accounting, &accountingactor.Event{Level: level, Name: name, Fields: fields})
}

func (*Actor) PostStop(*goakt.Context) error { return nil }

// resumeInterruptedJobs restarts the imports and WASM builds that the previous
// shutdown interrupted. It runs before the routes accept requests.
func (a *Actor) resumeInterruptedJobs(ctx context.Context) error {
	importResponse, err := goakt.Ask(ctx, a.projects, &projectsactor.ResumeImports{}, StartupTimeout)
	if err != nil {
		return fmt.Errorf("resume project imports: %w", err)
	}
	imports, ok := importResponse.(*projectsactor.ImportsResumed)
	if !ok {
		return fmt.Errorf("resume project imports: unexpected response %T", importResponse)
	}
	if imports.Err != nil {
		return fmt.Errorf("resume project imports: %w", imports.Err)
	}
	buildResponse, err := goakt.Ask(ctx, a.wasmBuilds, &wasmactor.ResumeBuilds{}, StartupTimeout)
	if err != nil {
		return fmt.Errorf("resume WASM builds: %w", err)
	}
	builds, ok := buildResponse.(*wasmactor.BuildsResumed)
	if !ok {
		return fmt.Errorf("resume WASM builds: unexpected response %T", buildResponse)
	}
	if builds.Err != nil {
		return fmt.Errorf("resume WASM builds: %w", builds.Err)
	}
	a.logger.Info("resumed interrupted jobs", zap.Int("imports", imports.Count), zap.Int("wasm_builds", builds.Count))
	return nil
}
