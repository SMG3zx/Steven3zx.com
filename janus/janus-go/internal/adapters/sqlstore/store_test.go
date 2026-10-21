package sqlstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

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

// seedSnapshot records an Owner, a Project and a ready Go Snapshot of it.
func seedSnapshot(t *testing.T, store *Store, owner, project, snapshot string) {
	t.Helper()
	storetest.ReadySnapshot(t, store, owner, project, snapshot, "Go")
}

// startBuildAt records a WASM Build of snapshot in status, as the builds service
// publishes it.
func startBuildAt(t *testing.T, store *Store, owner, project, snapshot, build, status string) {
	t.Helper()
	must(t, store.ApplyWASMBuild(testContext(t), app.WASMBuildChange{ID: build, OwnerID: owner, ProjectID: project, SnapshotID: snapshot, Target: "wasip1-wasm", Status: status}))
}

func buildStatus(t *testing.T, store *Store, owner, build string) app.WASMBuild {
	t.Helper()
	found, ok, err := store.FindWASMBuild(testContext(t), build, owner)
	must(t, err)
	if !ok {
		t.Fatalf("WASM build %s not found", build)
	}
	return found
}

// legacySchema is a janus.db written before the projects service existed.
const legacySchema = `
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
CREATE TABLE project_import_jobs(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,project_id TEXT NOT NULL,repository TEXT NOT NULL,branch TEXT NOT NULL,commit_sha TEXT NOT NULL,status TEXT NOT NULL,progress INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
INSERT INTO users(id,email) VALUES('alice','alice@example.com');
INSERT INTO projects(id,owner_id,name) VALUES('project-legacy','alice','Legacy');
INSERT INTO project_snapshots(id,project_id,owner_id,repository_id,repository,branch,commit_sha,language,state)
 VALUES('snapshot-legacy','project-legacy','alice',1,'alice/repo','main','old','Rust','ready');
INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status) VALUES('old-job','alice','project-legacy','alice/repo','main','abc','storing');`

// TestADatabaseFromBeforeTheSplitOpens opens a janus.db written before the
// projects service existed: its Projects and Snapshots are references now, its
// old Snapshots gain is_active, and its import jobs are left alone, as they are
// the projects service's after `janus migrate split`.
func TestADatabaseFromBeforeTheSplitOpens(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("turso", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(legacySchema); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	store := openTest(t, databasePath)
	ctx := testContext(t)
	if projects, err := store.ListOwnedProjects(ctx, "alice"); err != nil || len(projects) != 1 || projects[0] != "project-legacy" {
		t.Fatalf("legacy projects = %v, %v", projects, err)
	}
	snapshot, found, err := store.FindSnapshot(ctx, "snapshot-legacy", "alice")
	if err != nil || !found || snapshot.Language != "Rust" || snapshot.ProjectID != "project-legacy" {
		t.Fatalf("legacy snapshot = %#v, %v, %v", snapshot, found, err)
	}
	var active int
	if err := store.db.QueryRow(`SELECT is_active FROM project_snapshots WHERE id='snapshot-legacy'`).Scan(&active); err != nil || active != 1 {
		t.Fatalf("is_active after migration = %d, %v", active, err)
	}
	var status string
	if err := store.db.QueryRow(`SELECT status FROM project_import_jobs WHERE id='old-job'`).Scan(&status); err != nil || status != "storing" {
		t.Fatalf("legacy import job = %q, %v; want it left for the projects service", status, err)
	}
	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b", OwnerID: "alice", ProjectID: "project-legacy", SnapshotID: "snapshot-legacy", Target: "wasip1-wasm", Status: "queued"}))
}

func TestErrorsAreReplyErrorsExceptForTheContext(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "errors.db"))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err := store.AddOwner(cancelled, "z")
	if err == nil || app.IsReply(err) {
		t.Fatalf("a cancelled context = %v, want the context's error and not a reply error", err)
	}
}

func TestRemovingAProjectRemovesItsSnapshotsAndBuilds(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "project-delete.db"))
	ctx := testContext(t)
	seedSnapshot(t, store, "alice", "project-1", "snapshot-1")
	startBuildAt(t, store, "alice", "project-1", "snapshot-1", "build-1", "queued")
	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "build-1", OwnerID: "alice", ProjectID: "project-1", SnapshotID: "snapshot-1", Target: "wasip1-wasm", Status: "complete"}))

	must(t, store.RemoveProject(ctx, "project-1"))
	for _, table := range []string{"projects", "project_snapshots", "project_wasm_builds"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s still has %d rows after removing the project", table, count)
		}
	}
}

func TestDeleteAccountIsBlockedByActiveBuildsUntilCancelled(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "account-delete.db"))
	ctx := testContext(t)
	seedSnapshot(t, store, "bob", "bob-project", "bob-snapshot")
	startBuildAt(t, store, "bob", "bob-project", "bob-snapshot", "bob-build", "building")
	blocked, err := store.DeleteAccount(ctx, "bob", false)
	if err != nil || blocked.Deleted || len(blocked.ActiveJobs) != 1 || blocked.ActiveJobs[0].ID != "bob-build" {
		t.Fatalf("expected a blocked deletion listing bob-build, got %#v, %v", blocked, err)
	}
	if projects, _ := store.ListOwnedProjects(ctx, "bob"); len(projects) != 1 {
		t.Fatal("blocked deletion removed the account's project")
	}

	deleted, err := store.DeleteAccount(ctx, "bob", true)
	if err != nil || !deleted.Deleted {
		t.Fatalf("cancelling deletion failed: %#v, %v", deleted, err)
	}
	if again, _ := store.DeleteAccount(ctx, "bob", false); again.Deleted {
		t.Fatal("the Owner still exists after deletion")
	}
	if projects, _ := store.ListOwnedProjects(ctx, "bob"); len(projects) != 0 {
		t.Fatal("project survived account deletion")
	}
	if _, found, _ := store.FindWASMBuild(ctx, "bob-build", "bob"); found {
		t.Fatal("build survived account deletion")
	}
}

func TestDeleteAccountLeavesOtherAccountsUntouched(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "isolation.db"))
	ctx := testContext(t)
	seedSnapshot(t, store, "erin", "erin-project", "erin-snapshot")
	seedSnapshot(t, store, "frank", "frank-project", "frank-snapshot")

	if deleted, err := store.DeleteAccount(ctx, "frank", false); err != nil || !deleted.Deleted {
		t.Fatalf("delete account: %#v, %v", deleted, err)
	}
	if projects, err := store.ListOwnedProjects(ctx, "erin"); err != nil || len(projects) != 1 {
		t.Fatal("deleting one account removed another account's project")
	}
}

func TestASnapshotReferenceKeepsItsRuntime(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "runtime.db"))
	ctx := testContext(t)
	must(t, store.AddOwner(ctx, "ivy"))
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "ivy-project", OwnerID: "ivy"}))
	must(t, store.AddSnapshot(ctx, app.SnapshotRef{ID: "ivy-snapshot", ProjectID: "ivy-project", OwnerID: "ivy", Language: "JavaScript/TypeScript", Runtime: "Bun"}))
	snapshot, found, err := store.FindSnapshot(ctx, "ivy-snapshot", "ivy")
	if err != nil || !found || snapshot.Language != "JavaScript/TypeScript" || snapshot.Runtime != "Bun" {
		t.Fatalf("snapshot did not return its language and runtime: %#v, %v, %v", snapshot, found, err)
	}
}
