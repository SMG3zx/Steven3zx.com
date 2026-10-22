// Package storetest holds the in-memory app.Store and the contract suite that
// every app.Store adapter, real or test, must pass.
package storetest

import (
	"context"
	"janus/internal/app"
	"testing"
)

// Run runs the Store contract against adapters made by newStore. Each case
// gets a fresh, empty Store.
func Run(t *testing.T, newStore func(t *testing.T) app.Store) {
	t.Helper()
	for _, c := range []struct {
		name string
		test func(t *testing.T, store app.Store)
	}{
		{"OwnersAreAddedOnce", testOwners},
		{"ReferencesGoWithWhatRefersToThem", testReferences},
		{"BuildReferencesFollowTheBuildsService", testBuildReferencesFollowTheBuildsService},
		{"DeletingAnAccountCascades", testDeletingAnAccountCascades},
		{"ConcurrentCallersNeverDeadlock", testConcurrentCallers},
	} {
		t.Run(c.name, func(t *testing.T) { c.test(t, newStore(t)) })
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// ReadySnapshot records what a WASM Build needs to refer to: Owner, Project
// and a ready Snapshot of language, with the runtime the scan would give it.
// It returns the Snapshot's ID. Adding any of them again changes nothing.
func ReadySnapshot(t testing.TB, store app.Store, owner, project, snapshot, language string) string {
	t.Helper()
	ctx := context.Background()
	runtimes := map[string]string{"Rust": "rust", "Go": "go", "JavaScript": "node", "TypeScript": "node"}
	for _, err := range []error{
		store.AddOwner(ctx, owner),
		store.AddProject(ctx, app.ProjectRef{ID: project, OwnerID: owner}),
		store.AddSnapshot(ctx, app.SnapshotRef{ID: snapshot, ProjectID: project, OwnerID: owner, Language: language, Runtime: runtimes[language]}),
	} {
		if err != nil {
			t.Fatalf("record snapshot %s of %s: %v", snapshot, project, err)
		}
	}
	return snapshot
}

func testOwners(t *testing.T, store app.Store) {
	ctx := context.Background()
	if err := store.AddProject(ctx, app.ProjectRef{ID: "early", OwnerID: "alice"}); err == nil {
		t.Fatal("a Project was recorded for an Owner that was never added")
	}
	must(t, store.AddOwner(ctx, "alice"))
	must(t, store.AddOwner(ctx, "alice")) // adding again changes nothing
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "p1", OwnerID: "alice"}))
	must(t, store.AddOwner(ctx, "alice"))
	if projects, err := store.ListOwnedProjects(ctx, "alice"); err != nil || len(projects) != 1 || projects[0] != "p1" {
		t.Fatalf("adding an Owner again lost its Project: %v, %v", projects, err)
	}
	if deleted, err := store.DeleteAccount(ctx, "nobody"); err != nil || deleted.Deleted {
		t.Fatalf("deleting an Owner that was never added = %#v, %v, want nothing deleted", deleted, err)
	}
}

func testReferences(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.AddOwner(ctx, "bob"))
	ReadySnapshot(t, store, "alice", "p1", "s1", "Rust")
	ReadySnapshot(t, store, "alice", "p1", "s2", "Go")
	ReadySnapshot(t, store, "alice", "p1", "s1", "Go") // already recorded: kept as it was
	if err := store.AddSnapshot(ctx, app.SnapshotRef{ID: "orphan", ProjectID: "unknown", OwnerID: "alice"}); err == nil {
		t.Fatal("a Snapshot was recorded for a Project that was not")
	}
	got, found, err := store.FindSnapshot(ctx, "s1", "alice")
	if err != nil || !found || got != (app.SnapshotRef{ID: "s1", ProjectID: "p1", OwnerID: "alice", Language: "Rust", Runtime: "rust"}) {
		t.Fatalf("FindSnapshot = %#v, %v, %v", got, found, err)
	}
	if _, found, _ := store.FindSnapshot(ctx, "s1", "bob"); found {
		t.Fatal("bob found alice's Snapshot")
	}
	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: "s1", Target: "t", Status: "failed"}))
	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b2", OwnerID: "alice", ProjectID: "p1", SnapshotID: "s2", Target: "t", Status: "queued"}))

	must(t, store.RemoveSnapshot(ctx, "s1"))
	must(t, store.RemoveSnapshot(ctx, "s1")) // removing again changes nothing
	if _, found, _ := store.FindWASMBuild(ctx, "b1", "alice"); found {
		t.Fatal("a Build outlived its Snapshot")
	}
	if _, found, _ := store.FindWASMBuild(ctx, "b2", "alice"); !found {
		t.Fatal("removing one Snapshot removed another's Build")
	}
	must(t, store.RemoveProject(ctx, "p1"))
	if _, found, _ := store.FindSnapshot(ctx, "s2", "alice"); found {
		t.Fatal("a Snapshot outlived its Project")
	}
	if _, found, _ := store.FindWASMBuild(ctx, "b2", "alice"); found {
		t.Fatal("a Build outlived its Project")
	}
	if projects, _ := store.ListOwnedProjects(ctx, "alice"); len(projects) != 0 {
		t.Fatalf("alice's Projects after removal = %v", projects)
	}
}

