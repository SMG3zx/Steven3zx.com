// Package storetest holds the in-memory app.Store and the contract suite that
// every app.Store adapter, real or test, must pass.
package storetest

import (
	"context"
	"janus/internal/app"
	"strings"
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
		{"Identity", testIdentity},
		{"GitHubConnection", testGitHubConnection},
		{"ProjectsAreOwned", testProjectsAreOwned},
		{"ImportCreatesAnActiveSnapshot", testImportCreatesAnActiveSnapshot},
		{"ImportJobStateMachine", testImportJobStateMachine},
		{"StagingSnapshotsAndResume", testStagingSnapshotsAndResume},
		{"CompletingACancelledImportFails", testCompletingACancelledImportFails},
		{"BuildsFollowTheirStateMachine", testBuildsFollowTheirStateMachine},
		{"DeletingAnAccountCascades", testDeletingAnAccountCascades},
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

// importSnapshot imports a ready Snapshot of two files into project, the way an
// import job does, and returns its ID.
func importSnapshot(t *testing.T, store app.Store, owner, project, name string) string {
	t.Helper()
	ctx := context.Background()
	job, snapshot := name+"-job", name+"-snapshot"
	commit := strings.Repeat("a", 40)
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: job, OwnerID: owner, ProjectID: project, Repository: owner + "/repo", Branch: "main", Commit: commit}))
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: snapshot, JobID: job, OwnerID: owner, ProjectID: project, RepositoryID: 7,
		Repository: owner + "/repo", Branch: "main", Commit: commit, DefaultBranch: "main", Language: "Rust", Runtime: "rust", Stars: 3, Forks: 1, MetadataJSON: "{}"}))
	must(t, store.AddSnapshotFiles(ctx, snapshot, []app.SnapshotFile{
		{Path: "src/lib.rs", Mode: "100644", Hash: "h2", Content: []byte("lib")},
		{Path: "Cargo.toml", Mode: "100644", Hash: "h1", Content: []byte("cargo")},
	}))
	for _, status := range []string{"cloning", "scanning", "storing"} {
		must(t, store.UpdateImportJob(ctx, app.UpdateImportJob{ID: job, Status: status, Progress: 50}))
	}
	must(t, store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: snapshot, JobID: job, EntryPoint: "src/lib.rs", FileCount: 2, TotalBytes: 8}))
	return snapshot
}

func testIdentity(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: " Alice@Example.com ", PasswordHash: "hash"}))

	err := store.CreateLocalUser(ctx, app.CreateLocalUser{ID: "other", Email: "alice@example.com", PasswordHash: "hash"})
	if err == nil || !app.IsReply(err) || !strings.Contains(strings.ToLower(err.Error()), "constraint failed") {
		t.Fatalf("duplicate email = %v, want a constraint failure the actor reported", err)
	}

	found, err := store.FindLocalUser(ctx, "ALICE@example.com")
	must(t, err)
	if !found.Found || found.ID != "alice" || found.Email != "Alice@Example.com" || found.PasswordHash != "hash" {
		t.Fatalf("app.FindLocalUser = %#v", found)
	}
	if missing, err := store.FindLocalUser(ctx, "nobody@example.com"); err != nil || missing.Found {
		t.Fatalf("absent user = %#v, %v, want found=false and no error", missing, err)
	}
	if user, err := store.FindUser(ctx, "alice"); err != nil || !user.Found || user.ID != "alice" {
		t.Fatalf("app.FindUser = %#v, %v", user, err)
	}

	first, err := store.ResolveGoogleIdentity(ctx, app.ResolveGoogleIdentity{Subject: "g-1", Email: "g@example.com", DisplayName: "G"})
	must(t, err)
	second, err := store.ResolveGoogleIdentity(ctx, app.ResolveGoogleIdentity{Subject: "g-1", Email: "changed@example.com", DisplayName: "Changed"})
	must(t, err)
	if first.ID == "" || second.ID != first.ID || second.Email != "g@example.com" {
		t.Fatalf("Google identity resolved to %#v then %#v, want the same Account by subject", first, second)
	}

	inUse, err := store.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-9", Email: "ALICE@example.com"})
	must(t, err)
	if !inUse.EmailInUse || inUse.ID != "" {
		t.Fatalf("GitHub login with a taken email = %#v, want EmailInUse and no Account", inUse)
	}
	if _, err = store.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-9"}); err == nil || !strings.Contains(err.Error(), "verified primary email") {
		t.Fatalf("GitHub login without an email = %v", err)
	}
	created, err := store.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-1", Email: "new@example.com", DisplayName: "New"})
	must(t, err)
	again, err := store.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-1", Email: "elsewhere@example.com"})
	must(t, err)
	if created.ID == "" || again.ID != created.ID {
		t.Fatalf("GitHub login created %#v then %#v, want the same Account by subject", created, again)
	}
}

