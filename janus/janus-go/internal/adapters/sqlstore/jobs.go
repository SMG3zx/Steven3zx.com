package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"janus/internal/app"
	"janus/internal/domain/jobs"
)

// CreateWASMBuild queues a build of a ready Snapshot.
func (s *Store) CreateWASMBuild(ctx context.Context, build app.CreateWASMBuild) error {
	ctx, done := s.start(ctx, "CreateWASMBuild")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_wasm_builds(id,owner_id,project_id,snapshot_id,target,status)
			SELECT ?,p.owner_id,p.id,s.id,?,'queued' FROM projects p JOIN project_snapshots s ON s.project_id=p.id
			WHERE p.id=? AND p.owner_id=? AND s.id=? AND s.state='ready'`, build.ID, build.Target, build.ProjectID, build.OwnerID, build.SnapshotID); err != nil {
			return err
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_wasm_builds WHERE id=?`, build.ID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return errors.New("project snapshot not found")
		}
		return nil
	}))
}

// TransitionWASMBuild moves a build to a new status when the build state
// machine allows it and the build is still in the status the caller saw.
func (s *Store) TransitionWASMBuild(ctx context.Context, t app.JobTransition) error {
	ctx, done := s.start(ctx, "TransitionWASMBuild")
	return done(s.transition(ctx, "project_wasm_builds", jobs.WASM, t,
		`UPDATE project_wasm_builds SET status=?,progress=?,error=?,build_log=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`,
		[]any{t.To, t.Progress, t.Error, t.Log, t.ID, t.From}, "WASM build"))
}

// CompleteWASMBuild stores a finished build's Artifact and log.
func (s *Store) CompleteWASMBuild(ctx context.Context, build app.CompleteWASMBuild) error {
	ctx, done := s.start(ctx, "CompleteWASMBuild")
	result, err := s.db.ExecContext(ctx, `UPDATE project_wasm_builds SET status='complete',progress=100,error='',artifact_name=?,artifact=?,build_log=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status IN ('queued','building')`, build.ArtifactName, build.Artifact, string(build.Log), build.ID)
	if err == nil {
		if rows, _ := result.RowsAffected(); rows == 0 {
			err = errors.New("WASM build not found")
		}
	}
	return done(err)
}

// FindWASMBuild returns one of an Account's builds, without its Artifact.
func (s *Store) FindWASMBuild(ctx context.Context, id, ownerID string) (build app.WASMBuild, found bool, err error) {
	ctx, done := s.start(ctx, "FindWASMBuild")
	err = s.db.QueryRowContext(ctx, `SELECT id,project_id,snapshot_id,target,status,progress,error,artifact_name,build_log,created_at,updated_at FROM project_wasm_builds WHERE id=? AND owner_id=?`, id, ownerID).Scan(&build.ID, &build.ProjectID, &build.SnapshotID, &build.Target, &build.Status, &build.Progress, &build.Error, &build.ArtifactName, &build.Log, &build.CreatedAt, &build.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return app.WASMBuild{}, false, done(nil)
	}
	return build, err == nil, done(err)
}

// FindWASMBuildArtifact returns the Artifact of one of an Account's completed builds.
func (s *Store) FindWASMBuildArtifact(ctx context.Context, id, ownerID string) (app.WASMBuildArtifact, error) {
	ctx, done := s.start(ctx, "FindWASMBuildArtifact")
	var result app.WASMBuildArtifact
	err := s.db.QueryRowContext(ctx, `SELECT artifact_name,artifact FROM project_wasm_builds WHERE id=? AND owner_id=? AND status='complete'`, id, ownerID).Scan(&result.Name, &result.Data)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err == nil {
		result.Found = true
	}
	return result, done(err)
}

// transition checks the move against the domain rule and then swaps the status
// with one UPDATE that matches the status the caller saw, so a concurrent change
// is detected by the database and not by a read.
func (s *Store) transition(ctx context.Context, table string, machine jobs.Machine, t app.JobTransition, update string, args []any, noun string) error {
	if !machine.CanMove(t.From, t.To) {
		return fmt.Errorf("%s cannot move from %s to %s", noun, t.From, t.To)
	}
	result, err := s.db.ExecContext(ctx, update, args...)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 1 {
		return nil
	}
	var exists int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id=?`, t.ID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return errors.New("job not found")
	}
	return fmt.Errorf("%s changed while it was being updated", noun)
}

// migrateAndRecoverJobs adds the columns that job resumption needs to existing
// databases, then recovers jobs interrupted by the previous shutdown. New
// databases get the same columns from this migration too.
func migrateAndRecoverJobs(db *sql.DB) error {
	columns := []struct{ table, column, definition string }{
		{"project_snapshots", "job_id", "TEXT NOT NULL DEFAULT ''"},
		{"project_snapshots", "runtime", "TEXT NOT NULL DEFAULT ''"},
		{"project_snapshots", "manifest_json", "TEXT NOT NULL DEFAULT ''"},
		{"project_wasm_builds", "resume_count", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, c := range columns {
		if err := ensureColumn(db, c.table, c.column, c.definition); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS project_snapshots_job ON project_snapshots(job_id)`); err != nil {
		return err
	}
	return recoverInterruptedJobs(db)
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

