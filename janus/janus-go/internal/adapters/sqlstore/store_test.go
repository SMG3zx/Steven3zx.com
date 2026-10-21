package sqlstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

const testSecret = "sqlstore-test-secret-of-some-length"

// openTest opens the database at path and closes it when the test ends.
func openTest(t *testing.T, path string) *Store {
	t.Helper()
	store, closeStore := openAt(t, path)
	t.Cleanup(closeStore)
	return store
}

// openAt opens the database at path. Closing it and opening the same path again
// simulates a restart: the next Open recovers interrupted jobs.
func openAt(t *testing.T, path string) (*Store, func()) {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return store, func() { _ = store.Close() }
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func seedAccountAndProject(t *testing.T, store *Store, owner, project string) {
	t.Helper()
	ctx := testContext(t)
	must(t, store.AddOwner(ctx, owner))
	_, err := store.CreateProject(ctx, app.CreateProject{ID: project, OwnerID: owner, Name: project})
	must(t, err)
}

// advanceImport moves an import job through the stages a worker passes before
// it completes its Snapshot.
func advanceImport(t *testing.T, store *Store, jobID string) {
	t.Helper()
	storetest.AdvanceImport(t, store, jobID, "queued", "cloning", "scanning", "storing")
}

// startImportAt creates an import job and moves it to status, as a worker would
// while it is running. The Staging Snapshot is begun at the storing stage.
func startImportAt(t *testing.T, store *Store, owner, project, job, status string) {
	t.Helper()
	ctx := testContext(t)
	commit := strings.Repeat("c", 40)
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: job, OwnerID: owner, ProjectID: project, Repository: "alice/repo", Branch: "main", Commit: commit}))
	var path []string
	for _, next := range []string{"cloning", "scanning", "storing"} {
		path = append(path, next)
		if next == status {
			break
		}
	}
	storetest.AdvanceImport(t, store, job, "queued", path...)
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: job + "-snapshot", JobID: job, OwnerID: owner, ProjectID: project, RepositoryID: 1, Repository: "alice/repo", Branch: "main", Commit: commit, MetadataJSON: "{}", Language: "Go"}))
}

func importStatus(t *testing.T, store *Store, owner, job string) app.ImportJob {
	t.Helper()
	found, ok, err := store.FindImportJob(testContext(t), job, owner)
	must(t, err)
	if !ok {
		t.Fatalf("import job %s not found", job)
	}
	return found
}

func TestMigratesPreActivationSnapshotSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("turso", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	legacySchema := `
CREATE TABLE users(id TEXT PRIMARY KEY,email TEXT NOT NULL DEFAULT '',display_name TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE projects(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE project_snapshots(
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, repository_id INTEGER NOT NULL,
 repository TEXT NOT NULL, branch TEXT NOT NULL, commit_sha TEXT NOT NULL,
 default_branch TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '', stars INTEGER NOT NULL DEFAULT 0,
 forks INTEGER NOT NULL DEFAULT 0, metadata_json TEXT NOT NULL DEFAULT '{}', entry_point TEXT NOT NULL DEFAULT '',
 file_count INTEGER NOT NULL DEFAULT 0, total_bytes INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'staging', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO users(id,email) VALUES('alice','alice@example.com');
INSERT INTO projects(id,owner_id,name) VALUES('project-legacy','alice','Legacy');
INSERT INTO project_snapshots(id,project_id,owner_id,repository_id,repository,branch,commit_sha,state,created_at)
 VALUES('snapshot-old','project-legacy','alice',1,'alice/repo','main','old','ready','2026-01-01 00:00:00.000');
INSERT INTO project_snapshots(id,project_id,owner_id,repository_id,repository,branch,commit_sha,state,created_at)
 VALUES('snapshot-new','project-legacy','alice',1,'alice/repo','main','new','ready','2026-01-02 00:00:00.000');`
	if _, err = db.Exec(legacySchema); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	store := openTest(t, databasePath)
	ctx := testContext(t)
	projects, err := store.ListProjects(ctx, "alice")
	if err != nil || len(projects) != 1 {
		t.Fatalf("list projects after schema migration: %#v, %v", projects, err)
	}
	if projects[0].SnapshotID != "snapshot-new" || projects[0].Commit != "new" {
		t.Fatalf("migration did not activate newest successful snapshot: %#v", projects[0])
	}
	snapshots, err := store.ListSnapshots(ctx, "alice", "project-legacy")
	if err != nil || len(snapshots) != 2 || !snapshots[0].Active || snapshots[1].Active {
		t.Fatalf("snapshot activation after migration: %#v, %v", snapshots, err)
	}
}

func TestMigrationAddsResumeColumnsToExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-jobs.db")
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `
CREATE TABLE users(id TEXT PRIMARY KEY,email TEXT NOT NULL DEFAULT '',display_name TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE projects(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE project_import_jobs(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,project_id TEXT NOT NULL,repository TEXT NOT NULL,branch TEXT NOT NULL,commit_sha TEXT NOT NULL,status TEXT NOT NULL,progress INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
INSERT INTO users(id) VALUES('alice');
INSERT INTO projects(id,owner_id,name) VALUES('p','alice','p');
INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status) VALUES('old-job','alice','p','alice/repo','main','abc','storing');`
	if _, err = db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	store := openTest(t, path)
	if job := importStatus(t, store, "alice", "old-job"); job.Status != "queued" {
		t.Fatalf("legacy running job should resume as queued, got %q", job.Status)
	}
}

func TestErrorsAreReplyErrorsExceptForTheContext(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "errors.db"))
	must(t, store.AddOwner(testContext(t), "x"))
	_, err := store.CreateProject(testContext(t), app.CreateProject{ID: "p", OwnerID: "x", Name: "P"})
	must(t, err)
	_, err = store.CreateProject(testContext(t), app.CreateProject{ID: "p", OwnerID: "x", Name: "Again"})
	if !app.IsReply(err) {
		t.Fatalf("a constraint violation = %v, want a reply error", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err = store.AddOwner(cancelled, "z")
	if err == nil || app.IsReply(err) {
		t.Fatalf("a cancelled context = %v, want the context's error and not a reply error", err)
	}
}

func TestProjectOwnershipSnapshotRetentionAndFailedImport(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "test.db"))
	ctx := testContext(t)
	for _, user := range []struct{ id, email string }{{"alice", "alice@example.com"}, {"bob", "bob@example.com"}} {
		must(t, store.AddOwner(ctx, user.id))
	}
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "project-1", OwnerID: "alice", Name: "Janus", Description: "source"})
	must(t, err)
	if _, found, err := store.GetProject(ctx, "project-1", "bob"); err != nil || found {
		t.Fatalf("project visible to another owner: %v, %v", found, err)
	}
	for i := 0; i < 6; i++ {
		jobID := "job-" + string(rune('a'+i))
		snapshotID := "snapshot-" + string(rune('a'+i))
		commit := strings.Repeat(string(rune('a'+i)), 40)
		must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: jobID, OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: commit}))
		must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: snapshotID, JobID: jobID, OwnerID: "alice", ProjectID: "project-1", RepositoryID: 42, Repository: "alice/repo", Branch: "main", Commit: commit, DefaultBranch: "main", MetadataJSON: `{"scan":{"entrypoint_candidates":[]}}`}))
		must(t, store.AddSnapshotFiles(ctx, snapshotID, []app.SnapshotFile{{Path: "main.go", Mode: "100644", Content: []byte("package main"), Hash: "hash"}}))
		advanceImport(t, store, jobID)
		must(t, store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: snapshotID, JobID: jobID, FileCount: 1, TotalBytes: 12}))
		time.Sleep(3 * time.Millisecond)
	}
	listed, err := store.ListProjects(ctx, "alice")
	if err != nil || len(listed) != 1 {
		t.Fatalf("project list: %#v, %v", listed, err)
	}
	if listed[0].SnapshotCount != 5 || listed[0].Commit != strings.Repeat("f", 40) {
		t.Fatalf("snapshot retention/latest selection wrong: %#v", listed[0])
	}
	activated, err := store.ActivateSnapshot(ctx, "alice", "project-1", "snapshot-d")
	if err != nil || !activated {
		t.Fatalf("activate retained snapshot: %v, %v", activated, err)
	}
	selected, found, err := store.GetProject(ctx, "project-1", "alice")
	if err != nil || !found || selected.Commit != strings.Repeat("d", 40) {
		t.Fatalf("project did not switch to activated snapshot: %#v, %v, %v", selected, found, err)
	}
	retained, err := store.ListSnapshots(ctx, "alice", "project-1")
	if err != nil || len(retained) != 5 {
		t.Fatalf("activating an older snapshot changed retention: %#v, %v", retained, err)
	}
	activeCount := 0
	for _, snapshot := range retained {
		if snapshot.Active {
			activeCount++
			if snapshot.ID != "snapshot-d" {
				t.Fatalf("unexpected active snapshot: %#v", snapshot)
			}
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active snapshot, got %d", activeCount)
	}
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: "job-failed", OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: strings.Repeat("0", 40)}))
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: "snapshot-failed", JobID: "job-failed", OwnerID: "alice", ProjectID: "project-1", RepositoryID: 42, Repository: "alice/repo", Branch: "main", Commit: strings.Repeat("0", 40), MetadataJSON: "{}"}))
	must(t, store.DeleteStagingSnapshot(ctx, "snapshot-failed"))
	must(t, store.TransitionImportJob(ctx, app.JobTransition{ID: "job-failed", From: "queued", To: "failed", Progress: 100, Error: "clone failed"}))
	latest, found, err := store.GetProject(ctx, "project-1", "alice")
	if err != nil || !found || latest.Commit != strings.Repeat("d", 40) || latest.SnapshotCount != 5 {
		t.Fatalf("failed import damaged previous snapshot: %#v, %v, %v", latest, found, err)
	}
}

