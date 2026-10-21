package projects

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contractprojects "janus/internal/contract/projects"
)

// legacySchema is janus.db's project tables before the split, with users.
const legacySchema = `
CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL DEFAULT '', display_name TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE projects (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')));
CREATE TABLE project_snapshots (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, repository_id INTEGER NOT NULL, repository TEXT NOT NULL, branch TEXT NOT NULL, commit_sha TEXT NOT NULL, default_branch TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '', runtime TEXT NOT NULL DEFAULT '', stars INTEGER NOT NULL DEFAULT 0, forks INTEGER NOT NULL DEFAULT 0, metadata_json TEXT NOT NULL DEFAULT '{}', entry_point TEXT NOT NULL DEFAULT '', file_count INTEGER NOT NULL DEFAULT 0, total_bytes INTEGER NOT NULL DEFAULT 0, is_active INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL DEFAULT 'staging', created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')), job_id TEXT NOT NULL DEFAULT '', manifest_json TEXT NOT NULL DEFAULT '');
CREATE TABLE project_files (snapshot_id TEXT NOT NULL REFERENCES project_snapshots(id) ON DELETE CASCADE, path TEXT NOT NULL, mode TEXT NOT NULL, content BLOB NOT NULL, sha TEXT NOT NULL, PRIMARY KEY(snapshot_id, path));
CREATE TABLE project_import_jobs (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, repository TEXT NOT NULL, branch TEXT NOT NULL, commit_sha TEXT NOT NULL, status TEXT NOT NULL, progress INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')), resume_count INTEGER NOT NULL DEFAULT 0);`

// legacyDatabase writes a janus.db with alice's project p1: one finished
// import and its Snapshot, and one import of commit that was interrupted.
func legacyDatabase(t *testing.T, commit string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "janus.db")
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{legacySchema, nil},
		{`INSERT INTO users(id) VALUES('alice')`, nil},
		{`INSERT INTO projects(id,owner_id,name) VALUES('p1','alice','Legacy')`, nil},
		{`INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status,progress) VALUES('done','alice','p1','alice/demo','main',?,'complete',100)`, []any{strings.Repeat("a", 40)}},
		{`INSERT INTO project_snapshots(id,project_id,owner_id,repository_id,repository,branch,commit_sha,language,file_count,total_bytes,is_active,state,job_id) VALUES('s1','p1','alice',1,'alice/demo','main',?,'Go',1,5,1,'ready','done')`, []any{strings.Repeat("a", 40)}},
		{`INSERT INTO project_files(snapshot_id,path,mode,content,sha) VALUES('s1','main.go','100644',?,'h')`, []any{[]byte("hello")}},
		{`INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status,progress) VALUES('interrupted','alice','p1','alice/demo','main',?,'storing',80)`, []any{commit}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestTheSplitKeepsProjectsAndResumesUnfinishedImports(t *testing.T) {
	h := start(t)
	commit := h.github.Commit(t, "alice/demo", map[string]string{"main.go": mainGo})
	legacy := legacyDatabase(t, commit)
	ctx := context.Background()
	// A fresh projects.db for the split; the harness's own service is replaced.
	if err := h.svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	h.path = filepath.Join(t.TempDir(), "projects.db")
	if needed, err := NeedsSplit(ctx, legacy, h.path); err != nil || !needed {
		t.Fatalf("NeedsSplit before = %v, %v", needed, err)
	}
	copied, err := Split(ctx, legacy, h.path)
	if err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"projects": 1, "project_snapshots": 1, "project_files": 1, "project_import_jobs": 2, "queued imports": 1} {
		if copied[table] != want {
			t.Errorf("copied %d of %s, want %d", copied[table], table, want)
		}
	}
	if needed, err := NeedsSplit(ctx, legacy, h.path); err != nil || needed {
		t.Fatalf("NeedsSplit after = %v, %v", needed, err)
	}
	if _, err := Split(ctx, legacy, h.path); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a second split = %v", err)
	}

	h.startService()
	if found := h.getProject("alice", "p1"); !found.Found || found.Project.Name != "Legacy" || found.Project.SnapshotID != "s1" {
		t.Fatalf("legacy project after the split = %+v", found)
	}
	if outcome := h.waitImport("interrupted"); outcome != "project.import.completed" {
		t.Fatalf("the interrupted import ended with %s", outcome)
	}
	if got := h.getProject("alice", "p1").Project; got.Commit != commit || got.SnapshotCount != 2 {
		t.Fatalf("project after the resumed import = %+v", got)
	}
	var files contractprojects.Files
	h.must(contractprojects.SubjectSnapshotFiles, contractprojects.SnapshotFiles{OwnerID: "alice", ProjectID: "p1", SnapshotID: "s1"}, &files)
	if len(files.Files) != 1 || string(files.Files[0].Content) != "hello" {
		t.Fatalf("the legacy Snapshot's files = %+v", files)
	}
}

func TestAFailedProjectsSplitLeavesNothing(t *testing.T) {
	ctx := context.Background()
	legacy := filepath.Join(t.TempDir(), "janus.db")
	db, err := sql.Open("turso", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	path := filepath.Join(t.TempDir(), "projects.db")
	if _, err := Split(ctx, legacy, path); err == nil {
		t.Fatal("split a database without the project tables")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("projects.db after a failed split: %v", err)
	}
	if needed, err := NeedsSplit(ctx, legacy, path); err != nil || needed {
		t.Fatalf("a janus.db without import history = %v, %v, want no split", needed, err)
	}
}