func testGitHubConnection(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}))
	must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: "bob", Email: "bob@example.com", PasswordHash: "h"}))

	if token, connected, err := store.FindGitHubToken(ctx, "alice"); err != nil || connected || token != "" {
		t.Fatalf("before connecting = %q, %v, %v", token, connected, err)
	}
	must(t, store.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-alice", Token: "secret"}))
	if token, connected, err := store.FindGitHubToken(ctx, "alice"); err != nil || !connected || token != "secret" {
		t.Fatalf("after connecting = %q, %v, %v", token, connected, err)
	}
	if err := store.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "bob", Subject: "gh-alice", Token: "t"}); err == nil || !strings.Contains(err.Error(), "another Janus account") {
		t.Fatalf("connecting alice's GitHub to bob = %v", err)
	}
	if err := store.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-other", Token: "t"}); err == nil || !strings.Contains(err.Error(), "different GitHub account") {
		t.Fatalf("connecting a second GitHub to alice = %v", err)
	}
	if err := store.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-alice"}); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("connecting without a token = %v", err)
	}
	must(t, store.DisconnectGitHub(ctx, "alice"))
	if _, connected, err := store.FindGitHubToken(ctx, "alice"); err != nil || connected {
		t.Fatalf("after disconnecting connected=%v err=%v", connected, err)
	}
}

func testProjectsAreOwned(t *testing.T, store app.Store) {
	ctx := context.Background()
	for _, id := range []string{"alice", "bob"} {
		must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: id, Email: id + "@example.com", PasswordHash: "h"}))
	}
	created, err := store.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "  First  ", Description: " one "})
	must(t, err)
	if created.Name != "First" || created.Description != "one" || created.OwnerID != "alice" || created.CreatedAt == "" || created.MetadataJSON != "{}" {
		t.Fatalf("created project = %#v", created)
	}
	_, err = store.CreateProject(ctx, app.CreateProject{ID: "p2", OwnerID: "alice", Name: "Second"})
	must(t, err)

	if _, found, err := store.GetProject(ctx, "p1", "bob"); err != nil || found {
		t.Fatalf("bob read alice's project: found=%v err=%v", found, err)
	}
	if _, err = store.UpdateProject(ctx, app.UpdateProject{ID: "p1", OwnerID: "bob", Name: "Mine"}); err == nil || !strings.Contains(err.Error(), "project not found") {
		t.Fatalf("bob updating alice's project = %v", err)
	}
	if deleted, err := store.DeleteProject(ctx, "p1", "bob"); err != nil || deleted {
		t.Fatalf("bob deleting alice's project = %v, %v", deleted, err)
	}

	updated, err := store.UpdateProject(ctx, app.UpdateProject{ID: "p1", OwnerID: "alice", Name: "Renamed", Description: ""})
	must(t, err)
	if updated.Name != "Renamed" {
		t.Fatalf("updated project = %#v", updated)
	}
	listed, err := store.ListProjects(ctx, "alice")
	must(t, err)
	// Order is not asserted: the database stamps an update with whole seconds
	// and an insert with milliseconds, so a project updated in the same second
	// as another's creation can sort behind it.
	if len(listed) != 2 || listed[0].ID == listed[1].ID {
		t.Fatalf("projects = %#v, want both of alice's", listed)
	}
	if none, err := store.ListProjects(ctx, "bob"); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("bob's projects = %#v, %v, want an empty list", none, err)
	}
	if deleted, err := store.DeleteProject(ctx, "p2", "alice"); err != nil || !deleted {
		t.Fatalf("deleting p2 = %v, %v", deleted, err)
	}
	if _, found, _ := store.GetProject(ctx, "p2", "alice"); found {
		t.Fatal("deleted project is still found")
	}
}