// recoverInterruptedJobs runs once at startup. Each WASM build that was still
// running is interrupted. It returns to queued and counts one resume, unless it
// has already used all of its resumes, in which case it fails. Imports are the
// projects service's, which resumes its own.
func recoverInterruptedJobs(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = recoverJobTable(tx, "project_wasm_builds", []string{"queued", "building"}, "resume limit reached after repeated server restarts during WASM build"); err != nil {
		return err
	}
	return tx.Commit()
}

func recoverJobTable(tx *sql.Tx, table string, running []string, exhausted string) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(running)), ",")
	args := make([]any, len(running))
	for i, status := range running {
		args[i] = status
	}
	rows, err := tx.Query(`SELECT id,resume_count FROM `+table+` WHERE status IN (`+placeholders+`)`, args...)
	if err != nil {
		return err
	}
	type interrupted struct {
		id      string
		resumes int
	}
	var interruptedJobs []interrupted
	for rows.Next() {
		var job interrupted
		if err = rows.Scan(&job.id, &job.resumes); err != nil {
			_ = rows.Close()
			return err
		}
		interruptedJobs = append(interruptedJobs, job)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, job := range interruptedJobs {
		if job.resumes >= app.MaxResumes {
			if _, err = tx.Exec(`UPDATE `+table+` SET status='failed',progress=100,error=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, exhausted, job.id); err != nil {
				return err
			}
			continue
		}
		if _, err = tx.Exec(`UPDATE `+table+` SET status='queued',progress=0,error='',resume_count=resume_count+1,updated_at=CURRENT_TIMESTAMP WHERE id=?`, job.id); err != nil {
			return err
		}
	}
	return nil
}

// ListResumableBuilds returns queued WASM builds with the language of the
// Snapshot each one compiles.
func (s *Store) ListResumableBuilds(ctx context.Context) ([]app.ResumableBuild, error) {
	ctx, done := s.start(ctx, "ListResumableBuilds")
	builds, err := func() ([]app.ResumableBuild, error) {
		rows, err := s.db.QueryContext(ctx, `SELECT b.id,b.owner_id,b.project_id,b.snapshot_id,b.target,s.language FROM project_wasm_builds b JOIN project_snapshots s ON s.id=b.snapshot_id WHERE b.status='queued' ORDER BY b.created_at,b.id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var builds []app.ResumableBuild
		for rows.Next() {
			var build app.ResumableBuild
			if err = rows.Scan(&build.JobID, &build.OwnerID, &build.ProjectID, &build.SnapshotID, &build.Target, &build.Language); err != nil {
				return nil, err
			}
			builds = append(builds, build)
		}
		return builds, rows.Err()
	}()
	return builds, done(err)
}

// ListActiveJobs returns the Owner's WASM builds that have not finished.
// Imports are the projects service's.
func (s *Store) ListActiveJobs(ctx context.Context, ownerID string) ([]app.ActiveJob, error) {
	ctx, done := s.start(ctx, "ListActiveJobs")
	active, err := activeJobs(ctx, s.db, ownerID)
	return active, done(err)
}

// DeleteAccount removes an Account and everything it owns. If the Account has
// active jobs, nothing is deleted unless cancelActiveJobs is set, in which case
// those jobs are cancelled first. The whole change happens in one transaction.
func (s *Store) DeleteAccount(ctx context.Context, userID string, cancelActiveJobs bool) (app.AccountDeleted, error) {
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
		active, err := activeJobs(ctx, tx, userID)
		if err != nil {
			return err
		}
		if len(active) > 0 && !cancelActiveJobs {
			result.ActiveJobs = active
			return nil
		}
		if len(active) > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE project_wasm_builds SET status='cancelled',updated_at=CURRENT_TIMESTAMP WHERE owner_id=? AND status IN ('queued','building')`, userID); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, userID); err != nil {
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

func activeJobs(ctx context.Context, q querier, ownerID string) ([]app.ActiveJob, error) {
	rows, err := q.QueryContext(ctx, `SELECT 'wasm_build',id,project_id,status FROM project_wasm_builds WHERE owner_id=? AND status IN ('queued','building')`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	active := make([]app.ActiveJob, 0)
	for rows.Next() {
		var job app.ActiveJob
		if err = rows.Scan(&job.Kind, &job.ID, &job.ProjectID, &job.Status); err != nil {
			return nil, err
		}
		active = append(active, job)
	}
	return active, rows.Err()
}
