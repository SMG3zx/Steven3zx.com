package projects

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	goakt "github.com/tochemey/goakt/v4/actor"

	"janus/actors/persistence"
	"janus/internal/app"
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

// manifestEntry describes one file of the selected commit. A Staging Snapshot
// keeps the manifest of its commit, so an interrupted import knows which files
// it still has to store without scanning the repository again.
type manifestEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// errStagingCorrupt reports that a Staging Snapshot does not match the commit it
// was started from. The import discards it and starts again from the clone.
var errStagingCorrupt = errors.New("staging snapshot does not match the commit")

// stagedImport is the Staging Snapshot of an interrupted import, after its
// manifest and stored files have been checked against each other.
type stagedImport struct {
	snapshotID string
	manifest   []manifestEntry
	stored     map[string]bool
}

// pending returns the manifest entries whose files are not stored yet.
func (s *stagedImport) pending() []manifestEntry {
	pending := make([]manifestEntry, 0, len(s.manifest))
	for _, entry := range s.manifest {
		if !s.stored[entry.Path] {
			pending = append(pending, entry)
		}
	}
	return pending
}

func (a *Actor) resumeImports(ctx context.Context, system goakt.ActorSystem) (int, error) {
	jobs, err := a.store.ListResumableImports(ctx)
	if err != nil {
		return 0, err
	}
	for _, job := range jobs {
		msg, err := a.resumedImportRun(ctx, job)
		if err != nil {
			a.failResumedImport(ctx, job.JobID, err)
			continue
		}
		a.queueImport(ctx, system, msg)
	}
	return len(jobs), nil
}

// resumedImportRun prepares the run message for an interrupted import. The
// repository is read again from GitHub, because the job stores only its name.
func (a *Actor) resumedImportRun(ctx context.Context, job persistence.ResumableImport) (*RunImport, error) {
	token, connected, err := a.github.Token(ctx, job.OwnerID)
	if err != nil {
		return nil, err
	}
	if !connected {
		return nil, errors.New("reconnect GitHub to resume this import")
	}
	repository, err := a.github.API.Repository(ctx, token, job.Repository)
	if err != nil {
		return nil, err
	}
	return &RunImport{JobID: job.JobID, OwnerID: job.OwnerID, ProjectID: job.ProjectID, Repository: repository, Branch: job.Branch, Commit: job.Commit, Token: token}, nil
}

func (a *Actor) failResumedImport(ctx context.Context, jobID string, cause error) {
	_ = a.updateJob(ctx, jobID, "failed", 100, "resume failed: "+cause.Error())
	_ = a.store.DiscardJobStaging(ctx, jobID)
}

// queueImport starts msg once an import slot is free, so resumed imports never
// exceed the concurrency limit. It does not block the caller.
func (a *Actor) queueImport(ctx context.Context, system goakt.ActorSystem, msg *RunImport) {
	worker := &ImportWorker{store: a.store, source: a.source, audit: a.audit, runner: a.runner}
	a.runner.Queue(ctx, system, importWorkerName(msg.JobID), worker, msg, func(err error) { a.failResumedImport(ctx, msg.JobID, err) })
}

func importWorkerName(jobID string) string {
	return "project-import-" + strings.ReplaceAll(jobID, "-", "")
}

func (a *Actor) retryImport(rctx *goakt.ReceiveContext, msg *RetryImport) (string, error) {
	ctx := rctx.Context()
	job, found, err := a.store.FindImportJob(ctx, msg.JobID, msg.OwnerID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("import job not found")
	}
	if job.Status != "failed" {
		return "", errors.New("only a failed import can be retried")
	}
	return a.startImport(rctx, &RequestImport{OwnerID: msg.OwnerID, ProjectID: job.ProjectID, Repository: job.Repository, Branch: job.Branch, Commit: job.Commit})
}

func (a *Actor) deleteAccount(ctx context.Context, msg *DeleteAccount) (AccountDeleted, error) {
	result, err := a.store.DeleteAccount(ctx, msg.OwnerID, msg.CancelActiveJobs)
	if err != nil && !persistence.IsReply(err) {
		return AccountDeleted{}, err
	}
	return AccountDeleted{Deleted: result.Deleted, ActiveJobs: result.ActiveJobs, Err: err}, nil
}

