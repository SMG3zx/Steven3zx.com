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

// CreateImportJob queues an import of a repository into a Project.
func (s *Store) CreateImportJob(ctx context.Context, job app.CreateImportJob) error {
	ctx, done := s.start(ctx, "CreateImportJob")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status) SELECT ?,owner_id,id,?,?,?,'queued' FROM projects WHERE id=? AND owner_id=?`, job.ID, job.Repository, job.Branch, job.Commit, job.ProjectID, job.OwnerID); err != nil {
			return err
		}
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM project_import_jobs WHERE id=?`, job.ID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("project not found")
		}
		return err
	}))
}

// UpdateImportJob moves an import job to a new status when the import state
// machine allows it.
func (s *Store) UpdateImportJob(ctx context.Context, update app.UpdateImportJob) error {
	ctx, done := s.start(ctx, "UpdateImportJob")
	return done(s.transitionJob(ctx, "project_import_jobs", jobs.Import, update.ID, update.Status, update.Progress, update.Error))
}

// FindImportJob returns one of an Account's import jobs.
func (s *Store) FindImportJob(ctx context.Context, id, ownerID string) (job app.ImportJob, found bool, err error) {
	ctx, done := s.start(ctx, "FindImportJob")
	err = s.db.QueryRowContext(ctx, `SELECT id,owner_id,project_id,repository,branch,commit_sha,status,progress,error,created_at,updated_at FROM project_import_jobs WHERE id=? AND owner_id=?`, id, ownerID).Scan(&job.ID, &job.OwnerID, &job.ProjectID, &job.Repository, &job.Branch, &job.Commit, &job.Status, &job.Progress, &job.Error, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return app.ImportJob{}, false, done(nil)
	}
	return job, err == nil, done(err)
}

// BeginSnapshot starts the Staging Snapshot an import job writes into.
func (s *Store) BeginSnapshot(ctx context.Context, snapshot app.BeginSnapshot) error {
	ctx, done := s.start(ctx, "BeginSnapshot")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_snapshots(id,project_id,owner_id,repository_id,repository,branch,commit_sha,default_branch,language,runtime,stars,forks,metadata_json,manifest_json,job_id,state) SELECT ?,p.id,p.owner_id,?,?,?,?,?,?,?,?,?,?,?,?,'staging' FROM projects p WHERE p.id=? AND p.owner_id=?`, snapshot.ID, snapshot.RepositoryID, snapshot.Repository, snapshot.Branch, snapshot.Commit, snapshot.DefaultBranch, snapshot.Language, snapshot.Runtime, snapshot.Stars, snapshot.Forks, snapshot.MetadataJSON, snapshot.Manifest, snapshot.JobID, snapshot.ProjectID, snapshot.OwnerID); err != nil {
			return err
		}
		var status string
		err := tx.QueryRowContext(ctx, `SELECT state FROM project_snapshots WHERE id=?`, snapshot.ID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("project not found")
		}
		return err
	}))
}

// AddSnapshotFiles stores files in a Staging Snapshot.
func (s *Store) AddSnapshotFiles(ctx context.Context, snapshotID string, files []app.SnapshotFile) error {
	ctx, done := s.start(ctx, "AddSnapshotFiles")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO project_files(snapshot_id,path,mode,content,sha) VALUES(?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, file := range files {
			if _, err = stmt.ExecContext(ctx, snapshotID, file.Path, file.Mode, file.Content, file.Hash); err != nil {
				return err
			}
		}
		return nil
	}))
}

// CompleteSnapshot makes a Staging Snapshot the active ready Snapshot of its
// Project, completes its import job and drops all but the newest five Snapshots.
func (s *Store) CompleteSnapshot(ctx context.Context, snapshot app.CompleteSnapshot) error {
	ctx, done := s.start(ctx, "CompleteSnapshot")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE project_snapshots SET is_active=0 WHERE project_id=(SELECT project_id FROM project_snapshots WHERE id=?) AND state='ready'`, snapshot.SnapshotID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE project_snapshots SET entry_point=?,file_count=?,total_bytes=?,state='ready',is_active=1 WHERE id=? AND state='staging'`, snapshot.EntryPoint, snapshot.FileCount, snapshot.TotalBytes, snapshot.SnapshotID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("staging snapshot not found")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=(SELECT project_id FROM project_snapshots WHERE id=?)`, snapshot.SnapshotID); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `UPDATE project_import_jobs SET status='complete',progress=100,error='',updated_at=CURRENT_TIMESTAMP WHERE id=? AND status='storing'`, snapshot.JobID)
		if err == nil {
			if n, _ := result.RowsAffected(); n != 1 {
				err = errors.New("import was cancelled before it completed")
			}
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM project_snapshots WHERE project_id=(SELECT project_id FROM project_snapshots WHERE id=?) AND state='ready' AND id NOT IN (SELECT id FROM project_snapshots WHERE project_id=(SELECT project_id FROM project_snapshots WHERE id=?) AND state='ready' ORDER BY created_at DESC,id DESC LIMIT 5)`, snapshot.SnapshotID, snapshot.SnapshotID)
		return err
	}))
}

// DeleteStagingSnapshot deletes a Snapshot that is still staging.
func (s *Store) DeleteStagingSnapshot(ctx context.Context, snapshotID string) error {
	ctx, done := s.start(ctx, "DeleteStagingSnapshot")
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_snapshots WHERE id=? AND state='staging'`, snapshotID)
	return done(err)
}

