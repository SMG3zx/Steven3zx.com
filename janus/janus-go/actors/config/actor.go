package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	actortrace "janus/actors/telemetry"
)

// Actor loads environment files and validates Janus runtime configuration.
type Actor struct{}

var _ goakt.Actor = (*Actor)(nil)

// New creates a configuration loading actor.
func New() *Actor { return &Actor{} }

func (*Actor) PreStart(*goakt.Context) error { return nil }

func (*Actor) Receive(ctx *goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *LoadEnvironment:
		err := godotenv.Load(".env")
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		ctx.Response(&EnvironmentLoaded{Err: err})
	case *Load:
		_, span := actortrace.StartSpan(ctx.Context(), "config.load", trace.WithSpanKind(trace.SpanKindInternal))
		defer span.End()

		addr := strings.TrimSpace(os.Getenv("JANUS_HTTP_ADDRESS"))
		source := "environment"
		if addr == "" {
			addr = ":9090"
			source = "default"
		}
		databasePath := strings.TrimSpace(os.Getenv("JANUS_DATABASE_PATH"))
		if databasePath == "" {
			databasePath = "data/janus.db"
		}
		secret := os.Getenv("JANUS_JWT_SECRET")
		if len(secret) < 32 {
			ctx.Response(&Loaded{Err: errors.New("JANUS_JWT_SECRET must contain at least 32 bytes")})
			return
		}
		issuer := strings.TrimSpace(os.Getenv("JANUS_JWT_ISSUER"))
		if issuer == "" {
			issuer = "janus-go"
		}
		expiry := 30 * time.Minute
		if value := strings.TrimSpace(os.Getenv("JANUS_JWT_EXPIRY")); value != "" {
			parsed, parseErr := time.ParseDuration(value)
			if parseErr != nil || parsed < time.Minute || parsed > 24*time.Hour {
				ctx.Response(&Loaded{Err: errors.New("JANUS_JWT_EXPIRY must be between 1m and 24h")})
				return
			}
			expiry = parsed
		}
		cookieSecure := false
		if value := strings.TrimSpace(os.Getenv("JANUS_COOKIE_SECURE")); value != "" {
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				ctx.Response(&Loaded{Err: errors.New("JANUS_COOKIE_SECURE must be a boolean")})
				return
			}
			cookieSecure = parsed
		}
		googleID := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
		googleSecret := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
		googleCallback := strings.TrimSpace(os.Getenv("GOOGLE_CALLBACK_URL"))
		pyroscopeAddress := strings.TrimSpace(os.Getenv("PYROSCOPE_SERVER_ADDRESS"))
		if pyroscopeAddress == "" {
			pyroscopeAddress = "http://localhost:4040"
		}
		googleEnabled := googleID != "" && googleSecret != "" && googleCallback != ""
		if (googleID != "" || googleSecret != "" || googleCallback != "") && !googleEnabled {
			ctx.Response(&Loaded{Err: errors.New("GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, and GOOGLE_CALLBACK_URL must all be set")})
			return
		}
		githubID := strings.TrimSpace(os.Getenv("GITHUB_CLIENT_ID"))
		githubSecret := strings.TrimSpace(os.Getenv("GITHUB_CLIENT_SECRET"))
		githubCallback := strings.TrimSpace(os.Getenv("GITHUB_CALLBACK_URL"))
		wasmBuilderImage := strings.TrimSpace(os.Getenv("JANUS_WASM_BUILDER_IMAGE"))
		if wasmBuilderImage == "" {
			wasmBuilderImage = "janus-rust-wasm-builder:local"
		}
		goWASMBuilderImage := strings.TrimSpace(os.Getenv("JANUS_GO_WASM_BUILDER_IMAGE"))
		if goWASMBuilderImage == "" {
			goWASMBuilderImage = "janus-go-wasm-builder:local"
		}
		githubEnabled := githubID != "" && githubSecret != "" && githubCallback != ""
		if (githubID != "" || githubSecret != "" || githubCallback != "") && !githubEnabled {
			ctx.Response(&Loaded{Err: errors.New("GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET, and GITHUB_CALLBACK_URL must all be set")})
			return
		}
		span.SetAttributes(
			attribute.String("config.http.address", addr),
			attribute.String("config.http.address.source", source),
			attribute.String("config.database.path", databasePath),
			attribute.Bool("config.google.enabled", googleEnabled),
			attribute.Bool("config.github.enabled", githubEnabled),
			attribute.Bool("config.pyroscope.enabled", pyroscopeAddress != ""),
		)
		ctx.Response(&Loaded{Config: Config{
			HTTPAddress: addr, DatabasePath: databasePath, JWTSecret: secret, JWTIssuer: issuer,
			JWTExpiry: expiry, CookieSecure: cookieSecure, GoogleClientID: googleID,
			GoogleSecret: googleSecret, GoogleCallback: googleCallback, GoogleEnabled: googleEnabled,
			GitHubClientID: githubID, GitHubSecret: githubSecret, GitHubCallback: githubCallback, GitHubEnabled: githubEnabled,
			WASMBuilderImage: wasmBuilderImage, GoWASMBuilderImage: goWASMBuilderImage,
			PyroscopeServerAddress: pyroscopeAddress,
		}})
	default:
		ctx.Unhandled()
	}
}

func (*Actor) PostStop(*goakt.Context) error { return nil }