// findStaging returns the Staging Snapshot of an interrupted import when it has
// a manifest and every stored file is listed in that manifest with the same
// hash. Any other Staging Snapshot is corrupt, or was interrupted before its
// scan finished, so it is discarded and nil is returned.
func (a *ImportWorker) findStaging(ctx context.Context, msg *RunImport) (*stagedImport, error) {
	found, err := a.store.FindStagingSnapshot(ctx, msg.JobID)
	if err != nil {
		return nil, err
	}
	if !found.Found {
		return nil, nil
	}
	manifest, ok := parseManifest(found.Manifest)
	if ok && storedFilesMatch(found.Files, manifest) {
		stored := make(map[string]bool, len(found.Files))
		for path := range found.Files {
			stored[path] = true
		}
		return &stagedImport{snapshotID: found.SnapshotID, manifest: manifest, stored: stored}, nil
	}
	if err = a.store.DiscardJobStaging(ctx, msg.JobID); err != nil {
		return nil, err
	}
	return nil, nil
}

// parseManifest decodes the manifest of a Staging Snapshot. An empty manifest
// means that the scan never finished, so ok is false.
func parseManifest(raw string) ([]manifestEntry, bool) {
	if raw == "" {
		return nil, false
	}
	var manifest []manifestEntry
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return nil, false
	}
	return manifest, true
}

// storedFilesMatch reports whether every stored file is listed in the manifest
// with the same hash. A Staging Snapshot that fails this check is corrupt or
// from a different commit, so it must not be reused.
func storedFilesMatch(stored map[string]string, manifest []manifestEntry) bool {
	hashes := make(map[string]string, len(manifest))
	for _, entry := range manifest {
		hashes[entry.Path] = entry.Hash
	}
	for path, sha := range stored {
		hash, ok := hashes[path]
		if !ok || hash != sha {
			return false
		}
	}
	return true
}

// filesMatchManifest reports whether files are exactly the entries of manifest,
// with the same hashes.
func filesMatchManifest(files []app.SnapshotFile, manifest []manifestEntry) bool {
	if len(files) != len(manifest) {
		return false
	}
	hashes := make(map[string]string, len(manifest))
	for _, entry := range manifest {
		hashes[entry.Path] = entry.Hash
	}
	for _, file := range files {
		hash, ok := hashes[file.Path]
		if !ok || hash != file.Hash {
			return false
		}
	}
	return true
}

// manifestOf lists files as manifest entries.
func manifestOf(files []app.SnapshotFile) []manifestEntry {
	manifest := make([]manifestEntry, 0, len(files))
	for _, file := range files {
		manifest = append(manifest, manifestEntry{Path: file.Path, Mode: file.Mode, Hash: file.Hash, Size: int64(len(file.Content))})
	}
	return manifest
}

func (a *ImportWorker) runImport(parent context.Context, msg *RunImport) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Minute)
	defer cancel()
	staged, err := a.findStaging(ctx, msg)
	if err != nil {
		return a.fail(ctx, msg, "snapshot could not be initialized", err)
	}
	if staged != nil {
		err = a.storeStaged(ctx, msg, staged)
		if !errors.Is(err, errStagingCorrupt) {
			return err
		}
		if err = a.store.DiscardJobStaging(ctx, msg.JobID); err != nil {
			return a.fail(ctx, msg, "snapshot could not be initialized", err)
		}
	}
	return a.importFromClone(ctx, msg)
}

// storeStaged finishes an interrupted import from its Staging Snapshot. The
// repository is cloned only when the manifest lists files that are not stored.
// It returns errStagingCorrupt when the clone disagrees with the manifest.
func (a *ImportWorker) storeStaged(ctx context.Context, msg *RunImport, staged *stagedImport) error {
	pending := staged.pending()
	if len(pending) == 0 {
		if err := a.updateJob(ctx, msg.JobID, "storing", 95, ""); err != nil {
			return err
		}
		return a.completeImport(ctx, msg, staged.snapshotID, staged.manifest)
	}
	tree, err := a.cloneCommit(ctx, msg)
	if err != nil {
		return err
	}
	defer tree.Close()
	want := make(map[string]bool, len(pending))
	for _, entry := range pending {
		want[entry.Path] = true
	}
	files, err := tree.Files(want)
	if err != nil {
		return a.fail(ctx, msg, "repository scan failed", err)
	}
	if !filesMatchManifest(files, pending) {
		return errStagingCorrupt
	}
	if err = a.updateJob(ctx, msg.JobID, "storing", 70, ""); err != nil {
		return err
	}
	if err = a.storeFiles(ctx, msg, staged.snapshotID, files); err != nil {
		return err
	}
	return a.completeImport(ctx, msg, staged.snapshotID, staged.manifest)
}