func TestProjectFilePagingAndWASMBuildArtifactOwnership(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "test.db"))
	ctx := testContext(t)
	for _, user := range []struct{ id, email string }{{"alice", "alice@example.com"}, {"bob", "bob@example.com"}} {
		must(t, store.AddOwner(ctx, user.id))
	}
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "wasm-project", OwnerID: "alice", Name: "WASM"})
	must(t, err)
	commit := strings.Repeat("a", 40)
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: "wasm-import", OwnerID: "alice", ProjectID: "wasm-project", Repository: "alice/rust", Branch: "main", Commit: commit}))
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: "wasm-snapshot", JobID: "wasm-import", OwnerID: "alice", ProjectID: "wasm-project", Repository: "alice/rust", Branch: "main", Commit: commit, MetadataJSON: "{}"}))
	files := []app.SnapshotFile{{Path: "Cargo.toml", Content: []byte("manifest")}, {Path: "src/lib.rs", Content: []byte("library")}, {Path: "README.md", Content: []byte("readme")}}
	must(t, store.AddSnapshotFiles(ctx, "wasm-snapshot", files))
	advanceImport(t, store, "wasm-import")
	must(t, store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: "wasm-snapshot", JobID: "wasm-import", FileCount: 3, TotalBytes: 23}))
	first, err := store.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "alice", ProjectID: "wasm-project", SnapshotID: "wasm-snapshot", Offset: 0, Limit: 2})
	must(t, err)
	second, err := store.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "alice", ProjectID: "wasm-project", SnapshotID: "wasm-snapshot", Offset: 2, Limit: 2})
	must(t, err)
	if len(first) != 2 || len(second) != 1 || second[0].Path != "src/lib.rs" {
		t.Fatalf("paged project files: first=%#v second=%#v", first, second)
	}
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "wasm-job", OwnerID: "alice", ProjectID: "wasm-project", SnapshotID: "wasm-snapshot", Target: "wasm32-unknown-unknown"}))
	if err = store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "wasm-job-2", OwnerID: "alice", ProjectID: "wasm-project", SnapshotID: "wasm-snapshot", Target: "wasm32-wasip1"}); err == nil {
		t.Fatal("more than one active build was allowed for a project")
	}
	must(t, store.CompleteWASMBuild(ctx, app.CompleteWASMBuild{ID: "wasm-job", ArtifactName: "wasm-artifacts.zip", Artifact: []byte("artifact"), Log: []byte("build log")}))
	build, found, err := store.FindWASMBuild(ctx, "wasm-job", "alice")
	if err != nil || !found || build.Status != "complete" || build.Target != "wasm32-unknown-unknown" || build.Log != "build log" {
		t.Fatalf("stored WASM build = %#v, %v, %v", build, found, err)
	}
	stored, err := store.FindWASMBuildArtifact(ctx, "wasm-job", "alice")
	if err != nil || !stored.Found || string(stored.Data) != "artifact" {
		t.Fatalf("stored WASM artifact = %#v, %v", stored, err)
	}
	if _, found, err = store.FindWASMBuild(ctx, "wasm-job", "bob"); err != nil || found {
		t.Fatalf("another owner's WASM build was visible: %v, %v", found, err)
	}
}

