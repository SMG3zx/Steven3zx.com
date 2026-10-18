package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gitHTTP "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/uuid"
	goakt "github.com/tochemey/goakt/v4/actor"

	"janus/actors/persistence"
)

// ResumeImports starts every import that was interrupted by the previous
// shutdown. It runs once at startup, before any request is served.
type ResumeImports struct{}

// ImportsResumed reports how many interrupted imports were started again.
type ImportsResumed struct {
	Count int
	Err   error
}

// RetryImport starts a new import from a failed one, using the same project,
// repository, branch, and commit. The new job starts with no resumes.
type RetryImport struct{ OwnerID, JobID string }

// DeleteAccount deletes an Account, cancelling its active imports and builds
// first when CancelActiveJobs is set.
type DeleteAccount struct {
	OwnerID          string
	CancelActiveJobs bool
}

// AccountDeleted reports the result of DeleteAccount. When Deleted is false
// and ActiveJobs is not empty, the Account still has unfinished jobs.
type AccountDeleted struct {
	Deleted    bool
	ActiveJobs []persistence.ActiveJob
	Err        error
}

func (a *Actor) resumeImports(ctx context.Context, system goakt.ActorSystem) (int, error) {
	response, err := a.ask(ctx, &persistence.ListResumableImports{})
	if err != nil {
		return 0, err
	}
	listed, ok := response.(*persistence.ResumableImportsListed)
	if !ok {
		return 0, fmt.Errorf("unexpected persistence response %T", response)
	}
	if listed.Err != nil {
		return 0, listed.Err
	}
	for _, job := range listed.Jobs {
		msg, err := a.resumedImportRun(ctx, job)
		if err != nil {
			a.failResumedImport(ctx, job.JobID, err)
			continue
		}
		a.queueImport(ctx, system, msg)
	}
	return len(listed.Jobs), nil
}

// resumedImportRun prepares the run message for an interrupted import. The
// repository is read again from GitHub, because the job stores only its name.
func (a *Actor) resumedImportRun(ctx context.Context, job persistence.ResumableImport) (*RunImport, error) {
	token, connected, err := a.token(ctx, job.OwnerID)
	if err != nil {
		return nil, err
	}
	if !connected {
		return nil, errors.New("reconnect GitHub to resume this import")
	}
	repository, err := a.github.Repository(ctx, token, job.Repository)
	if err != nil {
		return nil, err
	}
	return &RunImport{JobID: job.JobID, OwnerID: job.OwnerID, ProjectID: job.ProjectID, Repository: repository, Branch: job.Branch, Commit: job.Commit, Token: token}, nil
}

func (a *Actor) failResumedImport(ctx context.Context, jobID string, cause error) {
	_ = a.updateJob(ctx, jobID, "failed", 100, "resume failed: "+cause.Error())
	_ = discardStaging(ctx, a.persistence, jobID)
}

// queueImport starts msg once an import slot is free, so resumed imports never
// exceed the concurrency limit. It does not block the caller.
func (a *Actor) queueImport(ctx context.Context, system goakt.ActorSystem, msg *RunImport) {
	go func() {
		a.slots <- struct{}{}
		if err := a.spawnImport(context.WithoutCancel(ctx), system, msg); err != nil {
			<-a.slots
			a.failResumedImport(ctx, msg.JobID, err)
		}
	}()
}

// spawnImport starts an import worker for msg. The caller must already hold an
// import slot; the worker releases it when it finishes.
func (a *Actor) spawnImport(ctx context.Context, system goakt.ActorSystem, msg *RunImport) error {
	a.jobs.Add(1)
	worker := &ImportWorker{persistence: a.persistence, accounting: a.accounting, jobs: a.jobs, slots: a.slots, cloneBase: a.cloneBase}
	pid, err := system.Spawn(ctx, "project-import-"+strings.ReplaceAll(msg.JobID, "-", ""), worker)
	if err != nil {
		a.jobs.Done()
		return err
	}
	if err = goakt.Tell(context.WithoutCancel(ctx), pid, msg); err != nil {
		a.jobs.Done()
		return err
	}
	return nil
}

