// Package storetest holds the in-memory app.Store and the contract suite that
// every app.Store adapter, real or test, must pass.
package storetest

import (
	"context"
	"janus/internal/app"
	"janus/internal/domain/usage"
	"strings"
	"testing"
	"time"
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
		{"ChecksAndDeploymentsFollowTheirRules", testChecksAndDeployments},
		{"RedeployAndStopFollowTheirRules", testRedeployAndStop},
		{"DeletingAnAccountCascades", testDeletingAnAccountCascades},
		{"EnrollmentCodesAreSingleUseAndExpire", testEnrollmentCodes},
		{"AgentsAreOwned", testAgents},
		{"DeletingAnAccountRemovesItsAgentsAndCodes", testDeletingAnAccountRemovesAgents},
		{"ListAllAgentsSpansOwners", testListAllAgents},
		{"RunUsageIsOwnedAndKeptOnce", testRunUsage},
		{"RunUsageGoesWithTheAccount", testRunUsageGoesWithTheAccount},
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
	AdvanceImport(t, store, job, "queued", "cloning", "scanning", "storing")
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

	if err = store.TransitionImportJob(ctx, app.JobTransition{ID: "j1", From: "queued", To: "complete", Progress: 100}); err == nil || !strings.Contains(err.Error(), "cannot move from queued to complete") {
		t.Fatalf("queued to complete = %v", err)
	}
	if err = store.TransitionImportJob(ctx, app.JobTransition{ID: "j1", From: "cloning", To: "scanning"}); err == nil || !strings.Contains(err.Error(), "changed while it was being updated") {
		t.Fatalf("a move from a status the job is not in = %v", err)
	}
	if job, _, _ = store.FindImportJob(ctx, "j1", "alice"); job.Status != "queued" {
		t.Fatalf("a refused move changed the job: %#v", job)
	}
	must(t, store.TransitionImportJob(ctx, app.JobTransition{ID: "j1", From: "queued", To: "failed", Progress: 100, Error: "clone failed"}))
	if err = store.TransitionImportJob(ctx, app.JobTransition{ID: "j1", From: "failed", To: "queued"}); err == nil {
		t.Fatal("a failed job moved back to queued")
	}
	if err = store.TransitionImportJob(ctx, app.JobTransition{ID: "nope", From: "queued", To: "failed"}); err == nil || !strings.Contains(err.Error(), "job not found") {
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
	must(t, store.TransitionImportJob(ctx, app.JobTransition{ID: "job", From: "queued", To: "cloning"}))
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: "staging", JobID: "job", OwnerID: "alice", ProjectID: "p1", Repository: "alice/repo", Branch: "main", Commit: commit, MetadataJSON: "{}", Manifest: "[manifest]"}))
	must(t, store.AddSnapshotFiles(ctx, "staging", []app.SnapshotFile{{Path: "main.go", Mode: "100644", Hash: "abc", Content: []byte("x")}}))
	if err = store.AddSnapshotFiles(ctx, "staging", []app.SnapshotFile{{Path: "main.go", Mode: "100644", Hash: "abc", Content: []byte("x")}}); err == nil {
		t.Fatal("storing the same path twice succeeded")
	}
	must(t, store.TransitionImportJob(ctx, app.JobTransition{ID: "job", From: "cloning", To: "queued"}))

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
	must(t, store.TransitionImportJob(ctx, app.JobTransition{ID: "job", From: "queued", To: "cancelled", Progress: 100}))

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
	if err = store.TransitionWASMBuild(ctx, app.JobTransition{ID: "b1", From: "queued", To: "complete"}); err == nil || !strings.Contains(err.Error(), "cannot move from queued to complete") {
		t.Fatalf("queued to complete = %v", err)
	}
	if err = store.TransitionWASMBuild(ctx, app.JobTransition{ID: "b1", From: "building", To: "failed"}); err == nil || !strings.Contains(err.Error(), "changed while it was being updated") {
		t.Fatalf("a move from a status the build is not in = %v", err)
	}
	must(t, store.TransitionWASMBuild(ctx, app.JobTransition{ID: "b1", From: "queued", To: "building", Progress: 40, Log: "compiling"}))
	must(t, store.TransitionWASMBuild(ctx, app.JobTransition{ID: "b1", From: "building", To: "building", Progress: 60, Log: "still compiling"}))
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

