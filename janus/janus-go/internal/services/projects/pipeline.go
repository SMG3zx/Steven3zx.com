package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	contractprojects "janus/internal/contract/projects"
	"janus/internal/domain/manifest"
	"janus/internal/domain/scan"
	"janus/internal/platform/tursodb"
)

// snapshotsKept is how many ready Snapshots a Project keeps.
const snapshotsKept = 5

// importRun is one run of an Import. status is the status the run last moved
// the job to: the From of its next transition, so a change by anyone else
// (the Owner's deletion cancelling it) stops the run.
type importRun struct {
	job        contractprojects.ImportJob
	status     string
	token      string
	repository contractprojects.Repository
}

// errStagingCorrupt reports that a Staging Snapshot does not match its commit.
// The run discards it and starts again from the clone.
var errStagingCorrupt = errors.New("staging snapshot does not match the commit")

// errTransient is a failure the run cannot blame on the Import, such as the
// identity service being down. The job is not failed; JetStream delivers it
// again.
var errTransient = errors.New("import will be retried")

// runImport runs an Import from queued to complete or failed. An Import
// interrupted part-way resumes from its Staging Snapshot.
func (s *Service) runImport(parent context.Context, job contractprojects.ImportJob) (err error) {
	ctx, release := s.track(parent, job)
	defer release()
	run := &importRun{job: job, status: "queued"}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = s.fail(ctx, run, "repository import panicked", fmt.Errorf("%v", recovered))
		}
	}()
	token, connected, err := s.token(ctx, job.OwnerID)
	if err != nil {
		return fmt.Errorf("%w: %v", errTransient, err)
	}
	if !connected {
		return s.fail(ctx, run, "reconnect GitHub to import this repository", errNotConnected)
	}
	run.token = token
	if run.repository, err = s.github.repository(ctx, token, job.Repository); err != nil {
		return s.fail(ctx, run, "repository metadata could not be read", err)
	}
	staged, err := s.findStaging(ctx, job.ID)
	if err != nil {
		return s.fail(ctx, run, "snapshot could not be initialized", err)
	}
	if staged != nil {
		err = s.storeStaged(ctx, run, staged)
		if !errors.Is(err, errStagingCorrupt) {
			return err
		}
		if err = s.discardStaging(ctx, job.ID); err != nil {
			return s.fail(ctx, run, "snapshot could not be initialized", err)
		}
	}
	return s.importFromClone(ctx, run)
}

// update moves the run's job to status.
func (s *Service) update(ctx context.Context, run *importRun, status string, progress int) error {
	err := s.transition(ctx, run.job.ID, run.status, status, progress, "")
	if err == nil {
		run.status = status
	}
	return err
}