func (a *Actor) retryImport(rctx *goakt.ReceiveContext, msg *RetryImport) (string, error) {
	ctx := rctx.Context()
	response, err := a.ask(ctx, &persistence.FindImportJob{ID: msg.JobID, OwnerID: msg.OwnerID})
	if err != nil {
		return "", err
	}
	found, ok := response.(*persistence.ImportJobFound)
	if !ok {
		return "", fmt.Errorf("unexpected persistence response %T", response)
	}
	if found.Err != nil {
		return "", found.Err
	}
	if !found.Found {
		return "", errors.New("import job not found")
	}
	if found.Job.Status != "failed" {
		return "", errors.New("only a failed import can be retried")
	}
	return a.startImport(rctx, &RequestImport{OwnerID: msg.OwnerID, ProjectID: found.Job.ProjectID, Repository: found.Job.Repository, Branch: found.Job.Branch, Commit: found.Job.Commit})
}

func (a *Actor) deleteAccount(ctx context.Context, msg *DeleteAccount) (AccountDeleted, error) {
	response, err := a.ask(ctx, &persistence.DeleteAccount{UserID: msg.OwnerID, CancelActiveJobs: msg.CancelActiveJobs})
	if err != nil {
		return AccountDeleted{}, err
	}
	result, ok := response.(*persistence.AccountDeleted)
	if !ok {
		return AccountDeleted{}, fmt.Errorf("unexpected persistence response %T", response)
	}
	return AccountDeleted{Deleted: result.Deleted, ActiveJobs: result.ActiveJobs, Err: result.Err}, nil
}

func discardStaging(ctx context.Context, persistencePID *goakt.PID, jobID string) error {
	response, err := askPersistence(ctx, persistencePID, &persistence.DiscardJobStaging{JobID: jobID})
	if err != nil {
		return err
	}
	if result, ok := response.(*persistence.JobStagingDiscarded); ok {
		return result.Err
	}
	return fmt.Errorf("unexpected persistence response %T", response)
}

// stagingSnapshot returns the Staging Snapshot that the import keeps writing. An
// interrupted import reuses its Staging Snapshot only when every file already
// stored in it matches the commit. Otherwise the Staging Snapshot is discarded
// and a new one begins. It also returns the paths that are already stored.
func (a *ImportWorker) stagingSnapshot(ctx context.Context, msg *RunImport, files []fileData, metadata []byte, scan ScanResult) (string, map[string]bool, error) {
	response, err := a.ask(ctx, &persistence.FindStagingSnapshot{JobID: msg.JobID})
	if err != nil {
		return "", nil, err
	}
	found, ok := response.(*persistence.StagingSnapshotFound)
	if !ok {
		return "", nil, fmt.Errorf("unexpected persistence response %T", response)
	}
	if found.Err != nil {
		return "", nil, found.Err
	}
	if found.Found {
		if storedFilesMatch(found.Files, files) {
			stored := make(map[string]bool, len(found.Files))
			for path := range found.Files {
				stored[path] = true
			}
			return found.SnapshotID, stored, nil
		}
		if err = discardStaging(ctx, a.persistence, msg.JobID); err != nil {
			return "", nil, err
		}
	}
	snapshotID := uuid.NewString()
	begin, err := a.ask(ctx, &persistence.BeginSnapshot{ID: snapshotID, JobID: msg.JobID, OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, RepositoryID: msg.Repository.ID, Repository: msg.Repository.FullName, Branch: msg.Branch, Commit: msg.Commit, DefaultBranch: msg.Repository.DefaultBranch, Language: dominantLanguage(scan.Languages), Stars: msg.Repository.Stars, Forks: msg.Repository.Forks, MetadataJSON: string(metadata)})
	if err != nil {
		return "", nil, err
	}
	result, ok := begin.(*persistence.BeginSnapshotResult)
	if !ok {
		return "", nil, fmt.Errorf("unexpected persistence response %T", begin)
	}
	if result.Err != nil {
		return "", nil, result.Err
	}
	return snapshotID, map[string]bool{}, nil
}

// storedFilesMatch reports whether every stored file is in the commit with the
// same hash. A Staging Snapshot that fails this check is corrupt or from a
// different commit, so it must not be reused.
func storedFilesMatch(stored map[string]string, files []fileData) bool {
	hashes := make(map[string]string, len(files))
	for _, file := range files {
		hashes[file.path] = file.hash
	}
	for path, sha := range stored {
		if hashes[path] != sha {
			return false
		}
	}
	return true
}

