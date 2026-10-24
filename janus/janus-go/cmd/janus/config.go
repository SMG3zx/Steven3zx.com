package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"janus/internal/platform/tracing"
)

// settings contains validated runtime settings loaded from the environment.
type settings struct {
	HTTPAddress            string
	WebUpstream            string
	IdentityDatabasePath   string
	ProjectsDatabasePath   string
	BuildsDatabasePath     string
	FleetDatabasePath      string
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
	NATSKeysPath           string
	NATSLeafAddress        string
	KeySecret              string
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
	_, span := tracing.StartSpan(ctx, "config.load", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()

	cfg, err := readSettings()
	if err != nil {
		return settings{}, err
	}
	span.SetAttributes(
		attribute.String("config.http.address", cfg.HTTPAddress),
		attribute.String("config.http.address.source", envSource("JANUS_HTTP_ADDRESS")),
		attribute.String("config.identity.database.path", cfg.IdentityDatabasePath),
		attribute.String("config.projects.database.path", cfg.ProjectsDatabasePath),
		attribute.String("config.builds.database.path", cfg.BuildsDatabasePath),
		attribute.String("config.fleet.database.path", cfg.FleetDatabasePath),
		attribute.Bool("config.google.enabled", cfg.GoogleEnabled),
		attribute.Bool("config.github.enabled", cfg.GitHubEnabled),
		attribute.Bool("config.headscale.enabled", cfg.HeadscaleURL != ""),
		attribute.Bool("config.pyroscope.enabled", cfg.PyroscopeServerAddress != ""),
		attribute.Bool("config.nats.embedded", cfg.NATSURL == ""),
	)
	if err := validateWebUpstream(cfg.WebUpstream); err != nil {
		return settings{}, err
	}
	return cfg, nil
}

// readSettings reads every setting from the environment, applying defaults. The
// web upstream is checked by loadSettings, after the configuration is recorded.
func readSettings() (settings, error) {
	cfg := settings{
		HTTPAddress:            envString("JANUS_HTTP_ADDRESS", ":9090"),
		WebUpstream:            envString("JANUS_WEB_UPSTREAM", ""),
		IdentityDatabasePath:   envString("JANUS_IDENTITY_DATABASE_PATH", "data/identity.db"),
		ProjectsDatabasePath:   envString("JANUS_PROJECTS_DATABASE_PATH", "data/projects.db"),
		BuildsDatabasePath:     envString("JANUS_BUILDS_DATABASE_PATH", "data/builds.db"),
		FleetDatabasePath:      envString("JANUS_FLEET_DATABASE_PATH", "data/fleet.db"),
		JWTIssuer:              envString("JANUS_JWT_ISSUER", "janus-go"),
		PyroscopeServerAddress: envString("PYROSCOPE_SERVER_ADDRESS", "http://localhost:4040"),
		WASMBuilderImage:       envString("JANUS_WASM_BUILDER_IMAGE", "janus-rust-wasm-builder:local"),
		GoWASMBuilderImage:     envString("JANUS_GO_WASM_BUILDER_IMAGE", "janus-go-wasm-builder:local"),
		JSWASMBuilderImage:     envString("JANUS_JS_WASM_BUILDER_IMAGE", "janus-js-wasm-builder:local"),
		TailnetDir:             envString("JANUS_TAILNET_DIR", "data/tailnet"),
		NATSURL:                envString("JANUS_NATS_URL", ""),
		NATSAddress:            envString("JANUS_NATS_ADDRESS", "127.0.0.1:4222"),
		NATSDir:                envString("JANUS_NATS_DIR", "data/nats"),
		NATSLeafAddress:        envString("JANUS_NATS_LEAF_ADDRESS", ""),
		HeadscaleURL:           envURL("HEADSCALE_URL", ""),
		HeadscaleAPIKey:        envString("HEADSCALE_API_KEY", ""),
	}
	cfg.NATSKeysPath = envString("JANUS_NATS_KEYS", filepath.Join(cfg.NATSDir, "keys.db"))
	cfg.HeadscaleControlURL = envURL("HEADSCALE_CONTROL_URL", cfg.HeadscaleURL)
	if (cfg.HeadscaleURL == "") != (cfg.HeadscaleAPIKey == "") {
		return settings{}, errors.New("HEADSCALE_URL and HEADSCALE_API_KEY must both be set, or neither")
	}
	if err := readSecurity(&cfg); err != nil {
		return settings{}, err
	}
	return cfg, nil
}

// readSecurity reads the JWT, cookie, OAuth and NATS key settings into cfg. The
// JWT secret and the NATS key secret are not trimmed.
func readSecurity(cfg *settings) error {
	cfg.JWTSecret = os.Getenv("JANUS_JWT_SECRET")
	if err := checkSecret("JANUS_JWT_SECRET", cfg.JWTSecret); err != nil {
		return err
	}
	expiry, err := jwtExpiry()
	if err != nil {
		return err
	}
	cfg.JWTExpiry = expiry
	if cfg.CookieSecure, err = cookieSecure(); err != nil {
		return err
	}
	google, err := readOAuthClient("GOOGLE")
	if err != nil {
		return err
	}
	cfg.GoogleClientID, cfg.GoogleSecret, cfg.GoogleCallback, cfg.GoogleEnabled = google.ID, google.Secret, google.Callback, google.Enabled
	github, err := readOAuthClient("GITHUB")
	if err != nil {
		return err
	}
	cfg.GitHubClientID, cfg.GitHubSecret, cfg.GitHubCallback, cfg.GitHubEnabled = github.ID, github.Secret, github.Callback, github.Enabled
	// The NATS trust keys are opened only when a secret is set. Without one the
	// embedded server accepts any client, as it did before the keys existed.
	cfg.KeySecret = os.Getenv("JANUS_KEY_SECRET")
	if cfg.KeySecret != "" {
		return checkSecret("JANUS_KEY_SECRET", cfg.KeySecret)
	}
	return nil
}

// oauthClient is one OAuth provider's client settings. A provider is enabled
// only when all three are set.
type oauthClient struct {
	ID       string
	Secret   string
	Callback string
	Enabled  bool
}

// readOAuthClient reads the client of the provider named by prefix, such as
// GOOGLE. A partial set of its variables is an error.
func readOAuthClient(prefix string) (oauthClient, error) {
	client := oauthClient{
		ID:       envString(prefix+"_CLIENT_ID", ""),
		Secret:   envString(prefix+"_CLIENT_SECRET", ""),
		Callback: envString(prefix+"_CALLBACK_URL", ""),
	}
	client.Enabled = client.ID != "" && client.Secret != "" && client.Callback != ""
	if !client.Enabled && (client.ID != "" || client.Secret != "" || client.Callback != "") {
		return oauthClient{}, fmt.Errorf("%s_CLIENT_ID, %s_CLIENT_SECRET, and %s_CALLBACK_URL must all be set", prefix, prefix, prefix)
	}
	return client, nil
}

// jwtExpiry reads JANUS_JWT_EXPIRY, which defaults to 30 minutes and must be
// between a minute and a day.
func jwtExpiry() (time.Duration, error) {
	value := envString("JANUS_JWT_EXPIRY", "")
	if value == "" {
		return 30 * time.Minute, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < time.Minute || parsed > 24*time.Hour {
		return 0, errors.New("JANUS_JWT_EXPIRY must be between 1m and 24h")
	}
	return parsed, nil
}

// cookieSecure reads JANUS_COOKIE_SECURE, which defaults to false.
func cookieSecure() (bool, error) {
	value := envString("JANUS_COOKIE_SECURE", "")
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, errors.New("JANUS_COOKIE_SECURE must be a boolean")
	}
	return parsed, nil
}

// checkSecret refuses a secret shorter than 32 bytes.
func checkSecret(name, value string) error {
	if len(value) < 32 {
		return fmt.Errorf("%s must contain at least 32 bytes", name)
	}
	return nil
}

// validateWebUpstream accepts an empty upstream, which means there is none, or
// an http or https address.
func validateWebUpstream(upstream string) error {
	if upstream == "" {
		return nil
	}
	parsed, err := url.Parse(upstream)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("JANUS_WEB_UPSTREAM must be an http or https address, for example http://web:3000")
	}
	return nil
}

// envString returns the variable without surrounding space, or fallback when it
// is empty.
func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// envURL is envString for an address, which also loses any trailing slash.
func envURL(name, fallback string) string {
	if value := strings.TrimRight(envString(name, ""), "/"); value != "" {
		return value
	}
	return fallback
}

// envSource says where a setting came from, for the configuration span.
func envSource(name string) string {
	if strings.TrimSpace(os.Getenv(name)) == "" {
		return "default"
	}
	return "environment"
}