// fail records the Import as failed with the sentence the Account sees, and
// drops its Staging Snapshot. While the service is stopping it records
// nothing, so the Import resumes on its next delivery.
func (s *Service) fail(ctx context.Context, run *importRun, public string, err error) error {
	if s.closing.Load() {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	if transitionErr := s.transition(ctx, run.job.ID, run.status, "failed", 100, public); transitionErr == nil {
		run.status = "failed"
	}
	_ = s.discardStaging(ctx, run.job.ID)
	return fmt.Errorf("%s: %w", public, err)
}

// stagedImport is the Staging Snapshot of an interrupted Import, after its
// manifest and stored files have been checked against each other.
type stagedImport struct {
	snapshotID string
	manifest   []manifest.Entry
	stored     map[string]bool
}

// pending returns the manifest entries whose files are not stored yet.
func (s *stagedImport) pending() []manifest.Entry {
	pending := make([]manifest.Entry, 0, len(s.manifest))
	for _, entry := range s.manifest {
		if !s.stored[entry.Path] {
			pending = append(pending, entry)
		}
	}
	return pending
}

// findStaging returns the Staging Snapshot of an interrupted Import when it
// has a manifest and every stored file is in it with the same hash. Any other
// Staging Snapshot is corrupt, or was interrupted before its scan finished, so
// it is discarded and nil is returned.
func (s *Service) findStaging(ctx context.Context, jobID string) (*stagedImport, error) {
	var snapshotID, manifestJSON string
	err := s.db.QueryRowContext(ctx, `SELECT id,manifest_json FROM project_snapshots WHERE job_id=? AND state='staging' LIMIT 1`, jobID).Scan(&snapshotID, &manifestJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT path,sha FROM project_files WHERE snapshot_id=?`, snapshotID)
	if err != nil {
		return nil, err
	}
	files := map[string]string{}
	for rows.Next() {
		var path, sha string
		if err := rows.Scan(&path, &sha); err != nil {
			rows.Close()
			return nil, err
		}
		files[path] = sha
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	entries, ok := manifest.Parse(manifestJSON)
	if ok && manifest.StoredMatch(files, entries) {
		stored := make(map[string]bool, len(files))
		for path := range files {
			stored[path] = true
		}
		return &stagedImport{snapshotID: snapshotID, manifest: entries, stored: stored}, nil
	}
	return nil, s.discardStaging(ctx, jobID)
}

func (s *Service) discardStaging(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_snapshots WHERE job_id=? AND state='staging'`, jobID)
	return err
}

// storeStaged finishes an interrupted Import from its Staging Snapshot,
// cloning only when the manifest lists files that are not stored yet. It
// returns errStagingCorrupt when the clone disagrees with the manifest.
func (s *Service) storeStaged(ctx context.Context, run *importRun, staged *stagedImport) error {
	pending := staged.pending()
	if len(pending) == 0 {
		if err := s.update(ctx, run, "storing", 95); err != nil {
			return err
		}
		return s.completeImport(ctx, run, staged.snapshotID, staged.manifest)
	}
	tree, err := s.cloneCommit(ctx, run)
	if err != nil {
		return err
	}
	defer tree.close()
	want := make(map[string]bool, len(pending))
	for _, entry := range pending {
		want[entry.Path] = true
	}
	files, err := tree.files(want)
	if err != nil {
		return s.fail(ctx, run, "repository scan failed", err)
	}
	if !manifest.Same(manifestOf(files), pending) {
		return errStagingCorrupt
	}
	if err = s.update(ctx, run, "storing", 70); err != nil {
		return err
	}
	if err = s.storeFiles(ctx, run, staged.snapshotID, files); err != nil {
		return err
	}
	return s.completeImport(ctx, run, staged.snapshotID, staged.manifest)
}

// importFromClone imports the commit from a fresh clone: it scans the files,
// starts a Staging Snapshot that keeps the manifest, and stores every file.
func (s *Service) importFromClone(ctx context.Context, run *importRun) error {
	tree, err := s.cloneCommit(ctx, run)
	if err != nil {
		return err
	}
	defer tree.close()
	if err = s.update(ctx, run, "scanning", 45); err != nil {
		return err
	}
	files, err := tree.files(nil)
	if err != nil {
		return s.fail(ctx, run, "repository scan failed", err)
	}
	found := scanFiles(files)
	metadata, err := json.Marshal(snapshotMetadata{Repository: run.repository, Scan: found})
	if err != nil {
		return s.fail(ctx, run, "repository metadata could not be encoded", err)
	}
	entries := manifestOf(files)
	manifestJSON, err := json.Marshal(entries)
	if err != nil {
		return s.fail(ctx, run, "repository manifest could not be encoded", err)
	}
	snapshotID := newID()
	_, err = s.db.ExecContext(ctx, `INSERT INTO project_snapshots(id,project_id,owner_id,repository_id,repository,branch,commit_sha,default_branch,language,runtime,stars,forks,metadata_json,manifest_json,job_id,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'staging')`,
		snapshotID, run.job.ProjectID, run.job.OwnerID, run.repository.ID, run.repository.FullName, run.job.Branch, run.job.Commit, run.repository.DefaultBranch,
		scan.DominantLanguage(found.Languages), found.Runtime, run.repository.Stars, run.repository.Forks, string(metadata), string(manifestJSON), run.job.ID)
	if err != nil {
		return s.fail(ctx, run, "snapshot could not be initialized", err)
	}
	if err = s.update(ctx, run, "storing", 70); err != nil {
		return err
	}
	if err = s.storeFiles(ctx, run, snapshotID, files); err != nil {
		return err
	}
	return s.completeImport(ctx, run, snapshotID, entries)
}

// cloneCommit fetches the commit and returns its tree. The caller closes it.
func (s *Service) cloneCommit(ctx context.Context, run *importRun) (*tree, error) {
	if err := s.update(ctx, run, "cloning", 15); err != nil {
		return nil, err
	}
	tree, err := s.source.open(ctx, run.job.Repository, run.job.Branch, run.job.Commit, run.token)
	if err != nil {
		var failure *sourceError
		if errors.As(err, &failure) {
			return nil, s.fail(ctx, run, failure.Message, failure.Err)
		}
		return nil, s.fail(ctx, run, "repository clone failed", err)
	}
	return tree, nil
}

// storeFiles saves files into the Staging Snapshot in batches, reporting
// progress.
func (s *Service) storeFiles(ctx context.Context, run *importRun, snapshotID string, files []file) error {
	for start := 0; start < len(files); start += 100 {
		end := min(start+100, len(files))
		err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
			stmt, err := tx.PrepareContext(ctx, `INSERT INTO project_files(snapshot_id,path,mode,content,sha) VALUES(?,?,?,?,?)`)
			if err != nil {
				return err
			}
			defer stmt.Close()
			for _, f := range files[start:end] {
				if _, err = stmt.ExecContext(ctx, snapshotID, f.Path, f.Mode, f.Content, f.Hash); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return s.fail(ctx, run, "snapshot storage failed", err)
		}
		_ = s.update(ctx, run, "storing", 70+(25*end)/max(1, len(files)))
	}
	return nil
}

// completeImport makes the Staging Snapshot the Project's active Snapshot,
// completes the Import and drops all but the newest five Snapshots, telling
// the other services which went. The totals come from the manifest.
func (s *Service) completeImport(ctx context.Context, run *importRun, snapshotID string, entries []manifest.Entry) error {
	var total int64
	for _, entry := range entries {
		total += entry.Size
	}
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE project_snapshots SET is_active=0 WHERE project_id=? AND state='ready'`, run.job.ProjectID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE project_snapshots SET file_count=?,total_bytes=?,state='ready',is_active=1 WHERE id=? AND state='staging'`, len(entries), total, snapshotID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("staging snapshot not found")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=?`, run.job.ProjectID); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `UPDATE project_import_jobs SET status='complete',progress=100,error='',updated_at=CURRENT_TIMESTAMP WHERE id=? AND status='storing'`, run.job.ID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("import was cancelled before it completed")
		}
		return pruneSnapshots(ctx, tx, run.job.ProjectID, run.job.OwnerID)
	})
	if err != nil {
		return s.fail(ctx, run, "snapshot finalization failed", err)
	}
	run.status = "complete"
	s.relay.Kick()
	return nil
}

// pruneSnapshots deletes the Project's ready Snapshots beyond the newest five
// and adds an event for each.
func pruneSnapshots(ctx context.Context, tx *sql.Tx, projectID, ownerID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM project_snapshots WHERE project_id=? AND state='ready' ORDER BY created_at DESC,id DESC LIMIT -1 OFFSET ?`, projectID, snapshotsKept)
	if err != nil {
		return err
	}
	var old []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		old = append(old, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range old {
		if _, err := tx.ExecContext(ctx, `DELETE FROM project_snapshots WHERE id=?`, id); err != nil {
			return err
		}
		event := contractprojects.SnapshotDeletedEvent{SnapshotID: id, ProjectID: projectID, OwnerID: ownerID}
		if err := addEvent(ctx, tx, contractprojects.SubjectSnapshotDeleted, "snapshot_deleted:"+id, event); err != nil {
			return err
		}
	}
	return nil
}

// manifestOf lists files as manifest entries.
func manifestOf(files []file) []manifest.Entry {
	entries := make([]manifest.Entry, 0, len(files))
	for _, f := range files {
		entries = append(entries, manifest.Entry{Path: f.Path, Mode: f.Mode, Hash: f.Hash, Size: int64(len(f.Content))})
	}
	return entries
}

func scanFiles(files []file) scan.Result {
	input := make([]scan.File, len(files))
	for i, f := range files {
		input[i] = scan.File{Path: f.Path, Content: f.Content}
	}
	return scan.Files(input)
}