func (a *ImportWorker) runImport(parent context.Context, msg *RunImport) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Minute)
	defer cancel()
	if err := a.updateJob(ctx, msg.JobID, "cloning", 15, ""); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "janus-github-import-")
	if err != nil {
		return a.fail(ctx, msg, "could not allocate import workspace", err)
	}
	defer os.RemoveAll(dir)
	remote := a.cloneBase + msg.Repository.FullName + ".git"
	_, err = git.PlainCloneContext(ctx, dir, true, &git.CloneOptions{URL: remote, Auth: &gitHTTP.BasicAuth{Username: "x-access-token", Password: msg.Token}, ReferenceName: plumbing.NewBranchReferenceName(msg.Branch), SingleBranch: true, Depth: cloneDepth, Tags: git.NoTags})
	if err != nil {
		return a.fail(ctx, msg, "repository clone failed", err)
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return a.fail(ctx, msg, "cloned repository could not be opened", err)
	}
	commit, err := repo.CommitObject(plumbing.NewHash(msg.Commit))
	if err != nil {
		return a.fail(ctx, msg, "selected commit was not found in the fetched history", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return a.fail(ctx, msg, "selected commit tree could not be read", err)
	}
	if err = a.updateJob(ctx, msg.JobID, "scanning", 45, ""); err != nil {
		return err
	}
	files, total, err := readSnapshotTree(tree)
	if err != nil {
		return a.fail(ctx, msg, "repository scan failed", err)
	}
	scan := scanFiles(files)
	metadata, err := json.Marshal(snapshotMetadata{Repository: msg.Repository, Scan: scan})
	if err != nil {
		return a.fail(ctx, msg, "repository metadata could not be encoded", err)
	}
	snapshotID, stored, err := a.stagingSnapshot(ctx, msg, files, metadata, scan)
	if err != nil {
		return a.fail(ctx, msg, "snapshot could not be initialized", err)
	}
	if err = a.updateJob(ctx, msg.JobID, "storing", 70, ""); err != nil {
		return err
	}
	pending := make([]fileData, 0, len(files))
	for _, file := range files {
		if !stored[file.path] {
			pending = append(pending, file)
		}
	}
	for start := 0; start < len(pending); start += 100 {
		end := min(start+100, len(pending))
		batch := make([]persistence.SnapshotFile, 0, end-start)
		for _, file := range pending[start:end] {
			batch = append(batch, persistence.SnapshotFile{Path: file.path, Mode: file.mode, Content: file.content, Hash: file.hash})
		}
		response, saveErr := a.ask(ctx, &persistence.AddSnapshotFiles{SnapshotID: snapshotID, Files: batch})
		if saveErr == nil {
			if result, ok := response.(*persistence.SnapshotFilesAdded); ok {
				saveErr = result.Err
			} else {
				saveErr = fmt.Errorf("unexpected persistence response %T", response)
			}
		}
		if saveErr != nil {
			return a.fail(ctx, msg, "snapshot storage failed", saveErr)
		}
		progress := 70 + (25*end)/max(1, len(pending))
		_ = a.updateJob(ctx, msg.JobID, "storing", progress, "")
	}
	response, err := a.ask(ctx, &persistence.CompleteSnapshot{SnapshotID: snapshotID, JobID: msg.JobID, FileCount: len(files), TotalBytes: total})
	if err == nil {
		if result, ok := response.(*persistence.SnapshotCompleted); ok {
			err = result.Err
		} else {
			err = fmt.Errorf("unexpected persistence response %T", response)
		}
	}
	if err != nil {
		return a.fail(ctx, msg, "snapshot finalization failed", err)
	}
	return nil
}

// dominantLanguage returns the recognized language with the most files, or ""
// when none was recognized. Ties go to the name that sorts first, so the same
// repository always gets the same language.
func dominantLanguage(languages map[string]int) string {
	best, bestCount := "", 0
	for name, count := range languages {
		if count > bestCount || (count == bestCount && count > 0 && name < best) {
			best, bestCount = name, count
		}
	}
	return best
}

func askPersistence(ctx context.Context, persistencePID *goakt.PID, message any) (any, error) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return goakt.Ask(c, persistencePID, message, 30*time.Second)
}