// AdvanceImport moves the import job id through the statuses in order, starting
// from status from and reporting 50 percent each time, as a worker would. It is
// for tests that need a job in a given state.
func AdvanceImport(t testing.TB, store app.Store, id, from string, to ...string) {
	t.Helper()
	for _, status := range to {
		if err := store.TransitionImportJob(context.Background(), app.JobTransition{ID: id, From: from, To: status, Progress: 50}); err != nil {
			t.Fatalf("move import %s from %s to %s: %v", id, from, status, err)
		}
		from = status
	}
}

func testEnrollmentCodes(t *testing.T, store app.Store) {
	ctx := context.Background()
	for _, id := range []string{"alice", "bob"} {
		must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: id, Email: id + "@example.com", PasswordHash: "h"}))
	}
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	expires := base.Add(15 * time.Minute)

	must(t, store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "h1", OwnerID: "alice", ExpiresAt: expires}))
	if err := store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "h1", OwnerID: "bob", ExpiresAt: expires}); err == nil {
		t.Fatal("a second code with the same hash was stored")
	}

	if owner, consumed, err := store.ConsumeEnrollmentCode(ctx, "unknown", base); err != nil || consumed || owner != "" {
		t.Fatalf("consuming an unknown code = %q, %v, %v, want no owner and not consumed", owner, consumed, err)
	}
	owner, consumed, err := store.ConsumeEnrollmentCode(ctx, "h1", base.Add(time.Minute))
	must(t, err)
	if !consumed || owner != "alice" {
		t.Fatalf("consuming a valid code = %q, %v, want alice and consumed", owner, consumed)
	}
	if owner, consumed, err = store.ConsumeEnrollmentCode(ctx, "h1", base.Add(2*time.Minute)); err != nil || consumed || owner != "" {
		t.Fatalf("consuming a code twice = %q, %v, %v, want not consumed the second time", owner, consumed, err)
	}

	must(t, store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "h2", OwnerID: "alice", ExpiresAt: expires}))
	if _, consumed, err = store.ConsumeEnrollmentCode(ctx, "h2", expires); err != nil || consumed {
		t.Fatalf("consuming a code at the moment it expires = %v, %v, want not consumed", consumed, err)
	}
	if _, consumed, err = store.ConsumeEnrollmentCode(ctx, "h2", expires.Add(time.Hour)); err != nil || consumed {
		t.Fatalf("consuming an expired code = %v, %v, want not consumed", consumed, err)
	}
	must(t, store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "h3", OwnerID: "bob", ExpiresAt: expires}))
	if owner, consumed, err = store.ConsumeEnrollmentCode(ctx, "h3", expires.Add(-time.Nanosecond)); err != nil || !consumed || owner != "bob" {
		t.Fatalf("consuming a code just before it expires = %q, %v, %v, want bob and consumed", owner, consumed, err)
	}
}

