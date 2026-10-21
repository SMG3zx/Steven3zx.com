package identitytest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"janus/internal/app"
)

// Run runs the Identities contract against adapters made by newIdentities.
// Each case gets a fresh, empty adapter.
func Run(t *testing.T, newIdentities func(t *testing.T) app.Identities) {
	t.Helper()
	for _, c := range []struct {
		name string
		test func(t *testing.T, identities app.Identities)
	}{
		{"LocalUsers", testLocalUsers},
		{"ProviderIdentitiesMatchBySubject", testProviderIdentities},
		{"GitHubConnection", testGitHubConnection},
		{"DeletingAUserRemovesItsSignInMethods", testDeleteUser},
		{"ConcurrentCallers", testConcurrent},
	} {
		t.Run(c.name, func(t *testing.T) { c.test(t, newIdentities(t)) })
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func testLocalUsers(t *testing.T, identities app.Identities) {
	ctx := context.Background()
	must(t, identities.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: " Alice@Example.com ", PasswordHash: "hash"}))

	err := identities.CreateLocalUser(ctx, app.CreateLocalUser{ID: "other", Email: "alice@example.com", PasswordHash: "hash"})
	if !app.IsConflict(err) {
		t.Fatalf("duplicate email = %v, want a *app.Conflict", err)
	}
	if err := identities.CreateLocalUser(ctx, app.CreateLocalUser{ID: "nopass", Email: "x@example.com"}); err == nil || app.IsConflict(err) {
		t.Fatalf("local user without a password hash = %v, want an error that is not a conflict", err)
	}

	found, err := identities.FindLocalUser(ctx, "ALICE@example.com")
	must(t, err)
	if !found.Found || found.ID != "alice" || found.Email != "Alice@Example.com" || found.PasswordHash != "hash" {
		t.Fatalf("FindLocalUser = %#v", found)
	}
	if missing, err := identities.FindLocalUser(ctx, "nobody@example.com"); err != nil || missing.Found {
		t.Fatalf("absent local user = %#v, %v, want found=false and no error", missing, err)
	}
	if user, err := identities.FindUser(ctx, "alice"); err != nil || !user.Found || user.ID != "alice" || user.Email != "Alice@Example.com" {
		t.Fatalf("FindUser = %#v, %v", user, err)
	}
	if user, err := identities.FindUser(ctx, "nobody"); err != nil || user.Found {
		t.Fatalf("absent user = %#v, %v, want found=false and no error", user, err)
	}
}

func testProviderIdentities(t *testing.T, identities app.Identities) {
	ctx := context.Background()
	must(t, identities.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "hash"}))

	first, err := identities.ResolveGoogleIdentity(ctx, app.ResolveGoogleIdentity{Subject: "g-1", Email: "g@example.com", DisplayName: "G"})
	must(t, err)
	second, err := identities.ResolveGoogleIdentity(ctx, app.ResolveGoogleIdentity{Subject: "g-1", Email: "changed@example.com", DisplayName: "Changed"})
	must(t, err)
	if first.ID == "" || second.ID != first.ID || second.Email != "g@example.com" {
		t.Fatalf("Google identity resolved to %#v then %#v, want the same Account by subject", first, second)
	}
	if user, err := identities.FindUser(ctx, first.ID); err != nil || !user.Found || user.DisplayName != "G" {
		t.Fatalf("the User a Google identity created = %#v, %v", user, err)
	}

	inUse, err := identities.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-9", Email: "ALICE@example.com"})
	must(t, err)
	if !inUse.EmailInUse || inUse.ID != "" {
		t.Fatalf("GitHub login with a taken email = %#v, want EmailInUse and no Account", inUse)
	}
	if _, err = identities.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-9"}); err == nil || !strings.Contains(err.Error(), "verified primary email") {
		t.Fatalf("GitHub login without an email = %v", err)
	}
	created, err := identities.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-1", Email: "new@example.com", DisplayName: " New "})
	must(t, err)
	again, err := identities.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-1", Email: "elsewhere@example.com"})
	must(t, err)
	if created.ID == "" || again.ID != created.ID || created.DisplayName != "New" {
		t.Fatalf("GitHub login created %#v then %#v, want the same Account by subject", created, again)
	}
}

