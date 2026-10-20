package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"

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

// CreateProject creates a Project and returns it.
func (s *Store) CreateProject(ctx context.Context, project app.CreateProject) (app.Project, error) {
	ctx, done := s.start(ctx, "CreateProject")
	var created app.Project
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO projects(id,owner_id,name,description) VALUES(?,?,?,?)`, project.ID, project.OwnerID, strings.TrimSpace(project.Name), strings.TrimSpace(project.Description)); err != nil {
			return err
		}
		var err error
		created, _, err = getProject(ctx, tx, project.ID, project.OwnerID)
		return err
	})
	return created, done(err)
}

// UpdateProject renames a Project and returns it.
func (s *Store) UpdateProject(ctx context.Context, project app.UpdateProject) (app.Project, error) {
	ctx, done := s.start(ctx, "UpdateProject")
	var updated app.Project
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE projects SET name=?,description=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND owner_id=?`, strings.TrimSpace(project.Name), strings.TrimSpace(project.Description), project.ID, project.OwnerID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("project not found")
		}
		updated, _, err = getProject(ctx, tx, project.ID, project.OwnerID)
		return err
	})
	return updated, done(err)
}

// DeleteProject deletes a Project and everything in it, unless an import of it
// is in progress.
func (s *Store) DeleteProject(ctx context.Context, id, ownerID string) (deleted bool, err error) {
	ctx, done := s.start(ctx, "DeleteProject")
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_import_jobs WHERE project_id=? AND owner_id=? AND status IN ('queued','cloning','scanning','storing')`, id, ownerID).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return errors.New("project has an import in progress")
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id=? AND owner_id=?`, id, ownerID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		deleted = count == 1
		return nil
	})
	return deleted, done(err)
}

const projectSelect = `SELECT p.id,p.owner_id,p.name,p.description,p.created_at,p.updated_at,COALESCE(s.id,''),
COALESCE(s.repository_id,0),COALESCE(s.repository,''),COALESCE(s.default_branch,''),COALESCE(s.branch,''),COALESCE(s.commit_sha,''),COALESCE(s.entry_point,''),COALESCE(s.language,''),COALESCE(s.runtime,''),COALESCE(s.stars,0),COALESCE(s.forks,0),COALESCE(s.file_count,0),COALESCE(s.total_bytes,0),COALESCE(s.metadata_json,'{}'),
(SELECT COUNT(*) FROM project_snapshots all_s WHERE all_s.project_id=p.id AND all_s.state='ready')
FROM projects p LEFT JOIN project_snapshots s ON s.id=(SELECT id FROM project_snapshots WHERE project_id=p.id AND state='ready' AND is_active=1 LIMIT 1)`

func scanProject(row interface{ Scan(...any) error }) (app.Project, error) {
	var p app.Project
	err := row.Scan(&p.ID, &p.OwnerID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.SnapshotID, &p.RepositoryID, &p.Repository, &p.DefaultBranch, &p.Branch, &p.Commit, &p.EntryPoint, &p.Language, &p.Runtime, &p.Stars, &p.Forks, &p.FileCount, &p.TotalBytes, &p.MetadataJSON, &p.SnapshotCount)
	return p, err
}