func testAgents(t *testing.T, store app.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"alice", "bob"} {
		must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: id, Email: id + "@example.com", PasswordHash: "h"}))
	}
	laptop := app.CreateAgent{ID: "a1", OwnerID: "alice", Name: "laptop", Tag: "tag:acct-alice", KeyID: "k1", CreatedAt: base}
	must(t, store.CreateAgent(ctx, laptop))
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "a2", OwnerID: "alice", Name: "desktop", Tag: "tag:acct-alice", KeyID: "k2", CreatedAt: base.Add(time.Minute)}))
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "b1", OwnerID: "bob", Name: "server", Tag: "tag:acct-bob", KeyID: "k3", CreatedAt: base}))
	if err := store.CreateAgent(ctx, app.CreateAgent{ID: "a3", OwnerID: "alice", Name: "again", Tag: "tag:acct-alice", KeyID: "k1", CreatedAt: base}); err == nil {
		t.Fatal("two Agents were stored for the same key")
	}

	agents, err := store.ListAgents(ctx, "alice")
	must(t, err)
	if len(agents) != 2 || agents[0].ID != "a1" || agents[1].ID != "a2" {
		t.Fatalf("alice's Agents = %#v, want a1 then a2 by creation time", agents)
	}
	if got := agents[0]; got.OwnerID != "alice" || got.Name != "laptop" || got.Tag != "tag:acct-alice" || got.KeyID != "k1" || !got.CreatedAt.Equal(base) {
		t.Fatalf("stored Agent = %#v, want the fields it was created with", got)
	}
	if none, err := store.ListAgents(ctx, "nobody"); err != nil || len(none) != 0 {
		t.Fatalf("an unknown owner's Agents = %#v, %v, want none", none, err)
	}

	if _, found, err := store.FindAgent(ctx, "a1", "bob"); err != nil || found {
		t.Fatalf("bob found alice's Agent: %v, %v", found, err)
	}
	if got, found, err := store.FindAgent(ctx, "a1", "alice"); err != nil || !found || got.KeyID != "k1" {
		t.Fatalf("finding alice's Agent = %#v, %v, %v", got, found, err)
	}
	if deleted, err := store.DeleteAgent(ctx, "a1", "bob"); err != nil || deleted {
		t.Fatalf("bob deleted alice's Agent: %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteAgent(ctx, "a1", "alice"); err != nil || !deleted {
		t.Fatalf("alice deleting her Agent = %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteAgent(ctx, "a1", "alice"); err != nil || deleted {
		t.Fatalf("deleting an Agent twice = %v, %v, want false the second time", deleted, err)
	}
	if remaining, _ := store.ListAgents(ctx, "alice"); len(remaining) != 1 || remaining[0].ID != "a2" {
		t.Fatalf("alice's Agents after a delete = %#v", remaining)
	}
}

func testDeletingAnAccountRemovesAgents(t *testing.T, store app.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"alice", "bob"} {
		must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: id, Email: id + "@example.com", PasswordHash: "h"}))
	}
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "a1", OwnerID: "alice", Name: "laptop", Tag: "tag:acct-alice", KeyID: "k1", CreatedAt: base}))
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "b1", OwnerID: "bob", Name: "server", Tag: "tag:acct-bob", KeyID: "k2", CreatedAt: base}))
	must(t, store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "ha", OwnerID: "alice", ExpiresAt: base.Add(time.Hour)}))
	must(t, store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "hb", OwnerID: "bob", ExpiresAt: base.Add(time.Hour)}))

	deleted, err := store.DeleteAccount(ctx, "alice", false)
	must(t, err)
	if !deleted.Deleted {
		t.Fatalf("deleting the Account = %#v", deleted)
	}
	if agents, _ := store.ListAgents(ctx, "alice"); len(agents) != 0 {
		t.Fatalf("an Agent survived its Account: %#v", agents)
	}
	if _, consumed, _ := store.ConsumeEnrollmentCode(ctx, "ha", base); consumed {
		t.Fatal("an Enrollment Code survived its Account")
	}
	if agents, _ := store.ListAgents(ctx, "bob"); len(agents) != 1 {
		t.Fatalf("deleting alice changed bob's Agents: %#v", agents)
	}
	if owner, consumed, _ := store.ConsumeEnrollmentCode(ctx, "hb", base); !consumed || owner != "bob" {
		t.Fatal("deleting alice removed bob's Enrollment Code")
	}
}

func testListAllAgents(t *testing.T, store app.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"alice", "bob"} {
		must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: id, Email: id + "@example.com", PasswordHash: "h"}))
	}
	if all, err := store.ListAllAgents(ctx); err != nil || len(all) != 0 {
		t.Fatalf("Agents of an empty Store = %#v, %v", all, err)
	}
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "b1", OwnerID: "bob", Name: "server", Tag: "tag:acct-bob", KeyID: "k1", CreatedAt: base.Add(time.Minute)}))
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "a1", OwnerID: "alice", Name: "laptop", Tag: "tag:acct-alice", KeyID: "k2", CreatedAt: base}))
	all, err := store.ListAllAgents(ctx)
	must(t, err)
	if len(all) != 2 || all[0].ID != "a1" || all[1].ID != "b1" || all[1].OwnerID != "bob" || all[0].KeyID != "k2" {
		t.Fatalf("all Agents = %#v, want a1 then b1 with their fields", all)
	}
}

