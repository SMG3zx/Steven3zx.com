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
)

func TestPersistenceMigratesPreActivationSnapshotSchema(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	system, err := goakt.NewActorSystem("persistence-legacy-migration-test", goakt.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = system.Stop(stop)
	})
	pid, err := system.Spawn(ctx, "persistence", New(databasePath, "test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	listed := askPersistence(t, ctx, pid, &ListProjects{OwnerID: "alice"}).(*ProjectsListed)
	if listed.Err != nil || len(listed.Projects) != 1 {
		t.Fatalf("list projects after schema migration: %#v", listed)
	}
	if listed.Projects[0].SnapshotID != "snapshot-new" || listed.Projects[0].Commit != "new" {
		t.Fatalf("migration did not activate newest successful snapshot: %#v", listed.Projects[0])
	}
	snapshots := askPersistence(t, ctx, pid, &ListSnapshots{OwnerID: "alice", ProjectID: "project-legacy"}).(*SnapshotsListed)
	if snapshots.Err != nil || len(snapshots.Snapshots) != 2 || !snapshots.Snapshots[0].Active || snapshots.Snapshots[1].Active {
		t.Fatalf("snapshot activation after migration: %#v", snapshots)
	}
}

func newPersistenceTest(t *testing.T, secret string) (context.Context, context.CancelFunc, goakt.ActorSystem, *goakt.PID, *Actor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	system, err := goakt.NewActorSystem("persistence-project-test", goakt.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	instance := New(filepath.Join(t.TempDir(), "test.db"), secret)
	pid, err := system.Spawn(ctx, "persistence", instance)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = system.Stop(stop)
	})
	return ctx, cancel, system, pid, instance
}

func askPersistence(t *testing.T, ctx context.Context, pid *goakt.PID, message any) any {
	t.Helper()
	response, err := goakt.Ask(ctx, pid, message, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestGitHubIdentityCollisionAndEncryptedConnection(t *testing.T) {
	ctx, cancel, _, pid, instance := newPersistenceTest(t, strings.Repeat("jwt-secret", 4))
	defer cancel()
	const owner = "local-user"
	created := askPersistence(t, ctx, pid, &CreateLocalUser{ID: owner, Email: "alice@example.com", PasswordHash: "hash"}).(*LocalUserCreated)
	if created.Err != nil {
		t.Fatal(created.Err)
	}
	collision := askPersistence(t, ctx, pid, &ResolveGitHubLogin{Subject: "github-1", Email: "alice@example.com", DisplayName: "Alice"}).(*GitHubLoginResolved)
	if collision.Err != nil || !collision.EmailInUse || collision.ID != "" {
		t.Fatalf("existing email was not rejected: %#v", collision)
	}
	login := askPersistence(t, ctx, pid, &ResolveGitHubLogin{Subject: "github-2", Email: "new@example.com", DisplayName: "New"}).(*GitHubLoginResolved)
	if login.Err != nil || login.EmailInUse || login.ID == "" {
		t.Fatalf("new GitHub identity was not created: %#v", login)
	}
	connected := askPersistence(t, ctx, pid, &ConnectGitHub{UserID: owner, Subject: "github-private", Email: "alice@example.com", DisplayName: "Alice", Token: "gho-secret-token"}).(*GitHubConnected)
	if connected.Err != nil {
		t.Fatal(connected.Err)
	}
	found := askPersistence(t, ctx, pid, &FindGitHubToken{UserID: owner}).(*GitHubTokenFound)
	if found.Err != nil || !found.Connected || found.Token != "gho-secret-token" {
		t.Fatalf("GitHub token not recovered: %#v", found)
	}
	var ciphertext []byte
	if err := instance.db.QueryRow(`SELECT token_ciphertext FROM github_connections WHERE user_id=?`, owner).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), "gho-secret-token") {
		t.Fatal("GitHub token was stored in plaintext")
	}
	wrongKey := New(":memory:", strings.Repeat("different", 4))
	if _, err := wrongKey.decryptToken(ciphertext); err == nil {
		t.Fatal("token decrypted after the JWT key changed")
	}
	second := askPersistence(t, ctx, pid, &CreateLocalUser{ID: "other-user", Email: "other@example.com", PasswordHash: "hash"}).(*LocalUserCreated)
	if second.Err != nil {
		t.Fatal(second.Err)
	}
	reuse := askPersistence(t, ctx, pid, &ConnectGitHub{UserID: "other-user", Subject: "github-private", Token: "another"}).(*GitHubConnected)
	if reuse.Err == nil {
		t.Fatal("same GitHub subject was linked to two Janus users")
	}
}

