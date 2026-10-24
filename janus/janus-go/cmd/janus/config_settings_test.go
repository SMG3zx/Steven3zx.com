package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// settingsEnvironment names every variable loadSettings reads. The tests clear
// them so the result depends on nothing but what a test sets.
var settingsEnvironment = []string{
	"JANUS_HTTP_ADDRESS", "JANUS_IDENTITY_DATABASE_PATH", "JANUS_PROJECTS_DATABASE_PATH",
	"JANUS_BUILDS_DATABASE_PATH", "JANUS_FLEET_DATABASE_PATH", "JANUS_JWT_SECRET",
	"JANUS_JWT_ISSUER", "JANUS_JWT_EXPIRY", "JANUS_COOKIE_SECURE",
	"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_CALLBACK_URL",
	"GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET", "GITHUB_CALLBACK_URL",
	"PYROSCOPE_SERVER_ADDRESS", "JANUS_WASM_BUILDER_IMAGE", "JANUS_GO_WASM_BUILDER_IMAGE",
	"JANUS_JS_WASM_BUILDER_IMAGE", "HEADSCALE_URL", "HEADSCALE_API_KEY", "HEADSCALE_CONTROL_URL",
	"JANUS_TAILNET_DIR", "JANUS_NATS_URL", "JANUS_NATS_ADDRESS", "JANUS_NATS_DIR",
	"JANUS_NATS_KEYS", "JANUS_NATS_LEAF_ADDRESS", "JANUS_KEY_SECRET", "JANUS_WEB_UPSTREAM",
}

// settingsEnvironmentFor clears every variable loadSettings reads, then sets the
// given ones. The JWT secret is valid unless the test sets it.
func settingsEnvironmentFor(t *testing.T, set map[string]string) {
	t.Helper()
	for _, name := range settingsEnvironment {
		t.Setenv(name, "")
	}
	t.Setenv("JANUS_JWT_SECRET", strings.Repeat("s", 32))
	for name, value := range set {
		t.Setenv(name, value)
	}
}

func TestLoadSettingsDefaults(t *testing.T) {
	settingsEnvironmentFor(t, nil)
	got, err := loadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := settings{
		HTTPAddress:            ":9090",
		IdentityDatabasePath:   "data/identity.db",
		ProjectsDatabasePath:   "data/projects.db",
		BuildsDatabasePath:     "data/builds.db",
		FleetDatabasePath:      "data/fleet.db",
		JWTSecret:              strings.Repeat("s", 32),
		JWTIssuer:              "janus-go",
		JWTExpiry:              30 * time.Minute,
		TailnetDir:             "data/tailnet",
		NATSAddress:            "127.0.0.1:4222",
		NATSDir:                "data/nats",
		NATSKeysPath:           filepath.Join("data/nats", "keys.db"),
		WASMBuilderImage:       "janus-rust-wasm-builder:local",
		GoWASMBuilderImage:     "janus-go-wasm-builder:local",
		JSWASMBuilderImage:     "janus-js-wasm-builder:local",
		PyroscopeServerAddress: "http://localhost:4040",
	}
	if got != want {
		t.Fatalf("defaults:\n got %#v\nwant %#v", got, want)
	}
}

func TestLoadSettingsTrimsEverythingButSecrets(t *testing.T) {
	settingsEnvironmentFor(t, map[string]string{
		"JANUS_HTTP_ADDRESS":           " :8080 ",
		"JANUS_WEB_UPSTREAM":           " http://web:3000 ",
		"JANUS_IDENTITY_DATABASE_PATH": " id.db ",
		"JANUS_PROJECTS_DATABASE_PATH": " pr.db ",
		"JANUS_BUILDS_DATABASE_PATH":   " bu.db ",
		"JANUS_FLEET_DATABASE_PATH":    " fl.db ",
		"JANUS_JWT_ISSUER":             " issuer ",
		"JANUS_JWT_EXPIRY":             " 2h ",
		"JANUS_COOKIE_SECURE":          " true ",
		"GOOGLE_CLIENT_ID":             " g-id ",
		"GOOGLE_CLIENT_SECRET":         " g-secret ",
		"GOOGLE_CALLBACK_URL":          " https://janus/google ",
		"GITHUB_CLIENT_ID":             " h-id ",
		"GITHUB_CLIENT_SECRET":         " h-secret ",
		"GITHUB_CALLBACK_URL":          " https://janus/github ",
		"PYROSCOPE_SERVER_ADDRESS":     " http://pyro:4040 ",
		"JANUS_WASM_BUILDER_IMAGE":     " rust:1 ",
		"JANUS_GO_WASM_BUILDER_IMAGE":  " go:1 ",
		"JANUS_JS_WASM_BUILDER_IMAGE":  " js:1 ",
		"HEADSCALE_URL":                " http://headscale:8080/ ",
		"HEADSCALE_API_KEY":            " hs-key ",
		"HEADSCALE_CONTROL_URL":        "",
		"JANUS_TAILNET_DIR":            " tail ",
		"JANUS_NATS_URL":               " nats://nats:4222 ",
		"JANUS_NATS_ADDRESS":           " 0.0.0.0:4222 ",
		"JANUS_NATS_DIR":               " nats-data ",
		"JANUS_NATS_KEYS":              " keys.db ",
		"JANUS_NATS_LEAF_ADDRESS":      " 0.0.0.0:7422 ",
		"JANUS_KEY_SECRET":             strings.Repeat("k", 32),
	})
	got, err := loadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := settings{
		HTTPAddress:            ":8080",
		WebUpstream:            "http://web:3000",
		IdentityDatabasePath:   "id.db",
		ProjectsDatabasePath:   "pr.db",
		BuildsDatabasePath:     "bu.db",
		FleetDatabasePath:      "fl.db",
		JWTSecret:              strings.Repeat("s", 32),
		JWTIssuer:              "issuer",
		JWTExpiry:              2 * time.Hour,
		CookieSecure:           true,
		GoogleClientID:         "g-id",
		GoogleSecret:           "g-secret",
		GoogleCallback:         "https://janus/google",
		GoogleEnabled:          true,
		GitHubClientID:         "h-id",
		GitHubSecret:           "h-secret",
		GitHubCallback:         "https://janus/github",
		GitHubEnabled:          true,
		HeadscaleURL:           "http://headscale:8080",
		HeadscaleAPIKey:        "hs-key",
		HeadscaleControlURL:    "http://headscale:8080",
		TailnetDir:             "tail",
		NATSURL:                "nats://nats:4222",
		NATSAddress:            "0.0.0.0:4222",
		NATSDir:                "nats-data",
		NATSKeysPath:           "keys.db",
		NATSLeafAddress:        "0.0.0.0:7422",
		KeySecret:              strings.Repeat("k", 32),
		WASMBuilderImage:       "rust:1",
		GoWASMBuilderImage:     "go:1",
		JSWASMBuilderImage:     "js:1",
		PyroscopeServerAddress: "http://pyro:4040",
	}
	if got != want {
		t.Fatalf("populated:\n got %#v\nwant %#v", got, want)
	}
}

