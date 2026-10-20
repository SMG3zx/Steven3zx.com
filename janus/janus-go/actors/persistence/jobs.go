package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"janus/internal/domain/jobs"
)

// MaxResumes is how many times an interrupted job may resume after a restart.
// The interruption after the last allowed resume fails the job.
const MaxResumes = 3

// ActiveJob describes an import or WASM build that has not finished yet.
type ActiveJob struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Status    string `json:"status"`
}

func (a *Actor) updateImportStatus(ctx context.Context, msg *UpdateImportJob) error {
	return transitionJob(ctx, a.db, "project_import_jobs", jobs.Import, msg.ID, msg.Status, msg.Progress, msg.Error)
}

func (a *Actor) updateWASMStatus(ctx context.Context, msg *UpdateWASMBuild) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	from, err := currentStatus(ctx, tx, "project_wasm_builds", msg.ID)
	if err != nil {
		return err
	}
	if !jobs.WASM.CanMove(from, msg.Status) {
		return fmt.Errorf("WASM build cannot move from %s to %s", from, msg.Status)
	}
	result, err := tx.ExecContext(ctx, `UPDATE project_wasm_builds SET status=?,progress=?,error=?,build_log=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`, msg.Status, msg.Progress, msg.Error, msg.Log, msg.ID, from)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return errors.New("WASM build changed while it was being updated")
	}
	return tx.Commit()
}

func currentStatus(ctx context.Context, tx *sql.Tx, table, id string) (string, error) {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM `+table+` WHERE id=?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("job not found")
	}
	return status, err
}

// transitionJob moves an import job to status if the state machine allows it,
// comparing against the stored status so that a concurrent change is detected.
func transitionJob(ctx context.Context, db *sql.DB, table string, machine jobs.Machine, id, status string, progress int, reason string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	from, err := currentStatus(ctx, tx, table, id)
	if err != nil {
		return err
	}
	if !machine.CanMove(from, status) {
		return fmt.Errorf("job cannot move from %s to %s", from, status)
	}
	result, err := tx.ExecContext(ctx, `UPDATE `+table+` SET status=?,progress=?,error=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`, status, progress, reason, id, from)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return errors.New("job changed while it was being updated")
	}
	return tx.Commit()
}

// migrateAndRecoverJobs adds the columns that job resumption needs to existing
// databases, then recovers jobs interrupted by the previous shutdown. New
// databases get the same columns from this migration too.
func migrateAndRecoverJobs(db *sql.DB) error {
	columns := []struct{ table, column, definition string }{
		{"project_snapshots", "job_id", "TEXT NOT NULL DEFAULT ''"},
		{"project_snapshots", "runtime", "TEXT NOT NULL DEFAULT ''"},
		{"project_snapshots", "manifest_json", "TEXT NOT NULL DEFAULT ''"},
		{"project_import_jobs", "resume_count", "INTEGER NOT NULL DEFAULT 0"},
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

// recoverInterruptedJobs runs once at startup. Each import or WASM build that
// was still running is interrupted. It returns to queued and counts one resume,
// unless it has already used all of its resumes, in which case it fails. Only
// the Staging Snapshots of imports that will resume are kept.
func recoverInterruptedJobs(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = recoverJobTable(tx, "project_import_jobs", []string{"queued", "cloning", "scanning", "storing"}, "resume limit reached after repeated server restarts during import"); err != nil {
		return err
	}
	if err = recoverJobTable(tx, "project_wasm_builds", []string{"queued", "building"}, "resume limit reached after repeated server restarts during WASM build"); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM project_snapshots WHERE state='staging' AND job_id NOT IN (SELECT id FROM project_import_jobs WHERE status='queued')`); err != nil {
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
	var jobs []interrupted
	for rows.Next() {
		var job interrupted
		if err = rows.Scan(&job.id, &job.resumes); err != nil {
			_ = rows.Close()
			return err
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, job := range jobs {
		if job.resumes >= MaxResumes {
			if _, err = tx.Exec(`UPDATE `+table+` SET status='failed',progress=100,error=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, exhausted, job.id); err != nil {
				return err
			}
			if table == "project_import_jobs" {
				if _, err = tx.Exec(`DELETE FROM project_snapshots WHERE state='staging' AND job_id=?`, job.id); err != nil {
					return err
				}
			}
			continue
		}
		if _, err = tx.Exec(`UPDATE `+table+` SET status='queued',progress=0,error='',resume_count=resume_count+1,updated_at=CURRENT_TIMESTAMP WHERE id=?`, job.id); err != nil {
			return err
		}
	}
	return nil
}

