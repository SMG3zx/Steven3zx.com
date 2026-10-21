package builds

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	contractbuilds "janus/internal/contract/builds"
)

// legacyBuildSchema is janus.db's build tables before the split.
const legacyBuildSchema = `
CREATE TABLE project_wasm_builds (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, project_id TEXT NOT NULL, snapshot_id TEXT NOT NULL, target TEXT NOT NULL, status TEXT NOT NULL, progress INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', artifact_name TEXT NOT NULL DEFAULT '', artifact BLOB, build_log TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')), resume_count INTEGER NOT NULL DEFAULT 0);
CREATE TABLE project_checks (build_id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', check_log TEXT NOT NULL DEFAULT '', no_tests INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')));`

// legacyBuilds writes a janus.db with one complete Build and its Check, one
// failed Build, and one Build that was still running.
func legacyBuilds(t *testing.T) string {
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
		{legacyBuildSchema, nil},
		{`INSERT INTO project_wasm_builds(id,owner_id,project_id,snapshot_id,target,status,artifact_name,artifact,build_log) VALUES('done','alice','p1','s1','wasm32-wasip1','complete','wasm-artifacts.zip',?,'ok')`, []any{[]byte("zip")}},
		{`INSERT INTO project_checks(build_id,owner_id,status,check_log) VALUES('done','alice','passed','tests ok')`, nil},
		{`INSERT INTO project_wasm_builds(id,owner_id,project_id,snapshot_id,target,status) VALUES('broken','alice','p2','s2','wasm32-wasip1','failed')`, nil},
		{`INSERT INTO project_wasm_builds(id,owner_id,project_id,snapshot_id,target,status) VALUES('running','alice','p3','s3','wasm32-wasip1','building')`, nil},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestTheSplitKeepsBuildsAndRequeuesUnfinishedOnes(t *testing.T) {
	ctx := context.Background()
	legacy := legacyBuilds(t)
	path := filepath.Join(t.TempDir(), "builds.db")
	if needed, err := NeedsSplit(ctx, legacy, path); err != nil || !needed {
		t.Fatalf("NeedsSplit = %v, %v; want true", needed, err)
	}
	copied, err := Split(ctx, legacy, path)
	if err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"project_wasm_builds": 3, "project_checks": 1, "queued builds": 1} {
		if copied[table] != want {
			t.Fatalf("%s copied %d, want %d", table, copied[table], want)
		}
	}

	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var artifact []byte
	var log string
	if err := db.QueryRowContext(ctx, `SELECT artifact,build_log FROM project_wasm_builds WHERE id='done'`).Scan(&artifact, &log); err != nil {
		t.Fatal(err)
	}
	if string(artifact) != "zip" || log != "ok" {
		t.Fatalf("copied build = %q, %q", artifact, log)
	}
	var checkLog string
	if err := db.QueryRowContext(ctx, `SELECT check_log FROM project_checks WHERE build_id='done'`).Scan(&checkLog); err != nil || checkLog != "tests ok" {
		t.Fatalf("copied check = %q, %v", checkLog, err)
	}

	var subject, msgID string
	var data []byte
	if err := db.QueryRowContext(ctx, `SELECT subject,msg_id,data FROM outbox`).Scan(&subject, &msgID, &data); err != nil {
		t.Fatal(err)
	}
	if subject != contractbuilds.SubjectBuildJob || msgID != "build:running" || !strings.Contains(string(data), `"job_id":"running"`) {
		t.Fatalf("queued job = %s %s %s", subject, msgID, data)
	}

	if needed, err := NeedsSplit(ctx, legacy, path); err != nil || needed {
		t.Fatalf("NeedsSplit after the split = %v, %v; want false", needed, err)
	}
	if _, err := Split(ctx, legacy, path); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second split error = %v", err)
	}
}
