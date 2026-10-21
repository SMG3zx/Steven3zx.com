package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	_ "turso.tech/database/tursogo"

	"janus/internal/platform/tursodb"
)

// splitTables are the project tables of janus.db, parents first, with the
// columns projects.db's first migration gives them.
var splitTables = []struct{ name, columns string }{
	{"projects", "id,owner_id,name,description,created_at,updated_at"},
	{"project_snapshots", "id,project_id,owner_id,repository_id,repository,branch,commit_sha,default_branch,language,runtime,stars,forks,metadata_json,manifest_json,entry_point,file_count,total_bytes,is_active,job_id,state,created_at"},
	{"project_files", "snapshot_id,path,mode,content,sha"},
	{"project_import_jobs", "id,owner_id,project_id,repository,branch,commit_sha,status,progress,error,created_at,updated_at"},
}

// SplitResult counts the rows Split copied, by table, and the unfinished
// Imports it queued.
type SplitResult map[string]int

// Split copies the project tables of the single-process database at legacy
// into a new projects.db at path, in one transaction, and queues the job of
// every unfinished Import so it resumes once the service starts. It refuses
// when path already exists, so it runs once. legacy is only read: janus.db
// keeps projects and project_snapshots, as its WASM Builds still refer to them.
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
			n, err := copyTable(ctx, source, tx, table.name, table.columns)
			if err != nil {
				return fmt.Errorf("copy %s: %w", table.name, err)
			}
			result[table.name] = n
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM project_import_jobs WHERE status IN `+activeImportStatuses)
		if err != nil {
			return err
		}
		var unfinished []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			unfinished = append(unfinished, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, id := range unfinished {
			if err := queueImport(ctx, tx, id); err != nil {
				return err
			}
		}
		result["queued imports"] = len(unfinished)
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

func copyTable(ctx context.Context, source *sql.DB, tx *sql.Tx, table, columns string) (int, error) {
	rows, err := source.QueryContext(ctx, `SELECT `+columns+` FROM `+table)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	width := strings.Count(columns, ",") + 1
	insert := `INSERT INTO ` + table + `(` + columns + `) VALUES(` + strings.TrimSuffix(strings.Repeat("?,", width), ",") + `)`
	n := 0
	for rows.Next() {
		values := make([]any, width)
		pointers := make([]any, width)
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return n, err
		}
		if _, err := tx.ExecContext(ctx, insert, values...); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// NeedsSplit reports whether projects.db at path is missing while the
// single-process database at legacy still holds Projects with an Import
// history. Starting the projects service then would hide them, so the caller
// should run Split first. Projects janus.db records only for its WASM Builds
// have no import jobs and do not count.
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
	if err := source.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('projects','project_import_jobs')`).Scan(&tables); err != nil || tables < 2 {
		return false, err
	}
	var named int
	err = source.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM projects WHERE name<>'')+(SELECT COUNT(*) FROM project_import_jobs)`).Scan(&named)
	return named > 0, err
}
