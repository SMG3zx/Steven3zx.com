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

// ownerDeleter records the Owners whose WASM Builds were deleted.
type ownerDeleter struct{ owners []string }

func (d *ownerDeleter) DeleteOwner(_ context.Context, owner string) error {
	d.owners = append(d.owners, owner)
	return nil
}

func TestDeleteRefusesWhileJobsAreActiveUnlessTheOwnerCancelsThem(t *testing.T) {
	store, catalog := storetest.NewMemory(), catalogtest.NewMemory()
	importedProject(t, catalog, store, "bob", "bob-project", "Rust")
	ctx := context.Background()
	catalog.SeedImport("bob", "bob-project", "bob-job", "queued")
	builds := &ownerDeleter{}
	identities := users(t, "bob")
	accounts := app.NewAccounts(store, identities, catalog).WithBuilds(builds)

	blocked, err := accounts.Delete(ctx, "bob", false)
	if err != nil || blocked.Deleted || len(blocked.ActiveJobs) != 1 || blocked.ActiveJobs[0].ID != "bob-job" {
		t.Fatalf("blocked = %+v, %v", blocked, err)
	}
	if len(builds.owners) != 0 {
		t.Fatal("WASM builds were deleted for a refused deletion")
	}
	deleted, err := accounts.Delete(ctx, "bob", true)
	if err != nil || !deleted.Deleted {
		t.Fatalf("deleted = %+v, %v", deleted, err)
	}
	if len(builds.owners) != 1 || builds.owners[0] != "bob" {
		t.Fatalf("deleted builds of %v, want bob's", builds.owners)
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

func TestDeleteWithoutActiveJobsDeletesTheBuildsToo(t *testing.T) {
	store, catalog := storetest.NewMemory(), catalogtest.NewMemory()
	importedProject(t, catalog, store, "carol", "carol-project", "Rust")
	builds := &ownerDeleter{}
	deleted, err := app.NewAccounts(store, users(t, "carol"), catalog).WithBuilds(builds).Delete(context.Background(), "carol", false)
	if err != nil || !deleted.Deleted || len(builds.owners) != 1 || builds.owners[0] != "carol" {
		t.Fatalf("deleted = %+v, %v, builds deleted for %v", deleted, err, builds.owners)
	}
}
