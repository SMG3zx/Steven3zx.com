package persistence

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"
	_ "turso.tech/database/tursogo"
)

// startPersistence opens the database at path in a fresh actor system, which is
// how a restart is simulated: the next call recovers interrupted jobs again.
func startPersistence(t *testing.T, path string) (*goakt.PID, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	system, err := goakt.NewActorSystem("persistence-jobs-test", goakt.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	pid, err := system.Spawn(ctx, "persistence", New(path, strings.Repeat("s", 32)))
	if err != nil {
		_ = system.Stop(ctx)
		t.Fatal(err)
	}
	stop := func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_, _ = goakt.Ask(closeCtx, pid, &CloseDatabase{}, 5*time.Second)
		_ = system.Stop(closeCtx)
	}
	return pid, stop
}

func seedAccountAndProject(t *testing.T, ctx context.Context, pid *goakt.PID, owner, project string) {
	t.Helper()
	askPersistence(t, ctx, pid, &CreateLocalUser{ID: owner, Email: owner + "@example.com", PasswordHash: "hash"})
	askPersistence(t, ctx, pid, &CreateProject{ID: project, OwnerID: owner, Name: project})
}

// startImportAt creates an import job and moves it to status, as a worker would
// while it is running. The Staging Snapshot is begun at the storing stage.
func startImportAt(t *testing.T, ctx context.Context, pid *goakt.PID, owner, project, job, status string) {
	t.Helper()
	askPersistence(t, ctx, pid, &CreateImportJob{ID: job, OwnerID: owner, ProjectID: project, Repository: "alice/repo", Branch: "main", Commit: strings.Repeat("c", 40)})
	for _, next := range []string{"cloning", "scanning", "storing"} {
		askPersistence(t, ctx, pid, &UpdateImportJob{ID: job, Status: next, Progress: 50})
		if next == status {
			break
		}
	}
	askPersistence(t, ctx, pid, &BeginSnapshot{ID: job + "-snapshot", JobID: job, OwnerID: owner, ProjectID: project, RepositoryID: 1, Repository: "alice/repo", Branch: "main", Commit: strings.Repeat("c", 40), MetadataJSON: "{}", Language: "Go"})
}

func importStatus(t *testing.T, ctx context.Context, pid *goakt.PID, owner, job string) ImportJob {
	t.Helper()
	found := askPersistence(t, ctx, pid, &FindImportJob{ID: job, OwnerID: owner}).(*ImportJobFound)
	if !found.Found {
		t.Fatalf("import job %s not found", job)
	}
	return found.Job
}

func TestInterruptedImportResumesUpToThreeTimesThenFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "resume.db")

	pid, stop := startPersistence(t, path)
	seedAccountAndProject(t, ctx, pid, "alice", "project-1")
	startImportAt(t, ctx, pid, "alice", "project-1", "job-1", "storing")
	stop()

	for restart := 1; restart <= MaxResumes; restart++ {
		pid, stop = startPersistence(t, path)
		job := importStatus(t, ctx, pid, "alice", "job-1")
		if job.Status != "queued" {
			t.Fatalf("restart %d: status = %q, want queued", restart, job.Status)
		}
		staging := askPersistence(t, ctx, pid, &FindStagingSnapshot{JobID: "job-1"}).(*StagingSnapshotFound)
		if !staging.Found {
			t.Fatalf("restart %d: staging snapshot was discarded before the resume limit", restart)
		}
		listed := askPersistence(t, ctx, pid, &ListResumableImports{}).(*ResumableImportsListed)
		if len(listed.Jobs) != 1 || listed.Jobs[0].JobID != "job-1" {
			t.Fatalf("restart %d: resumable jobs = %#v", restart, listed.Jobs)
		}
		// The resumed worker runs again and is interrupted by the next restart.
		askPersistence(t, ctx, pid, &UpdateImportJob{ID: "job-1", Status: "cloning", Progress: 15})
		askPersistence(t, ctx, pid, &UpdateImportJob{ID: "job-1", Status: "scanning", Progress: 45})
		askPersistence(t, ctx, pid, &UpdateImportJob{ID: "job-1", Status: "storing", Progress: 70})
		stop()
	}

	pid, stop = startPersistence(t, path)
	defer stop()
	job := importStatus(t, ctx, pid, "alice", "job-1")
	if job.Status != "failed" || !strings.Contains(job.Error, "resume limit") {
		t.Fatalf("after the fourth interruption: status=%q error=%q", job.Status, job.Error)
	}
	staging := askPersistence(t, ctx, pid, &FindStagingSnapshot{JobID: "job-1"}).(*StagingSnapshotFound)
	if staging.Found {
		t.Fatal("failed import kept its staging snapshot")
	}
}

func TestTerminalJobStatusCannotChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pid, stop := startPersistence(t, filepath.Join(t.TempDir(), "terminal.db"))
	defer stop()
	seedAccountAndProject(t, ctx, pid, "alice", "project-1")
	startImportAt(t, ctx, pid, "alice", "project-1", "job-1", "storing")
	askPersistence(t, ctx, pid, &UpdateImportJob{ID: "job-1", Status: "failed", Progress: 100, Error: "clone failed"})

	result := askPersistence(t, ctx, pid, &UpdateImportJob{ID: "job-1", Status: "cloning", Progress: 15}).(*ImportJobCreated)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "cannot move from failed to cloning") {
		t.Fatalf("expected a rejected transition from failed, got %v", result.Err)
	}
	job := importStatus(t, ctx, pid, "alice", "job-1")
	if job.Status != "failed" || job.Error != "clone failed" {
		t.Fatalf("failed job changed: status=%q error=%q", job.Status, job.Error)
	}
}

func TestFailedImportDiscardsOnlyItsStagingSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pid, stop := startPersistence(t, filepath.Join(t.TempDir(), "discard.db"))
	defer stop()
	seedAccountAndProject(t, ctx, pid, "alice", "project-1")
	startImportAt(t, ctx, pid, "alice", "project-1", "job-1", "storing")
	askPersistence(t, ctx, pid, &CompleteSnapshot{SnapshotID: "job-1-snapshot", JobID: "job-1", FileCount: 0, TotalBytes: 0})
	startImportAt(t, ctx, pid, "alice", "project-1", "job-2", "storing")

	askPersistence(t, ctx, pid, &DiscardJobStaging{JobID: "job-2"})

	if found := askPersistence(t, ctx, pid, &FindStagingSnapshot{JobID: "job-2"}).(*StagingSnapshotFound); found.Found {
		t.Fatal("failed import's staging snapshot still exists")
	}
	listed := askPersistence(t, ctx, pid, &ListSnapshots{OwnerID: "alice", ProjectID: "project-1"}).(*SnapshotsListed)
	if len(listed.Snapshots) != 1 || listed.Snapshots[0].ID != "job-1-snapshot" || !listed.Snapshots[0].Active {
		t.Fatalf("active snapshot changed by a failed import: %#v", listed.Snapshots)
	}
}

func TestDeleteProjectRemovesSnapshotsBuildsAndArtifacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "project-delete.db")
	pid, stop := startPersistence(t, path)
	seedAccountAndProject(t, ctx, pid, "alice", "project-1")
	startImportAt(t, ctx, pid, "alice", "project-1", "job-1", "storing")
	askPersistence(t, ctx, pid, &AddSnapshotFiles{SnapshotID: "job-1-snapshot", Files: []SnapshotFile{{Path: "main.go", Mode: "100644", Content: []byte("package main"), Hash: "h1"}}})
	askPersistence(t, ctx, pid, &CompleteSnapshot{SnapshotID: "job-1-snapshot", JobID: "job-1", FileCount: 1, TotalBytes: 12})
	askPersistence(t, ctx, pid, &CreateWASMBuild{ID: "build-1", OwnerID: "alice", ProjectID: "project-1", SnapshotID: "job-1-snapshot", Target: "wasip1-wasm"})
	askPersistence(t, ctx, pid, &CompleteWASMBuild{ID: "build-1", ArtifactName: "app.wasm.zst", Artifact: []byte("artifact"), Log: []byte("ok")})

	deleted := askPersistence(t, ctx, pid, &DeleteProject{ID: "project-1", OwnerID: "alice"}).(*ProjectDeleted)
	if deleted.Err != nil || !deleted.Deleted {
		t.Fatalf("delete project: deleted=%v err=%v", deleted.Deleted, deleted.Err)
	}
	stop()

	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"project_snapshots", "project_files", "project_wasm_builds", "project_import_jobs"} {
		var count int
		if err = db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s still has %d rows after deleting the project", table, count)
		}
	}
}