func TestProjectOwnershipSnapshotRetentionAndFailedImport(t *testing.T) {
	ctx, cancel, _, pid, _ := newPersistenceTest(t, strings.Repeat("secret", 6))
	defer cancel()
	for _, user := range []struct{ id, email string }{{"alice", "alice@example.com"}, {"bob", "bob@example.com"}} {
		result := askPersistence(t, ctx, pid, &CreateLocalUser{ID: user.id, Email: user.email, PasswordHash: "hash"}).(*LocalUserCreated)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	project := askPersistence(t, ctx, pid, &CreateProject{ID: "project-1", OwnerID: "alice", Name: "Janus", Description: "source"}).(*ProjectCreated)
	if project.Err != nil {
		t.Fatal(project.Err)
	}
	other := askPersistence(t, ctx, pid, &GetProject{ID: "project-1", OwnerID: "bob"}).(*ProjectFound)
	if other.Err != nil || other.Found {
		t.Fatalf("project visible to another owner: %#v", other)
	}
	for i := 0; i < 6; i++ {
		jobID := "job-" + string(rune('a'+i))
		snapshotID := "snapshot-" + string(rune('a'+i))
		commit := strings.Repeat(string(rune('a'+i)), 40)
		job := askPersistence(t, ctx, pid, &CreateImportJob{ID: jobID, OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: commit}).(*ImportJobCreated)
		if job.Err != nil {
			t.Fatal(job.Err)
		}
		begun := askPersistence(t, ctx, pid, &BeginSnapshot{ID: snapshotID, JobID: jobID, OwnerID: "alice", ProjectID: "project-1", RepositoryID: 42, Repository: "alice/repo", Branch: "main", Commit: commit, DefaultBranch: "main", MetadataJSON: `{"scan":{"entrypoint_candidates":[]}}`}).(*BeginSnapshotResult)
		if begun.Err != nil {
			t.Fatal(begun.Err)
		}
		stored := askPersistence(t, ctx, pid, &AddSnapshotFiles{SnapshotID: snapshotID, Files: []SnapshotFile{{Path: "main.go", Mode: "100644", Content: []byte("package main"), Hash: "hash"}}}).(*SnapshotFilesAdded)
		if stored.Err != nil {
			t.Fatal(stored.Err)
		}
		advanceImport(t, ctx, pid, jobID)
		done := askPersistence(t, ctx, pid, &CompleteSnapshot{SnapshotID: snapshotID, JobID: jobID, FileCount: 1, TotalBytes: 12}).(*SnapshotCompleted)
		if done.Err != nil {
			t.Fatal(done.Err)
		}
		time.Sleep(3 * time.Millisecond)
	}
	listed := askPersistence(t, ctx, pid, &ListProjects{OwnerID: "alice"}).(*ProjectsListed)
	if listed.Err != nil || len(listed.Projects) != 1 {
		t.Fatalf("project list: %#v", listed)
	}
	if listed.Projects[0].SnapshotCount != 5 || listed.Projects[0].Commit != strings.Repeat("f", 40) {
		t.Fatalf("snapshot retention/latest selection wrong: %#v", listed.Projects[0])
	}
	activated := askPersistence(t, ctx, pid, &ActivateSnapshot{OwnerID: "alice", ProjectID: "project-1", SnapshotID: "snapshot-d"}).(*SnapshotActivated)
	if activated.Err != nil || !activated.Activated {
		t.Fatalf("activate retained snapshot: %#v", activated)
	}
	selected := askPersistence(t, ctx, pid, &GetProject{ID: "project-1", OwnerID: "alice"}).(*ProjectFound)
	if selected.Err != nil || !selected.Found || selected.Project.Commit != strings.Repeat("d", 40) {
		t.Fatalf("project did not switch to activated snapshot: %#v", selected)
	}
	retained := askPersistence(t, ctx, pid, &ListSnapshots{OwnerID: "alice", ProjectID: "project-1"}).(*SnapshotsListed)
	if retained.Err != nil || len(retained.Snapshots) != 5 {
		t.Fatalf("activating an older snapshot changed retention: %#v", retained)
	}
	activeCount := 0
	for _, snapshot := range retained.Snapshots {
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
	failedJob := askPersistence(t, ctx, pid, &CreateImportJob{ID: "job-failed", OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: strings.Repeat("0", 40)}).(*ImportJobCreated)
	if failedJob.Err != nil {
		t.Fatal(failedJob.Err)
	}
	stage := askPersistence(t, ctx, pid, &BeginSnapshot{ID: "snapshot-failed", JobID: "job-failed", OwnerID: "alice", ProjectID: "project-1", RepositoryID: 42, Repository: "alice/repo", Branch: "main", Commit: strings.Repeat("0", 40), MetadataJSON: "{}"}).(*BeginSnapshotResult)
	if stage.Err != nil {
		t.Fatal(stage.Err)
	}
	_ = askPersistence(t, ctx, pid, &DeleteStagingSnapshot{SnapshotID: "snapshot-failed"})
	_ = askPersistence(t, ctx, pid, &UpdateImportJob{ID: "job-failed", Status: "failed", Progress: 100, Error: "clone failed"})
	latest := askPersistence(t, ctx, pid, &GetProject{ID: "project-1", OwnerID: "alice"}).(*ProjectFound)
	if latest.Err != nil || !latest.Found || latest.Project.Commit != strings.Repeat("d", 40) || latest.Project.SnapshotCount != 5 {
		t.Fatalf("failed import damaged previous snapshot: %#v", latest)
	}
}

func TestProjectFilePagingAndWASMBuildArtifactOwnership(t *testing.T) {
	ctx, cancel, _, pid, _ := newPersistenceTest(t, strings.Repeat("wasm-secret", 4))
	defer cancel()
	for _, user := range []struct{ id, email string }{{"alice", "alice@example.com"}, {"bob", "bob@example.com"}} {
		created := askPersistence(t, ctx, pid, &CreateLocalUser{ID: user.id, Email: user.email, PasswordHash: "hash"}).(*LocalUserCreated)
		if created.Err != nil {
			t.Fatal(created.Err)
		}
	}
	if result := askPersistence(t, ctx, pid, &CreateProject{ID: "wasm-project", OwnerID: "alice", Name: "WASM"}).(*ProjectCreated); result.Err != nil {
		t.Fatal(result.Err)
	}
	if result := askPersistence(t, ctx, pid, &CreateImportJob{ID: "wasm-import", OwnerID: "alice", ProjectID: "wasm-project", Repository: "alice/rust", Branch: "main", Commit: strings.Repeat("a", 40)}).(*ImportJobCreated); result.Err != nil {
		t.Fatal(result.Err)
	}
	if result := askPersistence(t, ctx, pid, &BeginSnapshot{ID: "wasm-snapshot", JobID: "wasm-import", OwnerID: "alice", ProjectID: "wasm-project", Repository: "alice/rust", Branch: "main", Commit: strings.Repeat("a", 40), MetadataJSON: "{}"}).(*BeginSnapshotResult); result.Err != nil {
		t.Fatal(result.Err)
	}
	files := []SnapshotFile{{Path: "Cargo.toml", Content: []byte("manifest")}, {Path: "src/lib.rs", Content: []byte("library")}, {Path: "README.md", Content: []byte("readme")}}
	if result := askPersistence(t, ctx, pid, &AddSnapshotFiles{SnapshotID: "wasm-snapshot", Files: files}).(*SnapshotFilesAdded); result.Err != nil {
		t.Fatal(result.Err)
	}
	advanceImport(t, ctx, pid, "wasm-import")
	if result := askPersistence(t, ctx, pid, &CompleteSnapshot{SnapshotID: "wasm-snapshot", JobID: "wasm-import", FileCount: 3, TotalBytes: 23}).(*SnapshotCompleted); result.Err != nil {
		t.Fatal(result.Err)
	}
	first := askPersistence(t, ctx, pid, &ListProjectFiles{OwnerID: "alice", ProjectID: "wasm-project", SnapshotID: "wasm-snapshot", Offset: 0, Limit: 2}).(*ProjectFilesListed)
	second := askPersistence(t, ctx, pid, &ListProjectFiles{OwnerID: "alice", ProjectID: "wasm-project", SnapshotID: "wasm-snapshot", Offset: 2, Limit: 2}).(*ProjectFilesListed)
	if first.Err != nil || len(first.Files) != 2 || second.Err != nil || len(second.Files) != 1 || second.Files[0].Path != "src/lib.rs" {
		t.Fatalf("paged project files: first=%#v second=%#v", first, second)
	}
	created := askPersistence(t, ctx, pid, &CreateWASMBuild{ID: "wasm-job", OwnerID: "alice", ProjectID: "wasm-project", SnapshotID: "wasm-snapshot", Target: "wasm32-unknown-unknown"}).(*WASMBuildCreated)
	if created.Err != nil {
		t.Fatal(created.Err)
	}
	duplicate := askPersistence(t, ctx, pid, &CreateWASMBuild{ID: "wasm-job-2", OwnerID: "alice", ProjectID: "wasm-project", SnapshotID: "wasm-snapshot", Target: "wasm32-wasip1"}).(*WASMBuildCreated)
	if duplicate.Err == nil {
		t.Fatal("more than one active build was allowed for a project")
	}
	completed := askPersistence(t, ctx, pid, &CompleteWASMBuild{ID: "wasm-job", ArtifactName: "wasm-artifacts.zip", Artifact: []byte("artifact"), Log: []byte("build log")}).(*WASMBuildCreated)
	if completed.Err != nil {
		t.Fatal(completed.Err)
	}
	build := askPersistence(t, ctx, pid, &FindWASMBuild{ID: "wasm-job", OwnerID: "alice"}).(*WASMBuildFound)
	if build.Err != nil || !build.Found || build.Build.Status != "complete" || build.Build.Target != "wasm32-unknown-unknown" || build.Build.Log != "build log" {
		t.Fatalf("stored WASM build = %#v", build)
	}
	artifact := askPersistence(t, ctx, pid, &FindWASMBuildArtifact{ID: "wasm-job", OwnerID: "alice"}).(*WASMBuildArtifact)
	if artifact.Err != nil || !artifact.Found || string(artifact.Data) != "artifact" {
		t.Fatalf("stored WASM artifact = %#v", artifact)
	}
	other := askPersistence(t, ctx, pid, &FindWASMBuild{ID: "wasm-job", OwnerID: "bob"}).(*WASMBuildFound)
	if other.Err != nil || other.Found {
		t.Fatalf("another owner's WASM build was visible: %#v", other)
	}
}

// advanceImport moves an import job through the stages a worker passes before
// it completes its Snapshot.
func advanceImport(t *testing.T, ctx context.Context, pid *goakt.PID, jobID string) {
	t.Helper()
	for _, status := range []string{"cloning", "scanning", "storing"} {
		askPersistence(t, ctx, pid, &UpdateImportJob{ID: jobID, Status: status, Progress: 50})
	}
}
