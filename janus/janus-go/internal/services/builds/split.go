package builds

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"

	_ "turso.tech/database/tursogo"

	contractbuilds "janus/internal/contract/builds"
	"janus/internal/platform/tursodb"
)

// splitTables are the build tables of janus.db, parents first, with the columns
// builds.db's first migration gives them.
var splitTables = []struct{ name, columns string }{
	{"project_wasm_builds", "id,owner_id,project_id,snapshot_id,target,status,progress,error,artifact_name,artifact,build_log,created_at,updated_at"},
	{"project_checks", "build_id,owner_id,status,error,check_log,no_tests,created_at,updated_at"},
}

// SplitResult counts the rows Split copied, by table, and the unfinished Builds
// it queued.
type SplitResult map[string]int

// Split copies the build tables of the single-process database at legacy into a
// new builds.db at path, in one transaction, and queues the job of every
// unfinished Build so it runs once the service starts. It refuses when path
// already exists, so it runs once. legacy is only read: janus.db keeps its
// project_wasm_builds rows as the references Deployments need.
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
			// A database from before the build tables had none to copy.
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
		unfinished, err := queuedBuilds(ctx, tx)
		if err != nil {
			return err
		}
		for _, id := range unfinished {
			if err := addEvent(ctx, tx, contractbuilds.SubjectBuildJob, "build:"+id, contractbuilds.BuildJobMessage{JobID: id}); err != nil {
				return err
			}
		}
		result["queued builds"] = len(unfinished)
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

// hasTable reports whether the database at source has the named table.
func hasTable(ctx context.Context, source *sql.DB, name string) (bool, error) {
	var n int
	err := source.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

// queuedBuilds returns the IDs of the Builds that have not finished.
func queuedBuilds(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM project_wasm_builds WHERE status IN ('queued','building') ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// NeedsSplit reports whether builds.db at path is missing while the
// single-process database at legacy still holds WASM Builds. Starting the builds
// service then would hide them, so the caller should run Split first.
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
	var tables int
	if err := source.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='project_wasm_builds'`).Scan(&tables); err != nil || tables == 0 {
		return false, err
	}
	var builds int
	err = source.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_wasm_builds`).Scan(&builds)
	return builds > 0, err
}