func testImportCreatesAnActiveSnapshot(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}))
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "P"})
	must(t, err)
	first := importSnapshot(t, store, "alice", "p1", "one")

	project, found, err := store.GetProject(ctx, "p1", "alice")
	must(t, err)
	if !found || project.SnapshotID != first || project.Language != "Rust" || project.Runtime != "rust" || project.EntryPoint != "src/lib.rs" ||
		project.FileCount != 2 || project.Repository != "alice/repo" || project.RepositoryID != 7 || project.Stars != 3 || project.SnapshotCount != 1 {
		t.Fatalf("project after import = %#v", project)
	}

	second := importSnapshot(t, store, "alice", "p1", "two")
	snapshots, err := store.ListSnapshots(ctx, "alice", "p1")
	must(t, err)
	if len(snapshots) != 2 || snapshots[0].ID != second || !snapshots[0].Active || snapshots[1].ID != first || snapshots[1].Active {
		t.Fatalf("snapshots = %#v, want the newest first and active", snapshots)
	}
	if activated, err := store.ActivateSnapshot(ctx, "alice", "p1", first); err != nil || !activated {
		t.Fatalf("reactivating = %v, %v", activated, err)
	}
	if activated, err := store.ActivateSnapshot(ctx, "alice", "p1", "missing"); err != nil || activated {
		t.Fatalf("activating a missing Snapshot = %v, %v", activated, err)
	}
	if project, _, _ = store.GetProject(ctx, "p1", "alice"); project.SnapshotID != first || project.SnapshotCount != 2 {
		t.Fatalf("project after reactivation = %#v", project)
	}

	must(t, store.SetProjectEntryPoint(ctx, app.SetProjectEntryPoint{OwnerID: "alice", ProjectID: "p1", SnapshotID: first, EntryPoint: "Cargo.toml"}))
	if err = store.SetProjectEntryPoint(ctx, app.SetProjectEntryPoint{OwnerID: "alice", ProjectID: "p1", SnapshotID: "missing", EntryPoint: "x"}); err == nil {
		t.Fatal("setting an Entrypoint on a missing Snapshot succeeded")
	}

	files, err := store.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "alice", ProjectID: "p1", SnapshotID: first})
	must(t, err)
	if len(files) != 2 || files[0].Path != "Cargo.toml" || files[1].Path != "src/lib.rs" || string(files[0].Content) != "cargo" || files[0].Hash != "h1" {
		t.Fatalf("files = %#v, want both, sorted by path", files)
	}
	page, err := store.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "alice", ProjectID: "p1", SnapshotID: first, Offset: 1, Limit: 1})
	must(t, err)
	if len(page) != 1 || page[0].Path != "src/lib.rs" {
		t.Fatalf("second page = %#v", page)
	}
	if _, err = store.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "bob", ProjectID: "p1", SnapshotID: first}); err == nil {
		t.Fatal("another Account listed the project's files")
	}
	if _, err = store.ListSnapshots(ctx, "bob", "p1"); err == nil {
		t.Fatal("another Account listed the project's Snapshots")
	}
}