// importFromClone imports the selected commit from a fresh clone. It scans the
// repository, starts a Staging Snapshot that keeps the manifest, and stores
// every file.
func (a *ImportWorker) importFromClone(ctx context.Context, msg *RunImport) error {
	tree, err := a.cloneCommit(ctx, msg)
	if err != nil {
		return err
	}
	defer tree.Close()
	if err = a.updateJob(ctx, msg.JobID, "scanning", 45, ""); err != nil {
		return err
	}
	files, err := tree.Files(nil)
	if err != nil {
		return a.fail(ctx, msg, "repository scan failed", err)
	}
	scan := scanFiles(files)
	metadata, err := json.Marshal(snapshotMetadata{Repository: msg.Repository, Scan: scan})
	if err != nil {
		return a.fail(ctx, msg, "repository metadata could not be encoded", err)
	}
	manifest := manifestOf(files)
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return a.fail(ctx, msg, "repository manifest could not be encoded", err)
	}
	snapshotID, err := a.beginStaging(ctx, msg, scan, metadata, manifestJSON)
	if err != nil {
		return a.fail(ctx, msg, "snapshot could not be initialized", err)
	}
	if err = a.updateJob(ctx, msg.JobID, "storing", 70, ""); err != nil {
		return err
	}
	if err = a.storeFiles(ctx, msg, snapshotID, files); err != nil {
		return err
	}
	return a.completeImport(ctx, msg, snapshotID, manifest)
}

// cloneCommit fetches the selected commit and returns its tree. The caller must
// call Close on it after it has read the files.
func (a *ImportWorker) cloneCommit(ctx context.Context, msg *RunImport) (app.SourceTree, error) {
	if err := a.updateJob(ctx, msg.JobID, "cloning", 15, ""); err != nil {
		return nil, err
	}
	tree, err := a.source.Open(ctx, app.SourceRequest{Repository: msg.Repository.FullName, Branch: msg.Branch, Commit: msg.Commit, Token: msg.Token})
	if err != nil {
		var failure *app.SourceError
		if errors.As(err, &failure) {
			return nil, a.fail(ctx, msg, failure.Message, failure.Err)
		}
		return nil, a.fail(ctx, msg, "repository clone failed", err)
	}
	return tree, nil
}

// beginStaging starts a Staging Snapshot for the commit and returns its ID.
func (a *ImportWorker) beginStaging(ctx context.Context, msg *RunImport, scan ScanResult, metadata, manifest []byte) (string, error) {
	snapshotID := uuid.NewString()
	err := a.store.BeginSnapshot(ctx, persistence.BeginSnapshot{ID: snapshotID, JobID: msg.JobID, OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, RepositoryID: msg.Repository.ID, Repository: msg.Repository.FullName, Branch: msg.Branch, Commit: msg.Commit, DefaultBranch: msg.Repository.DefaultBranch, Language: dominantLanguage(scan.Languages), Runtime: scan.Runtime, Stars: msg.Repository.Stars, Forks: msg.Repository.Forks, MetadataJSON: string(metadata), Manifest: string(manifest)})
	if err != nil {
		return "", err
	}
	return snapshotID, nil
}

// storeFiles saves files into the Staging Snapshot in batches, reporting progress.
func (a *ImportWorker) storeFiles(ctx context.Context, msg *RunImport, snapshotID string, files []app.SnapshotFile) error {
	for start := 0; start < len(files); start += 100 {
		end := min(start+100, len(files))
		batch := make([]persistence.SnapshotFile, 0, end-start)
		for _, file := range files[start:end] {
			batch = append(batch, persistence.SnapshotFile{Path: file.Path, Mode: file.Mode, Content: file.Content, Hash: file.Hash})
		}
		saveErr := a.store.AddSnapshotFiles(ctx, snapshotID, batch)
		if saveErr != nil {
			return a.fail(ctx, msg, "snapshot storage failed", saveErr)
		}
		progress := 70 + (25*end)/max(1, len(files))
		_ = a.updateJob(ctx, msg.JobID, "storing", progress, "")
	}
	return nil
}

// completeImport makes the Staging Snapshot the active Snapshot of its Project.
// The totals come from the manifest, which describes every file of the commit.
func (a *ImportWorker) completeImport(ctx context.Context, msg *RunImport, snapshotID string, manifest []manifestEntry) error {
	var total int64
	for _, entry := range manifest {
		total += entry.Size
	}
	err := a.store.CompleteSnapshot(ctx, persistence.CompleteSnapshot{SnapshotID: snapshotID, JobID: msg.JobID, FileCount: len(manifest), TotalBytes: total})
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