func TestInterruptedImportResumesUpToThreeTimesThenFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.db")
	ctx := testContext(t)

	store, stop := openAt(t, path)
	seedAccountAndProject(t, store, "alice", "project-1")
	startImportAt(t, store, "alice", "project-1", "job-1", "storing")
	stop()

	for restart := 1; restart <= app.MaxResumes; restart++ {
		store, stop = openAt(t, path)
		job := importStatus(t, store, "alice", "job-1")
		if job.Status != "queued" {
			t.Fatalf("restart %d: status = %q, want queued", restart, job.Status)
		}
		staging, err := store.FindStagingSnapshot(ctx, "job-1")
		must(t, err)
		if !staging.Found {
			t.Fatalf("restart %d: staging snapshot was discarded before the resume limit", restart)
		}
		listed, err := store.ListResumableImports(ctx)
		must(t, err)
		if len(listed) != 1 || listed[0].JobID != "job-1" {
			t.Fatalf("restart %d: resumable jobs = %#v", restart, listed)
		}
		// The resumed worker runs again and is interrupted by the next restart.
		storetest.AdvanceImport(t, store, "job-1", "queued", "cloning", "scanning", "storing")
		stop()
	}

	store, stop = openAt(t, path)
	defer stop()
	job := importStatus(t, store, "alice", "job-1")
	if job.Status != "failed" || !strings.Contains(job.Error, "resume limit") {
		t.Fatalf("after the fourth interruption: status=%q error=%q", job.Status, job.Error)
	}
	staging, err := store.FindStagingSnapshot(ctx, "job-1")
	must(t, err)
	if staging.Found {
		t.Fatal("failed import kept its staging snapshot")
	}
}

func TestTerminalJobStatusCannotChange(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "terminal.db"))
	ctx := testContext(t)
	seedAccountAndProject(t, store, "alice", "project-1")
	startImportAt(t, store, "alice", "project-1", "job-1", "storing")
	must(t, store.TransitionImportJob(ctx, app.JobTransition{ID: "job-1", From: "storing", To: "failed", Progress: 100, Error: "clone failed"}))

	err := store.TransitionImportJob(ctx, app.JobTransition{ID: "job-1", From: "failed", To: "cloning", Progress: 15})
	if err == nil || !strings.Contains(err.Error(), "cannot move from failed to cloning") {
		t.Fatalf("expected a rejected transition from failed, got %v", err)
	}
	job := importStatus(t, store, "alice", "job-1")
	if job.Status != "failed" || job.Error != "clone failed" {
		t.Fatalf("failed job changed: status=%q error=%q", job.Status, job.Error)
	}
}

func TestFailedImportDiscardsOnlyItsStagingSnapshot(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "discard.db"))
	ctx := testContext(t)
	seedAccountAndProject(t, store, "alice", "project-1")
	startImportAt(t, store, "alice", "project-1", "job-1", "storing")
	must(t, store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: "job-1-snapshot", JobID: "job-1"}))
	startImportAt(t, store, "alice", "project-1", "job-2", "storing")

	must(t, store.DiscardJobStaging(ctx, "job-2"))

	if found, err := store.FindStagingSnapshot(ctx, "job-2"); err != nil || found.Found {
		t.Fatalf("failed import's staging snapshot still exists: %#v, %v", found, err)
	}
	listed, err := store.ListSnapshots(ctx, "alice", "project-1")
	if err != nil || len(listed) != 1 || listed[0].ID != "job-1-snapshot" || !listed[0].Active {
		t.Fatalf("active snapshot changed by a failed import: %#v, %v", listed, err)
	}
}

