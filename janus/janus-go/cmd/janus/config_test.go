package main

import (
	"context"
	"strings"
	"testing"
)

// headscaleEnvironment sets the variables loadSettings needs, and the Headscale
// ones as given. Unset variables are cleared so the test does not depend on the
// machine's environment.
func headscaleEnvironment(t *testing.T, url, key, control string) {
	t.Helper()
	t.Setenv("JANUS_JWT_SECRET", strings.Repeat("s", 32))
	for _, name := range []string{"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_CALLBACK_URL", "GITHUB_CLIENT_ID", "GITHUB_CLIENT_SECRET", "GITHUB_CALLBACK_URL"} {
		t.Setenv(name, "")
	}
	t.Setenv("HEADSCALE_URL", url)
	t.Setenv("HEADSCALE_API_KEY", key)
	t.Setenv("HEADSCALE_CONTROL_URL", control)
}

func TestHeadscaleSettingsComeTogetherOrNotAtAll(t *testing.T) {
	headscaleEnvironment(t, "", "", "")
	got, err := loadSettings(context.Background())
	if err != nil || got.HeadscaleURL != "" {
		t.Fatalf("no Headscale settings = %#v, %v, want Agent enrollment off with no error", got, err)
	}
	for _, partial := range [][2]string{{"http://127.0.0.1:8080", ""}, {"", "a-key"}} {
		headscaleEnvironment(t, partial[0], partial[1], "")
		if _, err = loadSettings(context.Background()); err == nil || !strings.Contains(err.Error(), "HEADSCALE_URL and HEADSCALE_API_KEY") {
			t.Fatalf("URL %q with key %q = %v, want an error naming both variables", partial[0], partial[1], err)
		}
	}
}

func TestAgentsJoinTheServerURLUnlessAControlURLIsGiven(t *testing.T) {
	headscaleEnvironment(t, " http://127.0.0.1:8080/ ", "a-key", "")
	got, err := loadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.HeadscaleURL != "http://127.0.0.1:8080" || got.HeadscaleAPIKey != "a-key" || got.HeadscaleControlURL != "http://127.0.0.1:8080" {
		t.Fatalf("settings = %#v, want the trimmed URL used as the control URL too", got)
	}

	headscaleEnvironment(t, "http://127.0.0.1:8080", "a-key", "https://tailnet.example.com/")
	got, err = loadSettings(context.Background())
	if err != nil || got.HeadscaleControlURL != "https://tailnet.example.com" {
		t.Fatalf("settings = %#v, %v, want the control URL Agents are told to join", got, err)
	}
}

func TestNATSIsEmbeddedOnLocalhostUnlessAURLIsGiven(t *testing.T) {
	headscaleEnvironment(t, "", "", "")
	for _, name := range []string{"JANUS_NATS_URL", "JANUS_NATS_ADDRESS", "JANUS_NATS_DIR"} {
		t.Setenv(name, "")
	}
	got, err := loadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.NATSURL != "" || got.NATSAddress != "127.0.0.1:4222" || got.NATSDir != "data/nats" {
		t.Fatalf("settings = %#v, want an embedded server on 127.0.0.1:4222 storing in data/nats", got)
	}

	t.Setenv("JANUS_NATS_URL", " nats://nats.example:4222 ")
	got, err = loadSettings(context.Background())
	if err != nil || got.NATSURL != "nats://nats.example:4222" {
		t.Fatalf("settings = %#v, %v, want the trimmed external URL", got, err)
	}
}