// FindStagingSnapshot returns the Staging Snapshot an import job is writing, its
// manifest, and the path and hash of each file already stored in it.
func (s *Store) FindStagingSnapshot(ctx context.Context, jobID string) (app.StagingSnapshotFound, error) {
	ctx, done := s.start(ctx, "FindStagingSnapshot")
	var result app.StagingSnapshotFound
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT id,manifest_json FROM project_snapshots WHERE job_id=? AND state='staging' LIMIT 1`, jobID).Scan(&result.SnapshotID, &result.Manifest)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT path,sha FROM project_files WHERE snapshot_id=?`, result.SnapshotID)
		if err != nil {
			return err
		}
		defer rows.Close()
		result.Files = make(map[string]string)
		for rows.Next() {
			var path, sha string
			if err = rows.Scan(&path, &sha); err != nil {
				return err
			}
			result.Files[path] = sha
		}
		result.Found = true
		return rows.Err()
	})
	return result, done(err)
}

// DiscardJobStaging deletes the Staging Snapshot of an import job that has
// failed for good.
func (s *Store) DiscardJobStaging(ctx context.Context, jobID string) error {
	ctx, done := s.start(ctx, "DiscardJobStaging")
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_snapshots WHERE job_id=? AND state='staging'`, jobID)
	return done(err)
}

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

// UpdateWASMBuild moves a build to a new status when the build state machine
// allows it.
func (s *Store) UpdateWASMBuild(ctx context.Context, update app.UpdateWASMBuild) error {
	ctx, done := s.start(ctx, "UpdateWASMBuild")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		from, err := currentStatus(ctx, tx, "project_wasm_builds", update.ID)
		if err != nil {
			return err
		}
		if !jobs.WASM.CanMove(from, update.Status) {
			return fmt.Errorf("WASM build cannot move from %s to %s", from, update.Status)
		}
		result, err := tx.ExecContext(ctx, `UPDATE project_wasm_builds SET status=?,progress=?,error=?,build_log=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`, update.Status, update.Progress, update.Error, update.Log, update.ID, from)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return errors.New("WASM build changed while it was being updated")
		}
		return nil
	}))
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

func currentStatus(ctx context.Context, q querier, table, id string) (string, error) {
	var status string
	err := q.QueryRowContext(ctx, `SELECT status FROM `+table+` WHERE id=?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("job not found")
	}
	return status, err
}

// transitionJob moves an import job to status if the state machine allows it,
// comparing against the stored status so that a concurrent change is detected.
func (s *Store) transitionJob(ctx context.Context, table string, machine jobs.Machine, id, status string, progress int, reason string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
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
		return nil
	})
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

// ListResumableImports returns queued import jobs with their repository data.
// It is used at startup, when every queued job is one that was interrupted.
func (s *Store) ListResumableImports(ctx context.Context) ([]app.ResumableImport, error) {
	ctx, done := s.start(ctx, "ListResumableImports")
	imports, err := func() ([]app.ResumableImport, error) {
		rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,project_id,repository,branch,commit_sha FROM project_import_jobs WHERE status='queued' ORDER BY created_at,id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var imports []app.ResumableImport
		for rows.Next() {
			var job app.ResumableImport
			if err = rows.Scan(&job.JobID, &job.OwnerID, &job.ProjectID, &job.Repository, &job.Branch, &job.Commit); err != nil {
				return nil, err
			}
			imports = append(imports, job)
		}
		return imports, rows.Err()
	}()
	return imports, done(err)
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

// ListActiveJobs returns the Owner's imports and WASM builds that have not
// finished.
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
			if _, err = tx.ExecContext(ctx, `UPDATE project_import_jobs SET status='cancelled',updated_at=CURRENT_TIMESTAMP WHERE owner_id=? AND status IN ('queued','cloning','scanning','storing')`, userID); err != nil {
				return err
			}
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
	rows, err := q.QueryContext(ctx, `SELECT 'import',id,project_id,status FROM project_import_jobs WHERE owner_id=? AND status IN ('queued','cloning','scanning','storing')
		UNION ALL SELECT 'wasm_build',id,project_id,status FROM project_wasm_builds WHERE owner_id=? AND status IN ('queued','building')`, ownerID, ownerID)
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
