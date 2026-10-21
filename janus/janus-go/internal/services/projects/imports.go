package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	contractprojects "janus/internal/contract/projects"
	"janus/internal/domain/jobs"
	"janus/internal/platform/tursodb"
)

// importConsumer is the durable consumer of Import jobs in JOBS.
const importConsumer = "projects-imports"

// maxDeliveries is how many times an Import is handed to a worker. One whose
// workers keep dying fails on its last delivery instead of waiting forever.
const maxDeliveries = 4

// requestImport checks the repository, branch and commit with GitHub, records
// the Import and queues its job, in one transaction.
func (s *Service) requestImport(ctx context.Context, request contractprojects.RequestImport) (contractprojects.ImportQueued, error) {
	if request.OwnerID == "" || request.ProjectID == "" || request.Repository == "" || request.Branch == "" || len(request.Commit) != 40 {
		return contractprojects.ImportQueued{}, invalid("project, repository, branch and full commit SHA are required")
	}
	if _, found, err := getProject(ctx, s.db, request.ProjectID, request.OwnerID); err != nil {
		return contractprojects.ImportQueued{}, err
	} else if !found {
		return contractprojects.ImportQueued{}, invalid("project not found")
	}
	token, err := s.connectedToken(ctx, request.OwnerID)
	if err != nil {
		return contractprojects.ImportQueued{}, err
	}
	repository, err := s.github.repository(ctx, token, request.Repository)
	if err != nil {
		return contractprojects.ImportQueued{}, invalid("%s", err.Error())
	}
	if repository.FullName != request.Repository {
		return contractprojects.ImportQueued{}, invalid("GitHub returned a different repository")
	}
	branches, err := s.github.branches(ctx, token, request.Repository)
	if err != nil {
		return contractprojects.ImportQueued{}, invalid("%s", err.Error())
	}
	if !hasBranch(branches, request.Branch) {
		return contractprojects.ImportQueued{}, invalid("selected branch does not belong to this repository")
	}
	commits, err := s.github.commits(ctx, token, request.Repository, request.Branch)
	if err != nil {
		return contractprojects.ImportQueued{}, invalid("%s", err.Error())
	}
	if !hasCommit(commits, request.Commit) {
		return contractprojects.ImportQueued{}, invalid("selected commit is not among the branch's 50 most recent commits")
	}
	jobID := newID()
	err = tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status) VALUES(?,?,?,?,?,?,'queued')`, jobID, request.OwnerID, request.ProjectID, repository.FullName, request.Branch, request.Commit)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
				return invalid("an import of this project is already in progress")
			}
			return err
		}
		return queueImport(ctx, tx, jobID)
	})
	if err != nil {
		return contractprojects.ImportQueued{}, err
	}
	s.relay.Kick()
	s.audit(ctx, "info", "project.import.queued", map[string]string{"user_id": request.OwnerID, "project_id": request.ProjectID, "job_id": jobID, "repository": request.Repository})
	return contractprojects.ImportQueued{JobID: jobID}, nil
}

// queueImport adds the job message of an Import to the outbox in tx.
func queueImport(ctx context.Context, tx *sql.Tx, jobID string) error {
	return addEvent(ctx, tx, contractprojects.SubjectImportJob, "import:"+jobID, contractprojects.ImportJobMessage{JobID: jobID})
}

func hasBranch(branches []contractprojects.Branch, name string) bool {
	for _, branch := range branches {
		if branch.Name == name {
			return true
		}
	}
	return false
}

func hasCommit(commits []contractprojects.Commit, sha string) bool {
	for _, commit := range commits {
		if strings.EqualFold(commit.SHA, sha) {
			return true
		}
	}
	return false
}

// retryImport starts a new Import from a failed one, with the same Project,
// repository, branch and commit.
func (s *Service) retryImport(ctx context.Context, key contractprojects.ImportKey) (contractprojects.ImportQueued, error) {
	job, found, err := s.findImportJob(ctx, key.JobID, key.OwnerID)
	switch {
	case err != nil:
		return contractprojects.ImportQueued{}, err
	case !found:
		return contractprojects.ImportQueued{}, invalid("import job not found")
	case job.Status != "failed":
		return contractprojects.ImportQueued{}, invalid("only a failed import can be retried")
	}
	return s.requestImport(ctx, contractprojects.RequestImport{OwnerID: key.OwnerID, ProjectID: job.ProjectID, Repository: job.Repository, Branch: job.Branch, Commit: job.Commit})
}

func (s *Service) findImport(ctx context.Context, key contractprojects.ImportKey) (contractprojects.ImportFound, error) {
	job, found, err := s.findImportJob(ctx, key.JobID, key.OwnerID)
	return contractprojects.ImportFound{Found: found, Job: job}, err
}

func (s *Service) findImportJob(ctx context.Context, id, owner string) (contractprojects.ImportJob, bool, error) {
	query := `SELECT id,owner_id,project_id,repository,branch,commit_sha,status,progress,error,created_at,updated_at FROM project_import_jobs WHERE id=?`
	args := []any{id}
	if owner != "" {
		query += ` AND owner_id=?`
		args = append(args, owner)
	}
	var job contractprojects.ImportJob
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&job.ID, &job.OwnerID, &job.ProjectID, &job.Repository, &job.Branch, &job.Commit, &job.Status, &job.Progress, &job.Error, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return contractprojects.ImportJob{}, false, nil
	}
	return job, err == nil, err
}

func (s *Service) activeImports(ctx context.Context, owner contractprojects.Owner) (contractprojects.ActiveImports, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,project_id,status FROM project_import_jobs WHERE owner_id=? AND status IN `+activeImportStatuses+` ORDER BY created_at,id`, owner.OwnerID)
	if err != nil {
		return contractprojects.ActiveImports{}, err
	}
	defer rows.Close()
	list := contractprojects.ActiveImports{Imports: []contractprojects.ActiveImport{}}
	for rows.Next() {
		var active contractprojects.ActiveImport
		if err := rows.Scan(&active.ID, &active.ProjectID, &active.Status); err != nil {
			return contractprojects.ActiveImports{}, err
		}
		list.Imports = append(list.Imports, active)
	}
	return list, rows.Err()
}