func TestDeleteAccountIsBlockedByActiveJobsUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pid, stop := startPersistence(t, filepath.Join(t.TempDir(), "account-delete.db"))
	defer stop()
	seedAccountAndProject(t, ctx, pid, "bob", "bob-project")
	startImportAt(t, ctx, pid, "bob", "bob-project", "bob-job", "cloning")
	blocked := askPersistence(t, ctx, pid, &DeleteAccount{UserID: "bob"}).(*AccountDeleted)
	if blocked.Err != nil || blocked.Deleted || len(blocked.ActiveJobs) != 1 || blocked.ActiveJobs[0].ID != "bob-job" {
		t.Fatalf("expected a blocked deletion listing bob-job, got %#v", blocked)
	}
	if found := askPersistence(t, ctx, pid, &FindUser{ID: "bob"}).(*UserFound); !found.Found {
		t.Fatal("blocked deletion removed the account")
	}

	deleted := askPersistence(t, ctx, pid, &DeleteAccount{UserID: "bob", CancelActiveJobs: true}).(*AccountDeleted)
	if deleted.Err != nil || !deleted.Deleted {
		t.Fatalf("cancelling deletion failed: %#v", deleted)
	}
	if found := askPersistence(t, ctx, pid, &FindUser{ID: "bob"}).(*UserFound); found.Found {
		t.Fatal("account still exists after deletion")
	}
	if project := askPersistence(t, ctx, pid, &GetProject{ID: "bob-project", OwnerID: "bob"}).(*ProjectFound); project.Found {
		t.Fatal("project survived account deletion")
	}
	if job := askPersistence(t, ctx, pid, &FindImportJob{ID: "bob-job", OwnerID: "bob"}).(*ImportJobFound); job.Found {
		t.Fatal("import job survived account deletion")
	}
}

func TestDeleteAccountRemovesProviderIdentities(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "identities.db")
	pid, stop := startPersistence(t, path)
	linked := askPersistence(t, ctx, pid, &ResolveGoogleIdentity{Subject: "google-carol", Email: "carol@example.com", DisplayName: "Carol"}).(*GoogleIdentityResolved)
	if linked.ID == "" {
		t.Fatal("expected a Google identity to create an account")
	}

	deleted := askPersistence(t, ctx, pid, &DeleteAccount{UserID: linked.ID}).(*AccountDeleted)
	if !deleted.Deleted {
		t.Fatalf("delete account: %#v", deleted)
	}
	stop()

	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM oauth_identities WHERE subject='google-carol'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("deleted account still has %d provider identity rows", count)
	}
}

func TestDeleteAccountRemovesGitHubConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pid, stop := startPersistence(t, filepath.Join(t.TempDir(), "github-delete.db"))
	defer stop()
	seedAccountAndProject(t, ctx, pid, "dana", "dana-project")
	connected := askPersistence(t, ctx, pid, &ConnectGitHub{UserID: "dana", Subject: "github-dana", Email: "dana@example.com", DisplayName: "Dana", Token: "gho-dana-token"}).(*GitHubConnected)
	if connected.Err != nil {
		t.Fatalf("connect GitHub: %v", connected.Err)
	}

	deleted := askPersistence(t, ctx, pid, &DeleteAccount{UserID: "dana"}).(*AccountDeleted)
	if deleted.Err != nil || !deleted.Deleted {
		t.Fatalf("delete account: %#v", deleted)
	}
	found := askPersistence(t, ctx, pid, &FindGitHubToken{UserID: "dana"}).(*GitHubTokenFound)
	if found.Connected || found.Token != "" {
		t.Fatalf("deleted account still has a GitHub connection: %#v", found)
	}
}

