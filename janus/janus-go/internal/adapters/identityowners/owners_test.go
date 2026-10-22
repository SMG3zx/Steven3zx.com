package identityowners_test

import (
	"context"
	"errors"
	"testing"

	"janus/internal/adapters/identityowners"
	"janus/internal/adapters/identitytest"
	"janus/internal/adapters/storetest"
	"janus/internal/app"
	"janus/internal/contract"
	contractid "janus/internal/contract/identity"
)

// owns reports whether store accepts a Project for owner, which it does only for
// an Owner it was told about.
func owns(t *testing.T, store app.Store, owner string) bool {
	t.Helper()
	return store.AddProject(context.Background(), app.ProjectRef{ID: owner + "-probe", OwnerID: owner}) == nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// directory returns a Registry over identities that holds a local User for each id.
func directory(t *testing.T, store app.Store, ids ...string) *identityowners.Registry {
	t.Helper()
	memory := identitytest.NewMemory()
	for _, id := range ids {
		must(t, memory.CreateLocalUser(context.Background(), app.CreateLocalUser{ID: id, Email: id + "@example.com", PasswordHash: "h"}))
	}
	return identityowners.New(store, identitytest.NewDirectory(memory))
}

func TestEverySignInMakesTheUserAnOwner(t *testing.T) {
	ctx := context.Background()
	store := storetest.NewMemory()
	identity := identityowners.New(store, identitytest.NewDirectory(identitytest.NewMemory()))

	must(t, identity.CreateLocalUser(ctx, contractid.CreateLocalUser{ID: "local", Email: "local@example.com", PasswordHash: "h"}))
	if !owns(t, store, "local") {
		t.Error("a registered User is not an Owner")
	}
	google, err := identity.ResolveGoogleIdentity(ctx, contractid.ResolveIdentity{Subject: "g", Email: "g@example.com"})
	must(t, err)
	if !owns(t, store, google.ID) {
		t.Error("a Google User is not an Owner")
	}
	github, err := identity.ResolveGitHubLogin(ctx, contractid.ResolveIdentity{Subject: "gh", Email: "gh@example.com"})
	must(t, err)
	if !owns(t, store, github.ID) {
		t.Error("a GitHub User is not an Owner")
	}
}

func TestATakenEmailCreatesNoOwner(t *testing.T) {
	ctx := context.Background()
	store := storetest.NewMemory()
	identity := directory(t, store, "alice")
	login, err := identity.ResolveGitHubLogin(ctx, contractid.ResolveIdentity{Subject: "gh", Email: "alice@example.com"})
	if err != nil || !login.EmailInUse || login.ID != "" {
		t.Fatalf("login = %#v, %v, want EmailInUse", login, err)
	}
}

func TestSigningInLocallyRepairsAMissingOwner(t *testing.T) {
	ctx := context.Background()
	store := storetest.NewMemory()
	// alice exists in Identities but the Store never heard of her, as after a
	// registration whose second write failed.
	identity := directory(t, store, "alice")
	if owns(t, store, "alice") {
		t.Fatal("alice is an Owner before signing in")
	}
	found, err := identity.FindLocalUser(ctx, "alice@example.com")
	if err != nil || !found.Found {
		t.Fatalf("FindLocalUser = %#v, %v", found, err)
	}
	if !owns(t, store, "alice") {
		t.Fatal("signing in did not make alice an Owner")
	}
	if missing, err := identity.FindLocalUser(ctx, "nobody@example.com"); err != nil || missing.Found {
		t.Fatalf("absent user = %#v, %v", missing, err)
	}
}

func TestADuplicateRegistrationIsAConflict(t *testing.T) {
	identity := directory(t, storetest.NewMemory(), "alice")
	err := identity.CreateLocalUser(context.Background(), contractid.CreateLocalUser{ID: "other", Email: "ALICE@example.com", PasswordHash: "h"})
	if !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("duplicate = %v, want a conflict", err)
	}
}