// transition moves an Import from the status it was seen in to another, when
// domain/jobs allows it and nobody changed it meanwhile.
func (s *Service) transition(ctx context.Context, id, from, to string, progress int, reason string) error {
	if !jobs.Import.CanMove(from, to) {
		return fmt.Errorf("job cannot move from %s to %s", from, to)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE project_import_jobs SET status=?,progress=?,error=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`, to, progress, reason, id, from)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return errors.New("job changed while it was being updated")
	}
	return nil
}

// startWorkers starts Workers consumers of Import jobs, each taking one job at
// a time, so this process runs at most Workers Imports at once and leaves the
// rest in JOBS for other processes.
func (s *Service) startWorkers(ctx context.Context, stream jetstream.Stream) error {
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       importConsumer,
		FilterSubject: contractprojects.SubjectImportJob,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       s.cfg.AckWait,
		MaxDeliver:    maxDeliveries,
	})
	if err != nil {
		return fmt.Errorf("declare the import consumer: %w", err)
	}
	for range s.cfg.Workers {
		worker, err := consumer.Consume(s.handleImport, jetstream.PullMaxMessages(1))
		if err != nil {
			return fmt.Errorf("consume import jobs: %w", err)
		}
		s.workers = append(s.workers, worker)
	}
	return nil
}

// handleImport runs one delivery of an Import job. It acknowledges the job once
// the Import is finished, one way or the other; a job whose Import is not
// finished, because this process is stopping or the database failed, is left
// for JetStream to deliver again, and the Import resumes from where it got.
func (s *Service) handleImport(msg jetstream.Msg) {
	s.wg.Add(1)
	defer s.wg.Done()
	var queued contractprojects.ImportJobMessage
	if err := json.Unmarshal(msg.Data(), &queued); err != nil || queued.JobID == "" {
		_ = msg.Term()
		return
	}
	ctx := s.stopCtx
	job, found, err := s.findImportJob(ctx, queued.JobID, "")
	switch {
	case err != nil:
		_ = msg.NakWithDelay(time.Second)
		return
	case !found, jobs.Import.Terminal(job.Status):
		_ = msg.Ack()
		return
	}
	meta, err := msg.Metadata()
	last := err == nil && meta.NumDelivered >= maxDeliveries
	if last && job.Status != "queued" {
		s.failInterrupted(ctx, job, "resume limit reached after repeated interruptions during import")
		_ = msg.Ack()
		return
	}
	if job.Status != "queued" {
		// A worker stopped part-way; this one resumes from the Staging Snapshot.
		if err := s.transition(ctx, job.ID, job.Status, "queued", job.Progress, ""); err != nil {
			_ = msg.NakWithDelay(time.Second)
			return
		}
	}
	keepAlive := s.keepAlive(msg)
	err = s.runImport(ctx, job)
	keepAlive()
	if s.closing.Load() {
		return // not acknowledged: JetStream hands it out again
	}
	after, found, findErr := s.findImportJob(context.Background(), job.ID, "")
	switch {
	case findErr == nil && (!found || jobs.Import.Terminal(after.Status)):
		_ = msg.Ack()
		s.finished(job, err)
	case findErr == nil && last:
		// No delivery is left to finish it: fail it rather than leave it queued.
		s.failInterrupted(context.Background(), after, "import could not be run; retry it")
		_ = msg.Ack()
		s.finished(job, err)
	default:
		_ = msg.NakWithDelay(5 * time.Second)
	}
}

// keepAlive tells JetStream the job is still being worked on until the
// returned function is called.
func (s *Service) keepAlive(msg jetstream.Msg) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(s.cfg.AckWait / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_ = msg.InProgress()
			}
		}
	}()
	return func() { close(done) }
}

// finished audits how an Import ended.
func (s *Service) finished(job contractprojects.ImportJob, err error) {
	ctx := context.Background()
	if err != nil {
		s.audit(ctx, "error", "project.import.failed", map[string]string{"user_id": job.OwnerID, "project_id": job.ProjectID, "job_id": job.ID, "reason": "import_failed"})
		return
	}
	s.audit(ctx, "info", "project.import.completed", map[string]string{"user_id": job.OwnerID, "project_id": job.ProjectID, "job_id": job.ID, "repository": job.Repository, "commit": job.Commit})
}

// failInterrupted fails an Import that was interrupted too often, and drops
// its Staging Snapshot.
func (s *Service) failInterrupted(ctx context.Context, job contractprojects.ImportJob, reason string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE project_import_jobs SET status='failed',progress=100,error=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`, reason, job.ID, job.Status)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM project_snapshots WHERE job_id=? AND state='staging'`, job.ID)
}

// track registers a running Import so its Owner's deletion can cancel it, and
// returns its context and the function that unregisters it.
func (s *Service) track(parent context.Context, job contractprojects.ImportJob) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(parent, s.cfg.ImportTimeout)
	s.mu.Lock()
	s.running[job.ID] = runningImport{owner: job.OwnerID, cancel: cancel}
	s.mu.Unlock()
	return ctx, func() {
		s.mu.Lock()
		delete(s.running, job.ID)
		s.mu.Unlock()
		cancel()
	}
}

// cancelOwner cancels the Owner's Imports running in this process.
func (s *Service) cancelOwner(owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, running := range s.running {
		if running.owner == owner {
			running.cancel()
		}
	}
}
