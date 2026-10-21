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

type cancelCounter struct{ owners []string }

func (c *cancelCounter) CancelOwner(owner string) int {
	c.owners = append(c.owners, owner)
	return 1
}

func TestDeleteRefusesWhileJobsAreActiveUnlessTheOwnerCancelsThem(t *testing.T) {
	store, catalog := storetest.NewMemory(), catalogtest.NewMemory()
	importedProject(t, catalog, store, "bob", "bob-project", "Rust")
	ctx := context.Background()
	catalog.SeedImport("bob", "bob-project", "bob-job", "queued")
	running := &cancelCounter{}
	identities := users(t, "bob")
	accounts := app.NewAccounts(store, identities, catalog, running)

	blocked, err := accounts.Delete(ctx, "bob", false)
	if err != nil || blocked.Deleted || len(blocked.ActiveJobs) != 1 || blocked.ActiveJobs[0].ID != "bob-job" {
		t.Fatalf("blocked = %+v, %v", blocked, err)
	}
	if len(running.owners) != 0 {
		t.Fatal("running jobs were stopped for a refused deletion")
	}
	deleted, err := accounts.Delete(ctx, "bob", true)
	if err != nil || !deleted.Deleted {
		t.Fatalf("deleted = %+v, %v", deleted, err)
	}
	if len(running.owners) != 1 || running.owners[0] != "bob" {
		t.Fatalf("stopped jobs of %v, want bob's", running.owners)
	}
	if found, _ := identities.FindUser(ctx, "bob"); found.Found {
		t.Fatal("the account still exists")
	}
	if _, found, _ := catalog.GetProject(ctx, "bob-project", "bob"); found {
		t.Fatal("the account's project still exists")
	}
	if projects, _ := store.ListOwnedProjects(ctx, "bob"); len(projects) != 0 {
		t.Fatal("the account's project is still recorded for its Builds")
	}
	if again, err := accounts.Delete(ctx, "bob", true); err != nil || again.Deleted {
		t.Fatalf("deleting a missing account = %+v, %v", again, err)
	}
}

func TestDeleteWithoutActiveJobsStopsNothing(t *testing.T) {
	store, catalog := storetest.NewMemory(), catalogtest.NewMemory()
	importedProject(t, catalog, store, "carol", "carol-project", "Rust")
	running := &cancelCounter{}
	deleted, err := app.NewAccounts(store, users(t, "carol"), catalog, running).Delete(context.Background(), "carol", false)
	if err != nil || !deleted.Deleted || len(running.owners) != 0 {
		t.Fatalf("deleted = %+v, %v, stopped %v", deleted, err, running.owners)
	}
}