func testImportJobStateMachine(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}))
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "P"})
	must(t, err)
	commit := strings.Repeat("c", 40)

	if err = store.CreateImportJob(ctx, app.CreateImportJob{ID: "j0", OwnerID: "bob", ProjectID: "p1", Repository: "r", Branch: "main", Commit: commit}); err == nil {
		t.Fatal("another Account started an import in alice's project")
	}
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: "j1", OwnerID: "alice", ProjectID: "p1", Repository: "alice/repo", Branch: "main", Commit: commit}))
	err = store.CreateImportJob(ctx, app.CreateImportJob{ID: "j2", OwnerID: "alice", ProjectID: "p1", Repository: "alice/repo", Branch: "main", Commit: commit})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "constraint failed") {
		t.Fatalf("a second active import = %v, want a constraint failure", err)
	}
	if deleted, err := store.DeleteProject(ctx, "p1", "alice"); err == nil || deleted || !strings.Contains(err.Error(), "import in progress") {
		t.Fatalf("deleting a project with an import in progress = %v, %v", deleted, err)
	}

	job, found, err := store.FindImportJob(ctx, "j1", "alice")
	must(t, err)
	if !found || job.Status != "queued" || job.Repository != "alice/repo" || job.CreatedAt == "" {
		t.Fatalf("new job = %#v", job)
	}
	if _, found, _ = store.FindImportJob(ctx, "j1", "bob"); found {
		t.Fatal("another Account found the job")
	}

	if err = store.UpdateImportJob(ctx, app.UpdateImportJob{ID: "j1", Status: "complete", Progress: 100}); err == nil || !strings.Contains(err.Error(), "cannot move from queued to complete") {
		t.Fatalf("queued to complete = %v", err)
	}
	must(t, store.UpdateImportJob(ctx, app.UpdateImportJob{ID: "j1", Status: "failed", Progress: 100, Error: "clone failed"}))
	if err = store.UpdateImportJob(ctx, app.UpdateImportJob{ID: "j1", Status: "queued"}); err == nil {
		t.Fatal("a failed job moved back to queued")
	}
	if err = store.UpdateImportJob(ctx, app.UpdateImportJob{ID: "nope", Status: "failed"}); err == nil || !strings.Contains(err.Error(), "job not found") {
		t.Fatalf("updating a missing job = %v", err)
	}
	if job, _, _ = store.FindImportJob(ctx, "j1", "alice"); job.Status != "failed" || job.Error != "clone failed" || job.Progress != 100 {
		t.Fatalf("failed job = %#v", job)
	}
}

func testStagingSnapshotsAndResume(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}))
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "P"})
	must(t, err)
	commit := strings.Repeat("d", 40)
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: "job", OwnerID: "alice", ProjectID: "p1", Repository: "alice/repo", Branch: "main", Commit: commit}))
	must(t, store.UpdateImportJob(ctx, app.UpdateImportJob{ID: "job", Status: "cloning"}))
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: "staging", JobID: "job", OwnerID: "alice", ProjectID: "p1", Repository: "alice/repo", Branch: "main", Commit: commit, MetadataJSON: "{}", Manifest: "[manifest]"}))
	must(t, store.AddSnapshotFiles(ctx, "staging", []app.SnapshotFile{{Path: "main.go", Mode: "100644", Hash: "abc", Content: []byte("x")}}))
	if err = store.AddSnapshotFiles(ctx, "staging", []app.SnapshotFile{{Path: "main.go", Mode: "100644", Hash: "abc", Content: []byte("x")}}); err == nil {
		t.Fatal("storing the same path twice succeeded")
	}
	must(t, store.UpdateImportJob(ctx, app.UpdateImportJob{ID: "job", Status: "queued"}))

	staging, err := store.FindStagingSnapshot(ctx, "job")
	must(t, err)
	if !staging.Found || staging.SnapshotID != "staging" || staging.Manifest != "[manifest]" || staging.Files["main.go"] != "abc" {
		t.Fatalf("staging snapshot = %#v", staging)
	}
	imports, err := store.ListResumableImports(ctx)
	must(t, err)
	if len(imports) != 1 || imports[0].JobID != "job" || imports[0].Repository != "alice/repo" || imports[0].Commit != commit {
		t.Fatalf("resumable imports = %#v", imports)
	}

	must(t, store.DiscardJobStaging(ctx, "job"))
	if staging, err = store.FindStagingSnapshot(ctx, "job"); err != nil || staging.Found {
		t.Fatalf("after discarding = %#v, %v", staging, err)
	}
	if snapshots, _ := store.ListSnapshots(ctx, "alice", "p1"); len(snapshots) != 0 {
		t.Fatalf("a staging snapshot shows as ready: %#v", snapshots)
	}
}

