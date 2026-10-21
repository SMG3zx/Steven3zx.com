package main

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"janus/internal/app"
)

// settings contains validated runtime settings loaded from the environment.
type settings struct {
	HTTPAddress            string
	DatabasePath           string
	IdentityDatabasePath   string
	ProjectsDatabasePath   string
	BuildsDatabasePath     string
	JWTSecret              string
	JWTIssuer              string
	JWTExpiry              time.Duration
	CookieSecure           bool
	GoogleClientID         string
	GoogleSecret           string
	GoogleCallback         string
	GoogleEnabled          bool
	GitHubClientID         string
	GitHubSecret           string
	GitHubCallback         string
	GitHubEnabled          bool
	HeadscaleURL           string
	HeadscaleAPIKey        string
	HeadscaleControlURL    string
	TailnetDir             string
	NATSURL                string
	NATSAddress            string
	NATSDir                string
	WASMBuilderImage       string
	GoWASMBuilderImage     string
	JSWASMBuilderImage     string
	PyroscopeServerAddress string
}

// loadEnvironment reads the .env file into the process environment. A missing
// file is not an error.
func loadEnvironment() error {
	err := godotenv.Load(".env")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// loadSettings validates the process environment and returns the settings it describes.
func loadSettings(ctx context.Context) (settings, error) {
	_, span := app.StartSpan(ctx, "config.load", trace.WithSpanKind(trace.SpanKindInternal))
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
	identityDatabasePath := strings.TrimSpace(os.Getenv("JANUS_IDENTITY_DATABASE_PATH"))
	if identityDatabasePath == "" {
		identityDatabasePath = "data/identity.db"
	}
	projectsDatabasePath := strings.TrimSpace(os.Getenv("JANUS_PROJECTS_DATABASE_PATH"))
	if projectsDatabasePath == "" {
		projectsDatabasePath = "data/projects.db"
	}
	buildsDatabasePath := strings.TrimSpace(os.Getenv("JANUS_BUILDS_DATABASE_PATH"))
	if buildsDatabasePath == "" {
		buildsDatabasePath = "data/builds.db"
	}
	secret := os.Getenv("JANUS_JWT_SECRET")
	if len(secret) < 32 {
		return settings{}, errors.New("JANUS_JWT_SECRET must contain at least 32 bytes")
	}
	issuer := strings.TrimSpace(os.Getenv("JANUS_JWT_ISSUER"))
	if issuer == "" {
		issuer = "janus-go"
	}
	expiry := 30 * time.Minute
	if value := strings.TrimSpace(os.Getenv("JANUS_JWT_EXPIRY")); value != "" {
		parsed, parseErr := time.ParseDuration(value)
		if parseErr != nil || parsed < time.Minute || parsed > 24*time.Hour {
			return settings{}, errors.New("JANUS_JWT_EXPIRY must be between 1m and 24h")
		}
		expiry = parsed
	}
	cookieSecure := false
	if value := strings.TrimSpace(os.Getenv("JANUS_COOKIE_SECURE")); value != "" {
		parsed, parseErr := strconv.ParseBool(value)
		if parseErr != nil {
			return settings{}, errors.New("JANUS_COOKIE_SECURE must be a boolean")
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
		return settings{}, errors.New("GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, and GOOGLE_CALLBACK_URL must all be set")
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
	jsWASMBuilderImage := strings.TrimSpace(os.Getenv("JANUS_JS_WASM_BUILDER_IMAGE"))
	if jsWASMBuilderImage == "" {
		jsWASMBuilderImage = "janus-js-wasm-builder:local"
	}
	githubEnabled := githubID != "" && githubSecret != "" && githubCallback != ""
	if (githubID != "" || githubSecret != "" || githubCallback != "") && !githubEnabled {
		return settings{}, errors.New("GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET, and GITHUB_CALLBACK_URL must all be set")
	}
	headscaleURL := strings.TrimRight(strings.TrimSpace(os.Getenv("HEADSCALE_URL")), "/")
	headscaleKey := strings.TrimSpace(os.Getenv("HEADSCALE_API_KEY"))
	if (headscaleURL == "") != (headscaleKey == "") {
		return settings{}, errors.New("HEADSCALE_URL and HEADSCALE_API_KEY must both be set, or neither")
	}
	headscaleControlURL := strings.TrimRight(strings.TrimSpace(os.Getenv("HEADSCALE_CONTROL_URL")), "/")
	if headscaleControlURL == "" {
		headscaleControlURL = headscaleURL
	}
	tailnetDir := strings.TrimSpace(os.Getenv("JANUS_TAILNET_DIR"))
	if tailnetDir == "" {
		tailnetDir = "data/tailnet"
	}
	natsURL := strings.TrimSpace(os.Getenv("JANUS_NATS_URL"))
	natsAddress := strings.TrimSpace(os.Getenv("JANUS_NATS_ADDRESS"))
	if natsAddress == "" {
		natsAddress = "127.0.0.1:4222"
	}
	natsDir := strings.TrimSpace(os.Getenv("JANUS_NATS_DIR"))
	if natsDir == "" {
		natsDir = "data/nats"
	}
	span.SetAttributes(
		attribute.String("config.http.address", addr),
		attribute.String("config.http.address.source", source),
		attribute.String("config.database.path", databasePath),
		attribute.String("config.identity.database.path", identityDatabasePath),
		attribute.String("config.projects.database.path", projectsDatabasePath),
		attribute.String("config.builds.database.path", buildsDatabasePath),
		attribute.Bool("config.google.enabled", googleEnabled),
		attribute.Bool("config.github.enabled", githubEnabled),
		attribute.Bool("config.headscale.enabled", headscaleURL != ""),
		attribute.Bool("config.pyroscope.enabled", pyroscopeAddress != ""),
		attribute.Bool("config.nats.embedded", natsURL == ""),
	)
	return settings{
		HTTPAddress: addr, DatabasePath: databasePath, IdentityDatabasePath: identityDatabasePath, ProjectsDatabasePath: projectsDatabasePath, BuildsDatabasePath: buildsDatabasePath, JWTSecret: secret, JWTIssuer: issuer,
		JWTExpiry: expiry, CookieSecure: cookieSecure, GoogleClientID: googleID,
		GoogleSecret: googleSecret, GoogleCallback: googleCallback, GoogleEnabled: googleEnabled,
		GitHubClientID: githubID, GitHubSecret: githubSecret, GitHubCallback: githubCallback, GitHubEnabled: githubEnabled,
		HeadscaleURL: headscaleURL, HeadscaleAPIKey: headscaleKey, HeadscaleControlURL: headscaleControlURL, TailnetDir: tailnetDir,
		NATSURL: natsURL, NATSAddress: natsAddress, NATSDir: natsDir,
		WASMBuilderImage: wasmBuilderImage, GoWASMBuilderImage: goWASMBuilderImage, JSWASMBuilderImage: jsWASMBuilderImage,
		PyroscopeServerAddress: pyroscopeAddress,
	}, nil
}