func TestDeleteProjectRemovesSnapshotsBuildsAndArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-delete.db")
	store, stop := openAt(t, path)
	ctx := testContext(t)
	seedAccountAndProject(t, store, "alice", "project-1")
	startImportAt(t, store, "alice", "project-1", "job-1", "storing")
	must(t, store.AddSnapshotFiles(ctx, "job-1-snapshot", []app.SnapshotFile{{Path: "main.go", Mode: "100644", Content: []byte("package main"), Hash: "h1"}}))
	must(t, store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: "job-1-snapshot", JobID: "job-1", FileCount: 1, TotalBytes: 12}))
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "build-1", OwnerID: "alice", ProjectID: "project-1", SnapshotID: "job-1-snapshot", Target: "wasip1-wasm"}))
	must(t, store.CompleteWASMBuild(ctx, app.CompleteWASMBuild{ID: "build-1", ArtifactName: "app.wasm.zst", Artifact: []byte("artifact"), Log: []byte("ok")}))

	deleted, err := store.DeleteProject(ctx, "project-1", "alice")
	if err != nil || !deleted {
		t.Fatalf("delete project: deleted=%v err=%v", deleted, err)
	}
	for _, table := range []string{"project_snapshots", "project_files", "project_wasm_builds", "project_import_jobs"} {
		var count int
		if err = store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s still has %d rows after deleting the project", table, count)
		}
	}
	stop()
}

func TestDeleteAccountIsBlockedByActiveJobsUntilCancelled(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "account-delete.db"))
	ctx := testContext(t)
	seedAccountAndProject(t, store, "bob", "bob-project")
	startImportAt(t, store, "bob", "bob-project", "bob-job", "cloning")
	blocked, err := store.DeleteAccount(ctx, "bob", false)
	if err != nil || blocked.Deleted || len(blocked.ActiveJobs) != 1 || blocked.ActiveJobs[0].ID != "bob-job" {
		t.Fatalf("expected a blocked deletion listing bob-job, got %#v, %v", blocked, err)
	}
	if _, found, err := store.GetProject(ctx, "bob-project", "bob"); err != nil || !found {
		t.Fatal("blocked deletion removed the account's project")
	}

	deleted, err := store.DeleteAccount(ctx, "bob", true)
	if err != nil || !deleted.Deleted {
		t.Fatalf("cancelling deletion failed: %#v, %v", deleted, err)
	}
	if again, _ := store.DeleteAccount(ctx, "bob", false); again.Deleted {
		t.Fatal("the Owner still exists after deletion")
	}
	if _, found, _ := store.GetProject(ctx, "bob-project", "bob"); found {
		t.Fatal("project survived account deletion")
	}
	if _, found, _ := store.FindImportJob(ctx, "bob-job", "bob"); found {
		t.Fatal("import job survived account deletion")
	}
}

func TestDeleteAccountLeavesOtherAccountsUntouched(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "isolation.db"))
	ctx := testContext(t)
	seedAccountAndProject(t, store, "erin", "erin-project")
	seedAccountAndProject(t, store, "frank", "frank-project")

	if deleted, err := store.DeleteAccount(ctx, "frank", false); err != nil || !deleted.Deleted {
		t.Fatalf("delete account: %#v, %v", deleted, err)
	}
	if _, found, err := store.GetProject(ctx, "erin-project", "erin"); err != nil || !found {
		t.Fatal("deleting one account removed another account's project")
	}
}

func TestSnapshotStoresRuntimeAndReturnsItToReaders(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "runtime.db"))
	ctx := testContext(t)
	seedAccountAndProject(t, store, "ivy", "ivy-project")
	commit := strings.Repeat("d", 40)
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: "ivy-job", OwnerID: "ivy", ProjectID: "ivy-project", Repository: "ivy/app", Branch: "main", Commit: commit}))
	advanceImport(t, store, "ivy-job")
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: "ivy-snapshot", JobID: "ivy-job", OwnerID: "ivy", ProjectID: "ivy-project", RepositoryID: 1, Repository: "ivy/app", Branch: "main", Commit: commit, MetadataJSON: "{}", Language: "JavaScript/TypeScript", Runtime: "Bun"}))
	must(t, store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: "ivy-snapshot", JobID: "ivy-job", FileCount: 1, TotalBytes: 3}))

	project, found, err := store.GetProject(ctx, "ivy-project", "ivy")
	if err != nil || !found || project.Language != "JavaScript/TypeScript" || project.Runtime != "Bun" {
		t.Fatalf("project did not return its language and runtime: %#v, %v, %v", project, found, err)
	}
	listed, err := store.ListSnapshots(ctx, "ivy", "ivy-project")
	if err != nil || len(listed) != 1 || listed[0].Runtime != "Bun" {
		t.Fatalf("snapshot list did not return the runtime: %#v, %v", listed, err)
	}
}