// testBuildReferencesFollowTheBuildsService holds the rules of the Store's
// records of WASM Builds: the builds service publishes each state, the Store
// keeps it, applying a state again changes nothing, and a Build of a Project or
// Snapshot the Store does not reference is not kept.
func testBuildReferencesFollowTheBuildsService(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.AddOwner(ctx, "alice"))
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "p1", OwnerID: "alice"}))
	snapshot := ReadySnapshot(t, store, "alice", "p1", "one-snapshot", "Rust")

	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b0", OwnerID: "alice", ProjectID: "p1", SnapshotID: "missing", Target: "t", Status: "queued"}))
	if _, found, _ := store.FindWASMBuild(ctx, "b0", "alice"); found {
		t.Fatal("a Build of an unreferenced Snapshot was kept")
	}

	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown", Status: "queued"}))
	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown", Status: "building"}))
	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown", Status: "building"}))
	build, found, err := store.FindWASMBuild(ctx, "b1", "alice")
	must(t, err)
	if !found || build.Status != "building" || build.SnapshotID != snapshot || build.Target != "wasm32-unknown-unknown" {
		t.Fatalf("building build = %#v, found %v", build, found)
	}
	if _, found, _ = store.FindWASMBuild(ctx, "b1", "bob"); found {
		t.Fatal("another Account found the build")
	}
	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown", Status: "complete"}))
	build, _, err = store.FindWASMBuild(ctx, "b1", "alice")
	must(t, err)
	if build.Status != "complete" {
		t.Fatalf("finished build = %#v", build)
	}

	must(t, store.ApplyCheck(ctx, app.CheckChange{BuildID: "b1", OwnerID: "alice", Status: app.CheckPassed, Log: "tests ok"}))
	check, found, err := store.FindCheck(ctx, "b1", "alice")
	must(t, err)
	if !found || check.Status != app.CheckPassed || check.Log != "tests ok" {
		t.Fatalf("check = %#v, found %v", check, found)
	}
	if _, found, _ = store.FindCheck(ctx, "b1", "bob"); found {
		t.Fatal("another Account found the check")
	}
	must(t, store.ApplyCheck(ctx, app.CheckChange{BuildID: "missing", OwnerID: "alice", Status: app.CheckPassed}))
	if _, found, _ = store.FindCheck(ctx, "missing", "alice"); found {
		t.Fatal("a check of an unknown build was kept")
	}
}

func testDeletingAnAccountCascades(t *testing.T, store app.Store) {
	ctx := context.Background()
	for _, id := range []string{"alice", "bob"} {
		must(t, store.AddOwner(ctx, id))
	}
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "p1", OwnerID: "alice"}))
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "pb", OwnerID: "bob"}))
	snapshot := ReadySnapshot(t, store, "alice", "p1", "one-snapshot", "Rust")
	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "t", Status: "queued"}))

	deleted, err := store.DeleteAccount(ctx, "alice")
	must(t, err)
	if !deleted.Deleted {
		t.Fatalf("deleting an Account = %#v", deleted)
	}
	if projects, _ := store.ListOwnedProjects(ctx, "alice"); len(projects) != 0 {
		t.Fatal("the project survived its Account")
	}
	if _, found, _ := store.FindWASMBuild(ctx, "b1", "alice"); found {
		t.Fatal("the build survived its Account")
	}
	if again, err := store.DeleteAccount(ctx, "alice"); err != nil || again.Deleted {
		t.Fatalf("deleting the Owner again = %#v, %v, want nothing left to delete", again, err)
	}
	if projects, _ := store.ListOwnedProjects(ctx, "bob"); len(projects) != 1 {
		t.Fatal("deleting alice removed bob's project")
	}
	if missing, err := store.DeleteAccount(ctx, "ghost"); err != nil || missing.Deleted {
		t.Fatalf("deleting a missing Account = %#v, %v", missing, err)
	}
}
