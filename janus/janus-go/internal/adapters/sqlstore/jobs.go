package sqlstore

import (
	"context"
	"database/sql"
	"errors"

	"janus/internal/app"
)

// FindWASMBuild returns one of an Account's builds, without its Artifact.
func (s *Store) FindWASMBuild(ctx context.Context, id, ownerID string) (build app.WASMBuild, found bool, err error) {
	ctx, done := s.start(ctx, "FindWASMBuild")
	err = s.db.QueryRowContext(ctx, `SELECT id,project_id,snapshot_id,target,status,progress,error,artifact_name,build_log,created_at,updated_at FROM project_wasm_builds WHERE id=? AND owner_id=?`, id, ownerID).Scan(&build.ID, &build.ProjectID, &build.SnapshotID, &build.Target, &build.Status, &build.Progress, &build.Error, &build.ArtifactName, &build.Log, &build.CreatedAt, &build.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return app.WASMBuild{}, false, done(nil)
	}
	return build, err == nil, done(err)
}

// migrateJobColumns adds the columns the job tables of existing databases need.
// New databases get the same columns from this migration too.
func migrateJobColumns(db *sql.DB) error {
	columns := []struct{ table, column, definition string }{
		{"project_snapshots", "job_id", "TEXT NOT NULL DEFAULT ''"},
		{"project_snapshots", "runtime", "TEXT NOT NULL DEFAULT ''"},
		{"project_snapshots", "manifest_json", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range columns {
		if err := ensureColumn(db, c.table, c.column, c.definition); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS project_snapshots_job ON project_snapshots(job_id)`); err != nil {
		return err
	}
	return nil
}

// ensureColumn adds column to table when it is missing.
func ensureColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

// ApplyWASMBuild stores the state of a WASM Build the builds service published,
// as the reference this Store keeps for Deployments. A Build of a Project or
// Snapshot this Store does not reference is not kept.
func (s *Store) ApplyWASMBuild(ctx context.Context, build app.WASMBuildChange) error {
	ctx, done := s.start(ctx, "ApplyWASMBuild")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO project_wasm_builds(id,owner_id,project_id,snapshot_id,target,status,error)
			SELECT ?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM projects WHERE id=? AND owner_id=?) AND EXISTS(SELECT 1 FROM project_snapshots WHERE id=? AND project_id=?)
			ON CONFLICT(id) DO UPDATE SET status=excluded.status,error=excluded.error,updated_at=CURRENT_TIMESTAMP`,
			build.ID, build.OwnerID, build.ProjectID, build.SnapshotID, build.Target, build.Status, build.Error,
			build.ProjectID, build.OwnerID, build.SnapshotID, build.ProjectID)
		return err
	}))
}

// DeleteAccount removes an Account's User and the records that refer to it. Whether
// the Account may be deleted is identity's decision, made after the services report
// their unfinished jobs, so this does not check for them. The whole change happens
// in one transaction.
func (s *Store) DeleteAccount(ctx context.Context, userID string) (app.AccountDeleted, error) {
	ctx, done := s.start(ctx, "DeleteAccount")
	var result app.AccountDeleted
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=?`, userID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, userID); err != nil {
			return err
		}
		result.Deleted = true
		return nil
	})
	if err != nil {
		result = app.AccountDeleted{}
	}
	return result, done(err)
}

