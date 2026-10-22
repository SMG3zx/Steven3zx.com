package app_test

import (
	"context"
	"testing"

	"janus/internal/adapters/catalogtest"
	"janus/internal/adapters/identitytest"
	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

// users returns Identities holding a local User for each id.
func users(t *testing.T, ids ...string) *identitytest.Memory {
	t.Helper()
	identities := identitytest.NewMemory()
	for _, id := range ids {
		must(t, identities.CreateLocalUser(context.Background(), app.CreateLocalUser{ID: id, Email: id + "@example.com", PasswordHash: "h"}))
	}
	return identities
}

func TestDeleteRefusesWhileJobsAreActiveUnlessTheOwnerCancelsThem(t *testing.T) {
	store, catalog := storetest.NewMemory(), catalogtest.NewMemory()
	importedProject(t, catalog, store, "bob", "bob-project", "Rust")
	ctx := context.Background()
	catalog.SeedImport("bob", "bob-project", "bob-job", "queued")
	identities := users(t, "bob")
	accounts := app.NewAccounts(store, identities, catalog)

	blocked, err := accounts.Delete(ctx, "bob", false)
	if err != nil || blocked.Deleted || len(blocked.ActiveJobs) != 1 || blocked.ActiveJobs[0].ID != "bob-job" {
		t.Fatalf("blocked = %+v, %v", blocked, err)
	}
	if found, _ := identities.FindUser(ctx, "bob"); !found.Found {
		t.Fatal("a refused deletion removed the account")
	}
	deleted, err := accounts.Delete(ctx, "bob", true)
	if err != nil || !deleted.Deleted {
		t.Fatalf("deleted = %+v, %v", deleted, err)
	}
	if found, _ := identities.FindUser(ctx, "bob"); found.Found {
		t.Fatal("the account still exists")
	}
	if projects, _ := store.ListOwnedProjects(ctx, "bob"); len(projects) != 0 {
		t.Fatal("the account's project is still recorded for its Builds")
	}
	if again, err := accounts.Delete(ctx, "bob", true); err != nil || again.Deleted {
		t.Fatalf("deleting a missing account = %+v, %v", again, err)
	}
}

func TestDeleteWithoutActiveJobsRemovesTheAccount(t *testing.T) {
	store, catalog := storetest.NewMemory(), catalogtest.NewMemory()
	importedProject(t, catalog, store, "carol", "carol-project", "Rust")
	identities := users(t, "carol")
	deleted, err := app.NewAccounts(store, identities, catalog).Delete(context.Background(), "carol", false)
	if err != nil || !deleted.Deleted {
		t.Fatalf("deleted = %+v, %v", deleted, err)
	}
	if found, _ := identities.FindUser(context.Background(), "carol"); found.Found {
		t.Fatal("the account still exists")
	}
}
