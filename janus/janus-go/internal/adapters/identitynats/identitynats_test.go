package identitynats

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"janus/internal/adapters/identitytest"
	"janus/internal/app"
	"janus/internal/contract"
	"janus/internal/platform/natstest"
	"janus/internal/services/identity"
)

const secret = "a-secret-of-at-least-thirty-two-bytes"

// startIdentity runs the identity service on its own NATS server and database
// and returns a client for it.
func startIdentity(t *testing.T, path string) *Client {
	t.Helper()
	hub := natstest.Start(t)
	service := identity.New(identity.Config{DatabasePath: path, TokenSecret: secret})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := service.Start(ctx, hub.Conn()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Stop(context.Background()) })
	return New(hub.Conn())
}

func TestClientThroughTheIdentityServicePassesTheIdentitiesContract(t *testing.T) {
	identitytest.Run(t, func(t *testing.T) app.Identities {
		return startIdentity(t, filepath.Join(t.TempDir(), "identity.db"))
	})
}

func TestUsersOutliveARestartOfTheService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	ctx := context.Background()
	hub := natstest.Start(t)
	first := identity.New(identity.Config{DatabasePath: path, TokenSecret: secret})
	if err := first.Start(ctx, hub.Conn()); err != nil {
		t.Fatal(err)
	}
	client := New(hub.Conn())
	if err := client.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := client.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-alice", Token: "secret-token"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.FindUser(ctx, "alice"); !errors.Is(err, contract.ErrUnavailable) {
		t.Fatalf("FindUser with the service stopped = %v, want unavailable", err)
	}

	second := identity.New(identity.Config{DatabasePath: path, TokenSecret: secret})
	if err := second.Start(ctx, hub.Conn()); err != nil {
		t.Fatal(err)
	}
	defer second.Stop(ctx)
	if user, err := client.FindUser(ctx, "alice"); err != nil || !user.Found {
		t.Fatalf("after a restart FindUser = %#v, %v", user, err)
	}
	if token, connected, err := client.FindGitHubToken(ctx, "alice"); err != nil || !connected || token != "secret-token" {
		t.Fatalf("after a restart the token = %q, %v, %v", token, connected, err)
	}
}

func TestAnotherSecretCannotReadTheToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	ctx := context.Background()
	hub := natstest.Start(t)
	service := identity.New(identity.Config{DatabasePath: path, TokenSecret: secret})
	if err := service.Start(ctx, hub.Conn()); err != nil {
		t.Fatal(err)
	}
	client := New(hub.Conn())
	if err := client.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := client.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-alice", Token: "secret-token"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	other := identity.New(identity.Config{DatabasePath: path, TokenSecret: strings.Repeat("x", 32)})
	if err := other.Start(ctx, hub.Conn()); err != nil {
		t.Fatal(err)
	}
	defer other.Stop(ctx)
	if _, _, err := client.FindGitHubToken(ctx, "alice"); err == nil || !strings.Contains(err.Error(), "reconnect GitHub") {
		t.Fatalf("token under another secret = %v, want a failure asking to reconnect", err)
	}
}
