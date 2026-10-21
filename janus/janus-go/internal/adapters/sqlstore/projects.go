package sqlstore

import (
	"context"
	"database/sql"
	"errors"

	"janus/internal/app"
)

// migrateProjectSnapshotActiveColumn upgrades databases created before snapshot
// activation was introduced. CREATE TABLE IF NOT EXISTS does not evolve an
// existing table, so the column must be added explicitly. Existing snapshots
// are initialized to the newest successful revision exactly once.
func migrateProjectSnapshotActiveColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(project_snapshots)`)
	if err != nil {
		return err
	}
	hasActive := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "is_active" {
			hasActive = true
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if hasActive {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`ALTER TABLE project_snapshots ADD COLUMN is_active INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE project_snapshots AS snapshot SET is_active=1
		WHERE snapshot.state='ready' AND snapshot.id=(
			SELECT candidate.id FROM project_snapshots candidate
			WHERE candidate.project_id=snapshot.project_id AND candidate.state='ready'
			ORDER BY candidate.created_at DESC,candidate.id DESC LIMIT 1
		)`); err != nil {
		return err
	}
	return tx.Commit()
}

// AddProject records a reference to a Project of the projects service, so its
// WASM Builds and Deployments can refer to it. The row keeps only the IDs.
func (s *Store) AddProject(ctx context.Context, project app.ProjectRef) error {
	ctx, done := s.start(ctx, "AddProject")
	_, err := s.db.ExecContext(ctx, `INSERT INTO projects(id,owner_id,name) VALUES(?,?,'') ON CONFLICT(id) DO NOTHING`, project.ID, project.OwnerID)
	return done(err)
}

// AddSnapshot records a reference to a ready Snapshot of the projects service,
// with the language and runtime its Builds and runs need.
func (s *Store) AddSnapshot(ctx context.Context, snapshot app.SnapshotRef) error {
	ctx, done := s.start(ctx, "AddSnapshot")
	_, err := s.db.ExecContext(ctx, `INSERT INTO project_snapshots(id,project_id,owner_id,repository_id,repository,branch,commit_sha,language,runtime,state) VALUES(?,?,?,0,'','','',?,?,'ready') ON CONFLICT(id) DO NOTHING`,
		snapshot.ID, snapshot.ProjectID, snapshot.OwnerID, snapshot.Language, snapshot.Runtime)
	return done(err)
}

// FindSnapshot returns the reference to one of an Account's Snapshots.
func (s *Store) FindSnapshot(ctx context.Context, id, ownerID string) (app.SnapshotRef, bool, error) {
	ctx, done := s.start(ctx, "FindSnapshot")
	var ref app.SnapshotRef
	err := s.db.QueryRowContext(ctx, `SELECT id,project_id,owner_id,language,runtime FROM project_snapshots WHERE id=? AND owner_id=?`, id, ownerID).Scan(&ref.ID, &ref.ProjectID, &ref.OwnerID, &ref.Language, &ref.Runtime)
	if errors.Is(err, sql.ErrNoRows) {
		return app.SnapshotRef{}, false, done(nil)
	}
	return ref, err == nil, done(err)
}

// ListOwnedProjects returns the IDs of the Projects an Owner has references to.
func (s *Store) ListOwnedProjects(ctx context.Context, ownerID string) ([]string, error) {
	ctx, done := s.start(ctx, "ListOwnedProjects")
	ids, err := func() ([]string, error) {
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM projects WHERE owner_id=? ORDER BY id`, ownerID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		ids := make([]string, 0)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}()
	return ids, done(err)
}

// RemoveProject deletes the reference to a Project, and with it its Snapshot
// references, WASM Builds, Checks and Deployments.
func (s *Store) RemoveProject(ctx context.Context, id string) error {
	ctx, done := s.start(ctx, "RemoveProject")
	_, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id)
	return done(err)
}

// RemoveSnapshot deletes the reference to a Snapshot, and with it its WASM
// Builds and what refers to them.
func (s *Store) RemoveSnapshot(ctx context.Context, id string) error {
	ctx, done := s.start(ctx, "RemoveSnapshot")
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_snapshots WHERE id=?`, id)
	return done(err)
}
