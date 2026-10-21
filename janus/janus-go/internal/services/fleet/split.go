package fleet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"

	_ "turso.tech/database/tursogo"

	"janus/internal/platform/tursodb"
)

// splitTables are the fleet tables of janus.db that copy as they are, with the
// columns fleet.db's migration gives them.
var splitTables = []struct{ name, columns string }{
	{"enrollment_codes", "code_hash,owner_id,expires_at,used_at"},
	{"agents", "id,owner_id,name,tag,key_id,created_at"},
	{"run_usage", "run_id,owner_id,agent_id,deployment_id,fuel,peak_memory_bytes,duration_ms,cpu_time_us,network_bytes_in,network_bytes_out,status,error,probe,started_at"},
}

// SplitResult counts the rows Split copied, by table.
type SplitResult map[string]int

// Split copies the fleet tables of the single-process database at legacy into a
// new fleet.db at path, in one transaction. It refuses when path already exists,
// so it runs once. legacy is only read: janus.db keeps its rows, which the
// single process no longer reads once the fleet service runs.
//
// A Deployment copies with the Snapshot of its Build, which janus.db knows only
// through project_wasm_builds, so a snapshot's deletion can still find it.
func Split(ctx context.Context, legacy, path string) (SplitResult, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s already exists; the split has run", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(legacy); err != nil {
		return nil, fmt.Errorf("the database to split: %w", err)
	}
	source, err := sql.Open("turso", legacy)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", legacy, err)
	}
	defer source.Close()
	source.SetMaxOpenConns(1)

	target, err := tursodb.Open(ctx, path, migrations)
	if err != nil {
		return nil, err
	}
	result := SplitResult{}
	err = tursodb.InTx(ctx, target, func(tx *sql.Tx) error {
		for _, table := range splitTables {
			// A database from before the fleet tables had none to copy.
			present, err := hasTable(ctx, source, table.name)
			if err != nil {
				return err
			}
			if !present {
				result[table.name] = 0
				continue
			}
			n, err := tursodb.CopyTable(ctx, source, tx, table.name, table.columns)
			if err != nil {
				return fmt.Errorf("copy %s: %w", table.name, err)
			}
			result[table.name] = n
		}
		n, err := copyDeployments(ctx, source, tx)
		if err != nil {
			return err
		}
		result["deployments"] = n
		return nil
	})
	closeErr := target.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
		return nil, err
	}
	return result, nil
}

// copyDeployments copies the Deployments of source into tx, each with the
// Snapshot of its Build. A Deployment whose Build is gone keeps no Snapshot.
func copyDeployments(ctx context.Context, source *sql.DB, tx *sql.Tx) (int, error) {
	present, err := hasTable(ctx, source, "deployments")
	if err != nil || !present {
		return 0, err
	}
	rows, err := source.QueryContext(ctx, `SELECT d.id,d.owner_id,d.project_id,COALESCE(b.snapshot_id,''),d.build_id,d.agent_id,d.state,
		d.reason,d.error,d.deploy_anyway,d.created_at,d.updated_at
		FROM deployments d LEFT JOIN project_wasm_builds b ON b.id=d.build_id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, owner, project, snapshot, build, agent, state, reason, errText, createdAt, updatedAt string
		var anyway int
		if err := rows.Scan(&id, &owner, &project, &snapshot, &build, &agent, &state, &reason, &errText, &anyway, &createdAt, &updatedAt); err != nil {
			return n, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO deployments(id,owner_id,project_id,snapshot_id,build_id,agent_id,state,reason,error,deploy_anyway,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, owner, project, snapshot, build, agent, state, reason, errText, anyway, createdAt, updatedAt); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// hasTable reports whether the database at source has the named table.
func hasTable(ctx context.Context, source *sql.DB, name string) (bool, error) {
	var n int
	err := source.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

// NeedsSplit reports whether fleet.db at path is missing while the
// single-process database at legacy still holds Agents or Deployments. Starting
// the fleet service then would hide them, so the caller should run Split first.
func NeedsSplit(ctx context.Context, legacy, path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if _, err := os.Stat(legacy); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	source, err := sql.Open("turso", legacy)
	if err != nil {
		return false, err
	}
	defer source.Close()
	for _, table := range []string{"agents", "deployments"} {
		present, err := hasTable(ctx, source, table)
		if err != nil {
			return false, err
		}
		if !present {
			continue
		}
		var rows int
		if err := source.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&rows); err != nil {
			return false, err
		}
		if rows > 0 {
			return true, nil
		}
	}
	return false, nil
}