// ListProjects lists an Account's Projects, most recently updated first.
func (s *Store) ListProjects(ctx context.Context, ownerID string) ([]app.Project, error) {
	ctx, done := s.start(ctx, "ListProjects")
	projects, err := func() ([]app.Project, error) {
		rows, err := s.db.QueryContext(ctx, projectSelect+` WHERE p.owner_id=? ORDER BY p.updated_at DESC`, ownerID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		projects := make([]app.Project, 0)
		for rows.Next() {
			p, scanErr := scanProject(rows)
			if scanErr != nil {
				return nil, scanErr
			}
			projects = append(projects, p)
		}
		return projects, rows.Err()
	}()
	return projects, done(err)
}

// GetProject returns one of an Account's Projects.
func (s *Store) GetProject(ctx context.Context, id, ownerID string) (project app.Project, found bool, err error) {
	ctx, done := s.start(ctx, "GetProject")
	project, found, err = getProject(ctx, s.db, id, ownerID)
	return project, found, done(err)
}

func getProject(ctx context.Context, q querier, id, ownerID string) (app.Project, bool, error) {
	p, err := scanProject(q.QueryRowContext(ctx, projectSelect+` WHERE p.id=? AND p.owner_id=?`, id, ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return app.Project{}, false, nil
	}
	if err != nil {
		return app.Project{}, false, err
	}
	return p, true, nil
}

// ListSnapshots lists the ready Snapshots of a Project, newest first.
func (s *Store) ListSnapshots(ctx context.Context, ownerID, projectID string) ([]app.ProjectSnapshot, error) {
	ctx, done := s.start(ctx, "ListSnapshots")
	var snapshots []app.ProjectSnapshot
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, found, err := getProject(ctx, tx, projectID, ownerID); err != nil {
			return err
		} else if !found {
			return errors.New("project not found")
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,repository,branch,commit_sha,entry_point,language,runtime,default_branch,stars,forks,file_count,total_bytes,created_at,is_active FROM project_snapshots WHERE project_id=? AND owner_id=? AND state='ready' ORDER BY created_at DESC,id DESC`, projectID, ownerID)
		if err != nil {
			return err
		}
		defer rows.Close()
		snapshots = make([]app.ProjectSnapshot, 0, 5)
		for rows.Next() {
			var snapshot app.ProjectSnapshot
			var active int
			if err = rows.Scan(&snapshot.ID, &snapshot.Repository, &snapshot.Branch, &snapshot.Commit, &snapshot.EntryPoint, &snapshot.Language, &snapshot.Runtime, &snapshot.DefaultBranch, &snapshot.Stars, &snapshot.Forks, &snapshot.FileCount, &snapshot.TotalBytes, &snapshot.CreatedAt, &active); err != nil {
				return err
			}
			snapshot.Active = active != 0
			snapshots = append(snapshots, snapshot)
		}
		return rows.Err()
	})
	return snapshots, done(err)
}

// ActivateSnapshot makes a ready Snapshot the active one of its Project.
func (s *Store) ActivateSnapshot(ctx context.Context, ownerID, projectID, snapshotID string) (activated bool, err error) {
	ctx, done := s.start(ctx, "ActivateSnapshot")
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_snapshots WHERE id=? AND project_id=? AND owner_id=? AND state='ready'`, snapshotID, projectID, ownerID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE project_snapshots SET is_active=0 WHERE project_id=? AND owner_id=? AND state='ready'`, projectID, ownerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE project_snapshots SET is_active=1 WHERE id=? AND project_id=? AND owner_id=? AND state='ready'`, snapshotID, projectID, ownerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=? AND owner_id=?`, projectID, ownerID); err != nil {
			return err
		}
		activated = true
		return nil
	})
	return activated && err == nil, done(err)
}

// SetProjectEntryPoint records the Entrypoint an Account confirmed for a ready Snapshot.
func (s *Store) SetProjectEntryPoint(ctx context.Context, entry app.SetProjectEntryPoint) error {
	ctx, done := s.start(ctx, "SetProjectEntryPoint")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE project_snapshots SET entry_point=? WHERE id=? AND project_id=? AND owner_id=? AND state='ready'`, entry.EntryPoint, entry.SnapshotID, entry.ProjectID, entry.OwnerID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("project snapshot not found")
		}
		_, err = tx.ExecContext(ctx, `UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=? AND owner_id=?`, entry.ProjectID, entry.OwnerID)
		return err
	}))
}

// ListProjectFiles returns one page of a ready Snapshot's files, ordered by path.
func (s *Store) ListProjectFiles(ctx context.Context, page app.ListProjectFiles) ([]app.SnapshotFile, error) {
	ctx, done := s.start(ctx, "ListProjectFiles")
	if page.Offset < 0 {
		return nil, done(errors.New("file page offset cannot be negative"))
	}
	limit := page.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var files []app.SnapshotFile
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, found, err := getProject(ctx, tx, page.ProjectID, page.OwnerID); err != nil {
			return err
		} else if !found {
			return errors.New("project not found")
		}
		rows, err := tx.QueryContext(ctx, `SELECT f.path,f.mode,f.content,f.sha FROM project_files f JOIN project_snapshots s ON s.id=f.snapshot_id WHERE s.id=? AND s.project_id=? AND s.owner_id=? AND s.state='ready' ORDER BY f.path LIMIT ? OFFSET ?`, page.SnapshotID, page.ProjectID, page.OwnerID, limit, page.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		files = make([]app.SnapshotFile, 0)
		for rows.Next() {
			var f app.SnapshotFile
			if err = rows.Scan(&f.Path, &f.Mode, &f.Content, &f.Hash); err != nil {
				return err
			}
			files = append(files, f)
		}
		return rows.Err()
	})
	return files, done(err)
}
