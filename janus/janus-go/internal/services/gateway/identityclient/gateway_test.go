package identityclient

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"janus/internal/contract"
	contractid "janus/internal/contract/identity"
	"janus/internal/platform/natstest"
	"janus/internal/services/identity"
)

const secret = "a-secret-of-at-least-thirty-two-bytes"

func TestUsersOutliveARestartOfTheService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	ctx := context.Background()
	hub := natstest.Start(t)
	first := identity.New(identity.Config{DatabasePath: path, TokenSecret: secret})
	if err := first.Start(ctx, hub.Conn()); err != nil {
		t.Fatal(err)
	}
	client := NewGateway(hub.Conn())
	if err := client.CreateLocalUser(ctx, contractid.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := client.ConnectGitHub(ctx, contractid.ConnectGitHub{UserID: "alice", Subject: "gh-alice", Token: "secret-token"}); err != nil {
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
	if found, err := client.FindGitHubToken(ctx, "alice"); err != nil || !found.Connected || found.Token != "secret-token" {
		t.Fatalf("after a restart the token = %q, %v, %v", found.Token, found.Connected, err)
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
	client := NewGateway(hub.Conn())
	if err := client.CreateLocalUser(ctx, contractid.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := client.ConnectGitHub(ctx, contractid.ConnectGitHub{UserID: "alice", Subject: "gh-alice", Token: "secret-token"}); err != nil {
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
	if _, err := client.FindGitHubToken(ctx, "alice"); err == nil || !strings.Contains(err.Error(), "reconnect GitHub") {
		t.Fatalf("token under another secret = %v, want a failure asking to reconnect", err)
	}
}