func TestDeleteAccountLeavesOtherAccountsUntouched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pid, stop := startPersistence(t, filepath.Join(t.TempDir(), "isolation.db"))
	defer stop()
	seedAccountAndProject(t, ctx, pid, "erin", "erin-project")
	seedAccountAndProject(t, ctx, pid, "frank", "frank-project")

	deleted := askPersistence(t, ctx, pid, &DeleteAccount{UserID: "frank"}).(*AccountDeleted)
	if deleted.Err != nil || !deleted.Deleted {
		t.Fatalf("delete account: %#v", deleted)
	}
	if found := askPersistence(t, ctx, pid, &FindUser{ID: "erin"}).(*UserFound); !found.Found {
		t.Fatal("deleting one account removed another account")
	}
	if project := askPersistence(t, ctx, pid, &GetProject{ID: "erin-project", OwnerID: "erin"}).(*ProjectFound); !project.Found {
		t.Fatal("deleting one account removed another account's project")
	}
}

func TestDeleteAccountLeavesOtherAccountsProviderIdentitiesUntouched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pid, stop := startPersistence(t, filepath.Join(t.TempDir(), "identity-isolation.db"))
	defer stop()
	kept := askPersistence(t, ctx, pid, &ResolveGoogleIdentity{Subject: "google-gina", Email: "gina@example.com", DisplayName: "Gina"}).(*GoogleIdentityResolved)
	doomed := askPersistence(t, ctx, pid, &ResolveGoogleIdentity{Subject: "google-hal", Email: "hal@example.com", DisplayName: "Hal"}).(*GoogleIdentityResolved)
	if kept.ID == "" || doomed.ID == "" || kept.ID == doomed.ID {
		t.Fatalf("expected two separate accounts, got %#v and %#v", kept, doomed)
	}

	deleted := askPersistence(t, ctx, pid, &DeleteAccount{UserID: doomed.ID}).(*AccountDeleted)
	if !deleted.Deleted {
		t.Fatalf("delete account: %#v", deleted)
	}
	again := askPersistence(t, ctx, pid, &ResolveGoogleIdentity{Subject: "google-gina", Email: "gina@example.com", DisplayName: "Gina"}).(*GoogleIdentityResolved)
	if again.ID != kept.ID {
		t.Fatalf("another account's identity changed after deletion: got %q, want %q", again.ID, kept.ID)
	}
}

func TestSnapshotStoresRuntimeAndReturnsItToReaders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pid, stop := startPersistence(t, filepath.Join(t.TempDir(), "runtime.db"))
	defer stop()
	seedAccountAndProject(t, ctx, pid, "ivy", "ivy-project")
	commit := strings.Repeat("d", 40)
	askPersistence(t, ctx, pid, &CreateImportJob{ID: "ivy-job", OwnerID: "ivy", ProjectID: "ivy-project", Repository: "ivy/app", Branch: "main", Commit: commit})
	for _, next := range []string{"cloning", "scanning", "storing"} {
		askPersistence(t, ctx, pid, &UpdateImportJob{ID: "ivy-job", Status: next, Progress: 50})
	}
	askPersistence(t, ctx, pid, &BeginSnapshot{ID: "ivy-snapshot", JobID: "ivy-job", OwnerID: "ivy", ProjectID: "ivy-project", RepositoryID: 1, Repository: "ivy/app", Branch: "main", Commit: commit, MetadataJSON: "{}", Language: "JavaScript/TypeScript", Runtime: "Bun"})
	askPersistence(t, ctx, pid, &CompleteSnapshot{SnapshotID: "ivy-snapshot", JobID: "ivy-job", FileCount: 1, TotalBytes: 3})

	project := askPersistence(t, ctx, pid, &GetProject{ID: "ivy-project", OwnerID: "ivy"}).(*ProjectFound)
	if !project.Found || project.Project.Language != "JavaScript/TypeScript" || project.Project.Runtime != "Bun" {
		t.Fatalf("project did not return its language and runtime: %#v", project.Project)
	}
	listed := askPersistence(t, ctx, pid, &ListSnapshots{OwnerID: "ivy", ProjectID: "ivy-project"}).(*SnapshotsListed)
	if listed.Err != nil || len(listed.Snapshots) != 1 || listed.Snapshots[0].Runtime != "Bun" {
		t.Fatalf("snapshot list did not return the runtime: %#v", listed)
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

	pid, stop := startPersistence(t, path)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	job := importStatus(t, ctx, pid, "alice", "old-job")
	if job.Status != "queued" {
		t.Fatalf("legacy running job should resume as queued, got %q", job.Status)
	}
}