func testCompletingACancelledImportFails(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}))
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "P"})
	must(t, err)
	commit := strings.Repeat("e", 40)
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: "job", OwnerID: "alice", ProjectID: "p1", Repository: "alice/repo", Branch: "main", Commit: commit}))
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: "s", JobID: "job", OwnerID: "alice", ProjectID: "p1", Repository: "alice/repo", Branch: "main", Commit: commit, MetadataJSON: "{}"}))
	must(t, store.UpdateImportJob(ctx, app.UpdateImportJob{ID: "job", Status: "cancelled", Progress: 100}))

	err = store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: "s", JobID: "job", FileCount: 1})
	if err == nil || !strings.Contains(err.Error(), "cancelled before it completed") {
		t.Fatalf("completing a cancelled import = %v", err)
	}
	if snapshots, _ := store.ListSnapshots(ctx, "alice", "p1"); len(snapshots) != 0 {
		t.Fatalf("the failed completion left a ready Snapshot: %#v", snapshots)
	}
	must(t, store.DeleteStagingSnapshot(ctx, "s"))
	if staging, _ := store.FindStagingSnapshot(ctx, "job"); staging.Found {
		t.Fatal("the staging snapshot was not deleted")
	}
}

func testBuildsFollowTheirStateMachine(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}))
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "P"})
	must(t, err)
	snapshot := importSnapshot(t, store, "alice", "p1", "one")

	if err = store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b0", OwnerID: "bob", ProjectID: "p1", SnapshotID: snapshot, Target: "t"}); err == nil || !strings.Contains(err.Error(), "snapshot not found") {
		t.Fatalf("another Account building alice's Snapshot = %v", err)
	}
	if err = store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b0", OwnerID: "alice", ProjectID: "p1", SnapshotID: "missing", Target: "t"}); err == nil {
		t.Fatal("building a missing Snapshot succeeded")
	}
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown"}))
	err = store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b2", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown"})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "constraint failed") {
		t.Fatalf("a second active build = %v, want a constraint failure", err)
	}

	queued, err := store.ListResumableBuilds(ctx)
	must(t, err)
	if len(queued) != 1 || queued[0].JobID != "b1" || queued[0].Language != "Rust" || queued[0].SnapshotID != snapshot || queued[0].Target != "wasm32-unknown-unknown" {
		t.Fatalf("resumable builds = %#v", queued)
	}
	if artifact, err := store.FindWASMBuildArtifact(ctx, "b1", "alice"); err != nil || artifact.Found {
		t.Fatalf("artifact of an unfinished build = %#v, %v", artifact, err)
	}
	if err = store.UpdateWASMBuild(ctx, app.UpdateWASMBuild{ID: "b1", Status: "complete"}); err == nil || !strings.Contains(err.Error(), "cannot move from queued to complete") {
		t.Fatalf("queued to complete = %v", err)
	}
	must(t, store.UpdateWASMBuild(ctx, app.UpdateWASMBuild{ID: "b1", Status: "building", Progress: 40, Log: "compiling"}))
	must(t, store.CompleteWASMBuild(ctx, app.CompleteWASMBuild{ID: "b1", ArtifactName: "wasm-artifacts.zip", Artifact: []byte("zip"), Log: []byte("done")}))
	if err = store.CompleteWASMBuild(ctx, app.CompleteWASMBuild{ID: "b1", ArtifactName: "x", Artifact: []byte("y")}); err == nil || !strings.Contains(err.Error(), "WASM build not found") {
		t.Fatalf("completing a finished build = %v", err)
	}

	build, found, err := store.FindWASMBuild(ctx, "b1", "alice")
	must(t, err)
	if !found || build.Status != "complete" || build.Progress != 100 || build.ArtifactName != "wasm-artifacts.zip" || build.Log != "done" || build.SnapshotID != snapshot {
		t.Fatalf("finished build = %#v", build)
	}
	if _, found, _ = store.FindWASMBuild(ctx, "b1", "bob"); found {
		t.Fatal("another Account found the build")
	}
	artifact, err := store.FindWASMBuildArtifact(ctx, "b1", "alice")
	must(t, err)
	if !artifact.Found || artifact.Name != "wasm-artifacts.zip" || string(artifact.Data) != "zip" {
		t.Fatalf("artifact = %#v", artifact)
	}
	if other, _ := store.FindWASMBuildArtifact(ctx, "b1", "bob"); other.Found {
		t.Fatal("another Account downloaded the artifact")
	}
	// With the first build finished, a new one may start.
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b3", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown"}))
}