// listResumableImports returns queued import jobs with their repository data.
// It is used at startup, when every queued job is one that was interrupted.
func (a *Actor) listResumableImports(ctx context.Context) ([]ResumableImport, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id,owner_id,project_id,repository,branch,commit_sha FROM project_import_jobs WHERE status='queued' ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []ResumableImport
	for rows.Next() {
		var job ResumableImport
		if err = rows.Scan(&job.JobID, &job.OwnerID, &job.ProjectID, &job.Repository, &job.Branch, &job.Commit); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// listResumableBuilds returns queued WASM builds with the language of the
// Snapshot each one compiles.
func (a *Actor) listResumableBuilds(ctx context.Context) ([]ResumableBuild, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT b.id,b.owner_id,b.project_id,b.snapshot_id,b.target,s.language FROM project_wasm_builds b JOIN project_snapshots s ON s.id=b.snapshot_id WHERE b.status='queued' ORDER BY b.created_at,b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var builds []ResumableBuild
	for rows.Next() {
		var build ResumableBuild
		if err = rows.Scan(&build.JobID, &build.OwnerID, &build.ProjectID, &build.SnapshotID, &build.Target, &build.Language); err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}
	return builds, rows.Err()
}

// findStagingSnapshot returns the Staging Snapshot an import job is writing, its
// manifest, and the path and hash of each file already stored in it.
func (a *Actor) findStagingSnapshot(ctx context.Context, jobID string) (StagingSnapshotFound, error) {
	var result StagingSnapshotFound
	err := a.db.QueryRowContext(ctx, `SELECT id,manifest_json FROM project_snapshots WHERE job_id=? AND state='staging' LIMIT 1`, jobID).Scan(&result.SnapshotID, &result.Manifest)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	rows, err := a.db.QueryContext(ctx, `SELECT path,sha FROM project_files WHERE snapshot_id=?`, result.SnapshotID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	result.Files = make(map[string]string)
	for rows.Next() {
		var path, sha string
		if err = rows.Scan(&path, &sha); err != nil {
			return result, err
		}
		result.Files[path] = sha
	}
	result.Found = true
	return result, rows.Err()
}

func (a *Actor) discardJobStaging(ctx context.Context, jobID string) error {
	_, err := a.db.ExecContext(ctx, `DELETE FROM project_snapshots WHERE job_id=? AND state='staging'`, jobID)
	return err
}

// deleteAccount removes an Account and everything it owns. If the Account has
// active jobs, nothing is deleted unless cancelActive is set, in which case those
// jobs are cancelled first. The whole change happens in one transaction.
func (a *Actor) deleteAccount(ctx context.Context, msg *DeleteAccount) (AccountDeleted, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountDeleted{}, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=?`, msg.UserID).Scan(&exists); err != nil {
		return AccountDeleted{}, err
	}
	if exists == 0 {
		return AccountDeleted{}, nil
	}
	active, err := activeJobs(ctx, tx, msg.UserID)
	if err != nil {
		return AccountDeleted{}, err
	}
	if len(active) > 0 && !msg.CancelActiveJobs {
		return AccountDeleted{ActiveJobs: active}, nil
	}
	if len(active) > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE project_import_jobs SET status='cancelled',updated_at=CURRENT_TIMESTAMP WHERE owner_id=? AND status IN ('queued','cloning','scanning','storing')`, msg.UserID); err != nil {
			return AccountDeleted{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE project_wasm_builds SET status='cancelled',updated_at=CURRENT_TIMESTAMP WHERE owner_id=? AND status IN ('queued','building')`, msg.UserID); err != nil {
			return AccountDeleted{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, msg.UserID); err != nil {
		return AccountDeleted{}, err
	}
	if err = tx.Commit(); err != nil {
		return AccountDeleted{}, err
	}
	return AccountDeleted{Deleted: true}, nil
}

// listActiveJobs returns the Owner's imports and WASM builds that have not
// finished.
func (a *Actor) listActiveJobs(ctx context.Context, owner string) ([]ActiveJob, error) {
	return activeJobs(ctx, a.db, owner)
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func activeJobs(ctx context.Context, db queryer, owner string) ([]ActiveJob, error) {
	rows, err := db.QueryContext(ctx, `SELECT 'import',id,project_id,status FROM project_import_jobs WHERE owner_id=? AND status IN ('queued','cloning','scanning','storing')
		UNION ALL SELECT 'wasm_build',id,project_id,status FROM project_wasm_builds WHERE owner_id=? AND status IN ('queued','building')`, owner, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]ActiveJob, 0)
	for rows.Next() {
		var job ActiveJob
		if err = rows.Scan(&job.Kind, &job.ID, &job.ProjectID, &job.Status); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}