func runUsage(runID, owner, agent, deployment string, started time.Time) app.RunUsageRecord {
	return app.RunUsageRecord{OwnerID: owner, AgentID: agent, RunReport: app.RunReport{
		RunID: runID, DeploymentID: deployment, Status: "ok", StartedAt: started,
		Usage:          usage.Usage{Fuel: 1234, PeakMemoryBytes: 65536, DurationMS: 7, CPUTimeMicros: 900},
		NetworkBytesIn: 11, NetworkBytesOut: 22,
	}}
}

func testRunUsage(t *testing.T, store app.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"alice", "bob"} {
		must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: id, Email: id + "@example.com", PasswordHash: "h"}))
	}
	first := runUsage("r1", "alice", "a1", "d1", base)
	first.Error, first.Probe, first.Status = "boom", true, "failed"
	must(t, store.RecordRunUsage(ctx, first))
	must(t, store.RecordRunUsage(ctx, runUsage("r2", "alice", "a1", "d2", base.Add(time.Second))))
	must(t, store.RecordRunUsage(ctx, runUsage("r3", "alice", "a2", "d1", base.Add(2*time.Second))))
	must(t, store.RecordRunUsage(ctx, runUsage("r4", "bob", "b1", "d9", base)))
	// A report that arrives twice, after a reconnect, is kept once.
	again := runUsage("r1", "alice", "a1", "d1", base.Add(time.Hour))
	must(t, store.RecordRunUsage(ctx, again))

	ids := func(records []app.RunUsageRecord) string {
		var out []string
		for _, record := range records {
			out = append(out, record.RunID)
		}
		return strings.Join(out, ",")
	}
	all, err := store.ListRunUsage(ctx, app.RunUsageQuery{OwnerID: "alice"})
	must(t, err)
	if ids(all) != "r3,r2,r1" {
		t.Fatalf("alice's runs = %s, want r3,r2,r1 newest first", ids(all))
	}
	got := all[2]
	if got.OwnerID != "alice" || got.AgentID != "a1" || got.DeploymentID != "d1" || got.Status != "failed" || got.Error != "boom" || !got.Probe ||
		!got.StartedAt.Equal(base) || got.Usage != first.Usage || got.NetworkBytesIn != 11 || got.NetworkBytesOut != 22 {
		t.Fatalf("stored run = %#v, want %#v", got, first)
	}
	for query, want := range map[app.RunUsageQuery]string{
		{OwnerID: "alice", AgentID: "a1"}:                     "r2,r1",
		{OwnerID: "alice", DeploymentID: "d1"}:                "r3,r1",
		{OwnerID: "alice", AgentID: "a1", DeploymentID: "d1"}: "r1",
		{OwnerID: "alice", Limit: 1}:                          "r3",
		{OwnerID: "bob", DeploymentID: "d1"}:                  "",
		{OwnerID: "bob"}:                                      "r4",
	} {
		records, err := store.ListRunUsage(ctx, query)
		must(t, err)
		if ids(records) != want {
			t.Errorf("runs for %+v = %s, want %s", query, ids(records), want)
		}
	}
}

func testRunUsageGoesWithTheAccount(t *testing.T, store app.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"alice", "bob"} {
		must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: id, Email: id + "@example.com", PasswordHash: "h"}))
	}
	must(t, store.RecordRunUsage(ctx, runUsage("r1", "alice", "a1", "d1", base)))
	must(t, store.RecordRunUsage(ctx, runUsage("r2", "bob", "b1", "d2", base)))
	_, err := store.DeleteAccount(ctx, "alice", false)
	must(t, err)
	if records, _ := store.ListRunUsage(ctx, app.RunUsageQuery{OwnerID: "alice"}); len(records) != 0 {
		t.Fatalf("runs survived their Account: %#v", records)
	}
	if records, _ := store.ListRunUsage(ctx, app.RunUsageQuery{OwnerID: "bob"}); len(records) != 1 {
		t.Fatalf("deleting alice changed bob's runs: %#v", records)
	}
}