func testGitHubConnection(t *testing.T, identities app.Identities) {
	ctx := context.Background()
	must(t, identities.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}))
	must(t, identities.CreateLocalUser(ctx, app.CreateLocalUser{ID: "bob", Email: "bob@example.com", PasswordHash: "h"}))

	if token, connected, err := identities.FindGitHubToken(ctx, "alice"); err != nil || connected || token != "" {
		t.Fatalf("before connecting = %q, %v, %v", token, connected, err)
	}
	must(t, identities.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-alice", Token: "secret"}))
	if token, connected, err := identities.FindGitHubToken(ctx, "alice"); err != nil || !connected || token != "secret" {
		t.Fatalf("after connecting = %q, %v, %v", token, connected, err)
	}
	must(t, identities.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-alice", Token: "renewed"}))
	if token, _, err := identities.FindGitHubToken(ctx, "alice"); err != nil || token != "renewed" {
		t.Fatalf("after reconnecting = %q, %v, want the new token", token, err)
	}
	err := identities.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "bob", Subject: "gh-alice", Token: "t"})
	if !app.IsConflict(err) || !strings.Contains(err.Error(), "another Janus account") {
		t.Fatalf("connecting alice's GitHub to bob = %v, want a conflict", err)
	}
	err = identities.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-other", Token: "t"})
	if !app.IsConflict(err) || !strings.Contains(err.Error(), "different GitHub account") {
		t.Fatalf("connecting a second GitHub to alice = %v, want a conflict", err)
	}
	if err := identities.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-alice"}); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("connecting without a token = %v", err)
	}
	if err := identities.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "nobody", Subject: "gh-nobody", Token: "t"}); err == nil {
		t.Fatal("connected GitHub to a User that does not exist")
	}

	// Connecting links the GitHub identity, so signing in with it finds alice.
	login, err := identities.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-alice", Email: "whatever@example.com"})
	if err != nil || login.ID != "alice" {
		t.Fatalf("GitHub login after connecting = %#v, %v, want alice", login, err)
	}

	must(t, identities.DisconnectGitHub(ctx, "alice"))
	if _, connected, err := identities.FindGitHubToken(ctx, "alice"); err != nil || connected {
		t.Fatalf("after disconnecting connected=%v err=%v", connected, err)
	}
	if login, err := identities.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-alice", Email: "whatever@example.com"}); err != nil || login.ID != "alice" {
		t.Fatalf("GitHub login after disconnecting = %#v, %v, want the identity kept", login, err)
	}
}

func testDeleteUser(t *testing.T, identities app.Identities) {
	ctx := context.Background()
	must(t, identities.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}))
	must(t, identities.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-alice", Token: "t"}))

	deleted, err := identities.DeleteUser(ctx, "alice")
	if err != nil || !deleted {
		t.Fatalf("DeleteUser = %v, %v", deleted, err)
	}
	if user, _ := identities.FindUser(ctx, "alice"); user.Found {
		t.Fatal("the User is still there")
	}
	if local, _ := identities.FindLocalUser(ctx, "alice@example.com"); local.Found {
		t.Fatal("the local credential is still there")
	}
	if _, connected, _ := identities.FindGitHubToken(ctx, "alice"); connected {
		t.Fatal("the GitHub Connection is still there")
	}
	again, err := identities.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-alice", Email: "alice@example.com"})
	if err != nil || again.ID == "alice" || again.ID == "" {
		t.Fatalf("GitHub login after deletion = %#v, %v, want a new Account", again, err)
	}
	if deleted, err := identities.DeleteUser(ctx, "alice"); err != nil || deleted {
		t.Fatalf("deleting again = %v, %v, want false and no error", deleted, err)
	}
	// The email is free again.
	must(t, identities.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice-2", Email: "Alice@example.com", PasswordHash: "h"}))
}

func testConcurrent(t *testing.T, identities app.Identities) {
	const callers = 16
	ctx := context.Background()
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			name := fmt.Sprintf("user-%d", i)
			email := name + "@example.com"
			if err := identities.CreateLocalUser(ctx, app.CreateLocalUser{ID: name, Email: email, PasswordHash: "h"}); err != nil {
				errs <- err
				return
			}
			if err := identities.ConnectGitHub(ctx, app.ConnectGitHub{UserID: name, Subject: "gh-" + name, Token: "token-" + name}); err != nil {
				errs <- err
				return
			}
			token, connected, err := identities.FindGitHubToken(ctx, name)
			if err != nil || !connected || token != "token-"+name {
				errs <- fmt.Errorf("%s: token %q, %v, %v", name, token, connected, err)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	// One email, many callers: exactly one wins.
	results := make(chan error, callers)
	for i := range callers {
		wg.Go(func() {
			results <- identities.CreateLocalUser(ctx, app.CreateLocalUser{ID: fmt.Sprintf("shared-%d", i), Email: "shared@example.com", PasswordHash: "h"})
		})
	}
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case !app.IsConflict(err):
			t.Errorf("a losing caller got %v, want a conflict", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d callers created the same email, want exactly 1", won)
	}
}