func testDeletingAnAccountCascades(t *testing.T, store app.Store) {
	ctx := context.Background()
	for _, id := range []string{"alice", "bob"} {
		must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: id, Email: id + "@example.com", PasswordHash: "h"}))
	}
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "P"})
	must(t, err)
	_, err = store.CreateProject(ctx, app.CreateProject{ID: "pb", OwnerID: "bob", Name: "B"})
	must(t, err)
	must(t, store.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "gh-alice", Token: "t"}))
	snapshot := importSnapshot(t, store, "alice", "p1", "one")
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "t"}))

	active, err := store.ListActiveJobs(ctx, "alice")
	must(t, err)
	if len(active) != 1 || active[0] != (app.ActiveJob{Kind: "wasm_build", ID: "b1", ProjectID: "p1", Status: "queued"}) {
		t.Fatalf("active jobs = %#v", active)
	}
	refused, err := store.DeleteAccount(ctx, "alice", false)
	must(t, err)
	if refused.Deleted || len(refused.ActiveJobs) != 1 {
		t.Fatalf("deleting with an unfinished build = %#v, want a refusal listing it", refused)
	}
	if user, _ := store.FindUser(ctx, "alice"); !user.Found {
		t.Fatal("a refused deletion removed the Account")
	}

	deleted, err := store.DeleteAccount(ctx, "alice", true)
	must(t, err)
	if !deleted.Deleted {
		t.Fatalf("deleting with cancel = %#v", deleted)
	}
	if user, _ := store.FindUser(ctx, "alice"); user.Found {
		t.Fatal("the Account is still found")
	}
	if _, found, _ := store.GetProject(ctx, "p1", "alice"); found {
		t.Fatal("the project survived its Account")
	}
	if _, found, _ := store.FindWASMBuild(ctx, "b1", "alice"); found {
		t.Fatal("the build survived its Account")
	}
	if _, connected, _ := store.FindGitHubToken(ctx, "alice"); connected {
		t.Fatal("the GitHub Connection survived its Account")
	}
	if again, err := store.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: "gh-alice", Email: "alice-new@example.com"}); err != nil || again.ID == "alice" {
		t.Fatalf("the deleted Account's GitHub identity still resolves to it: %#v, %v", again, err)
	}
	if _, found, _ := store.GetProject(ctx, "pb", "bob"); !found {
		t.Fatal("deleting alice removed bob's project")
	}
	if missing, err := store.DeleteAccount(ctx, "ghost", false); err != nil || missing.Deleted {
		t.Fatalf("deleting a missing Account = %#v, %v", missing, err)
	}
}