func TestLoadSettingsHeadscaleControlURLOverridesTheServerURL(t *testing.T) {
	settingsEnvironmentFor(t, map[string]string{
		"HEADSCALE_URL":         "http://headscale:8080",
		"HEADSCALE_API_KEY":     "hs-key",
		"HEADSCALE_CONTROL_URL": " https://tailnet.example.com/ ",
	})
	got, err := loadSettings(context.Background())
	if err != nil || got.HeadscaleControlURL != "https://tailnet.example.com" {
		t.Fatalf("control URL = %q, %v, want the trimmed control URL", got.HeadscaleControlURL, err)
	}
}

func TestLoadSettingsRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		set  map[string]string
		// jwtSecret replaces the valid default when non-empty.
		jwtSecret string
		want      string
	}{
		{name: "short JWT secret", jwtSecret: strings.Repeat("s", 31), want: "JANUS_JWT_SECRET must contain at least 32 bytes"},
		{name: "JWT expiry below a minute", set: map[string]string{"JANUS_JWT_EXPIRY": "30s"}, want: "JANUS_JWT_EXPIRY must be between 1m and 24h"},
		{name: "JWT expiry above a day", set: map[string]string{"JANUS_JWT_EXPIRY": "25h"}, want: "JANUS_JWT_EXPIRY must be between 1m and 24h"},
		{name: "JWT expiry not a duration", set: map[string]string{"JANUS_JWT_EXPIRY": "soon"}, want: "JANUS_JWT_EXPIRY must be between 1m and 24h"},
		{name: "cookie flag not a boolean", set: map[string]string{"JANUS_COOKIE_SECURE": "maybe"}, want: "JANUS_COOKIE_SECURE must be a boolean"},
		{name: "partial Google", set: map[string]string{"GOOGLE_CLIENT_ID": "g"}, want: "GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, and GOOGLE_CALLBACK_URL must all be set"},
		{name: "partial GitHub", set: map[string]string{"GITHUB_CALLBACK_URL": "https://janus/github"}, want: "GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET, and GITHUB_CALLBACK_URL must all be set"},
		{name: "Headscale URL without key", set: map[string]string{"HEADSCALE_URL": "http://headscale:8080"}, want: "HEADSCALE_URL and HEADSCALE_API_KEY must both be set, or neither"},
		{name: "short key secret", set: map[string]string{"JANUS_KEY_SECRET": "short"}, want: "JANUS_KEY_SECRET must contain at least 32 bytes"},
		{name: "web upstream without a scheme", set: map[string]string{"JANUS_WEB_UPSTREAM": "web:3000"}, want: "JANUS_WEB_UPSTREAM must be an http or https address, for example http://web:3000"},
		{name: "web upstream with another scheme", set: map[string]string{"JANUS_WEB_UPSTREAM": "ftp://web:3000"}, want: "JANUS_WEB_UPSTREAM must be an http or https address, for example http://web:3000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settingsEnvironmentFor(t, tt.set)
			if tt.jwtSecret != "" {
				t.Setenv("JANUS_JWT_SECRET", tt.jwtSecret)
			}
			_, err := loadSettings(context.Background())
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadSettingsAcceptsTheBoundaries(t *testing.T) {
	for _, expiry := range []string{"1m", "24h"} {
		settingsEnvironmentFor(t, map[string]string{"JANUS_JWT_EXPIRY": expiry})
		if _, err := loadSettings(context.Background()); err != nil {
			t.Fatalf("expiry %s: %v", expiry, err)
		}
	}
	for _, flag := range []string{"true", "1", "false"} {
		settingsEnvironmentFor(t, map[string]string{"JANUS_COOKIE_SECURE": flag})
		if _, err := loadSettings(context.Background()); err != nil {
			t.Fatalf("cookie flag %s: %v", flag, err)
		}
	}
}
