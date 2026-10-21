package fleet

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// legacyFleetSchema is janus.db's fleet tables and the build columns the split
// reads, before the split.
const legacyFleetSchema = `
CREATE TABLE enrollment_codes (code_hash TEXT PRIMARY KEY, owner_id TEXT NOT NULL, expires_at INTEGER NOT NULL, used_at INTEGER);
CREATE TABLE agents (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, name TEXT NOT NULL, tag TEXT NOT NULL, key_id TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL);
CREATE TABLE run_usage (run_id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, agent_id TEXT NOT NULL, deployment_id TEXT NOT NULL, fuel INTEGER NOT NULL, peak_memory_bytes INTEGER NOT NULL, duration_ms INTEGER NOT NULL, cpu_time_us INTEGER NOT NULL, network_bytes_in INTEGER NOT NULL, network_bytes_out INTEGER NOT NULL, status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', probe INTEGER NOT NULL DEFAULT 0, started_at INTEGER NOT NULL);
CREATE TABLE project_wasm_builds (id TEXT PRIMARY KEY, snapshot_id TEXT NOT NULL);
CREATE TABLE deployments (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, project_id TEXT NOT NULL, build_id TEXT NOT NULL, agent_id TEXT NOT NULL, state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', deploy_anyway INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT '2026-10-20 12:00:00.000', updated_at TEXT NOT NULL DEFAULT '2026-10-20 12:00:00.000');`

// legacyFleet writes a janus.db with an Agent, a code, a run, a Build, and two
// Deployments: one whose Build is still there, and one whose Build is gone.
func legacyFleet(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "janus.db")
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		legacyFleetSchema,
		`INSERT INTO enrollment_codes(code_hash,owner_id,expires_at) VALUES('h1','alice',1)`,
		`INSERT INTO agents(id,owner_id,name,tag,key_id,created_at) VALUES('laptop','alice','laptop','tag:acct-alice','k1',1)`,
		`INSERT INTO run_usage(run_id,owner_id,agent_id,deployment_id,fuel,peak_memory_bytes,duration_ms,cpu_time_us,network_bytes_in,network_bytes_out,status,started_at) VALUES('r1','alice','laptop','d1',10,20,30,40,50,60,'ok',1)`,
		`INSERT INTO project_wasm_builds(id,snapshot_id) VALUES('b1','s1')`,
		`INSERT INTO deployments(id,owner_id,project_id,build_id,agent_id,state) VALUES('d1','alice','p1','b1','laptop','healthy')`,
		`INSERT INTO deployments(id,owner_id,project_id,build_id,agent_id,state,reason) VALUES('d2','alice','p1','gone','laptop','stopped','replaced')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestTheSplitCopiesTheFleetAndKeepsEachDeploymentsSnapshot(t *testing.T) {
	ctx := context.Background()
	legacy := legacyFleet(t)
	path := filepath.Join(t.TempDir(), "fleet.db")
	if needed, err := NeedsSplit(ctx, legacy, path); err != nil || !needed {
		t.Fatalf("NeedsSplit = %v, %v; want true", needed, err)
	}
	copied, err := Split(ctx, legacy, path)
	if err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"enrollment_codes": 1, "agents": 1, "run_usage": 1, "deployments": 2} {
		if copied[table] != want {
			t.Fatalf("%s copied %d, want %d", table, copied[table], want)
		}
	}
	if needed, err := NeedsSplit(ctx, legacy, path); err != nil || needed {
		t.Fatalf("NeedsSplit after the split = %v, %v; want false because fleet.db exists", needed, err)
	}
	if _, err := Split(ctx, legacy, path); err == nil {
		t.Fatal("a second split ran over fleet.db")
	}

	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snapshots := map[string]string{}
	rows, err := db.Query(`SELECT id, snapshot_id FROM deployments`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, snapshot string
		if err := rows.Scan(&id, &snapshot); err != nil {
			t.Fatal(err)
		}
		snapshots[id] = snapshot
	}
	if snapshots["d1"] != "s1" || snapshots["d2"] != "" {
		t.Fatalf("snapshots of the copied Deployments = %v, want d1 with s1 and d2 with none", snapshots)
	}
}
