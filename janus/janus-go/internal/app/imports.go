package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"janus/internal/domain/scan"
)

const (
	maxConcurrentImports = 2
	importTimeout        = 20 * time.Minute
)

// ImportRequest asks for a commit of a repository to be imported into a Project.
type ImportRequest struct {
	OwnerID, ProjectID, Repository, Branch, Commit string
}

// Imports brings a repository's files into a Project as a Snapshot. An import
// runs on a goroutine the use case owns, at most two at once, and is recorded in
// the Store as it moves through its states, so a restart can resume it.
type Imports struct {
	store  Store
	host   GitHost
	source Source
	audit  Audit
	jobs   *JobRunner
}

// NewImports returns an Imports that records imports in store, checks the
// repository, branch and commit against host, fetches files through source and
// audits what happens to audit.
func NewImports(store Store, host GitHost, source Source, audit Audit) *Imports {
	return &Imports{store: store, host: host, source: source, audit: audit, jobs: NewJobRunner(maxConcurrentImports)}
}

// importJob is one import to run.
type importJob struct {
	JobID, OwnerID, ProjectID string
	Repository                Repository
	Branch, Commit            string
	Token                     string
}

// snapshotMetadata is what a Snapshot keeps about where it came from and what
// the scan found in it.
type snapshotMetadata struct {
	Repository Repository  `json:"repository"`
	Scan       scan.Result `json:"scan"`
}

// Request checks the repository, branch and commit and starts an import of
// them into the Project. It returns the import job's ID, and a plain error whose
// text is meant for the Account when the import cannot start.
func (i *Imports) Request(ctx context.Context, request ImportRequest) (string, error) {
	if !i.jobs.Accepting() {
		return "", errors.New("project imports are shutting down")
	}
	if request.OwnerID == "" || request.ProjectID == "" || request.Repository == "" || request.Branch == "" || len(request.Commit) != 40 {
		return "", errors.New("project, repository, branch and full commit SHA are required")
	}
	_, found, err := i.store.GetProject(ctx, request.ProjectID, request.OwnerID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("project not found")
	}
	token, connected, err := i.store.FindGitHubToken(ctx, request.OwnerID)
	if err != nil {
		return "", err
	}
	if !connected {
		return "", errors.New("connect GitHub before importing a repository")
	}
	repository, err := i.host.Repository(ctx, token, request.Repository)
	if err != nil {
		return "", err
	}
	if repository.FullName != request.Repository {
		return "", errors.New("GitHub returned a different repository")
	}
	branches, err := i.host.Branches(ctx, token, request.Repository)
	if err != nil {
		return "", err
	}
	branchFound := false
	for _, branch := range branches {
		if branch.Name == request.Branch {
			branchFound = true
			break
		}
	}
	if !branchFound {
		return "", errors.New("selected branch does not belong to this repository")
	}
	commits, err := i.host.Commits(ctx, token, request.Repository, request.Branch)
	if err != nil {
		return "", err
	}
	commitFound := false
	for _, commit := range commits {
		if strings.EqualFold(commit.SHA, request.Commit) {
			commitFound = true
			break
		}
	}
	if !commitFound {
		return "", errors.New("selected commit is not among the branch's 50 most recent commits")
	}
	jobID := uuid.NewString()
	if err = i.store.CreateImportJob(ctx, CreateImportJob{ID: jobID, OwnerID: request.OwnerID, ProjectID: request.ProjectID, Repository: repository.FullName, Branch: request.Branch, Commit: request.Commit}); err != nil {
		return "", err
	}
	job := &importJob{JobID: jobID, OwnerID: request.OwnerID, ProjectID: request.ProjectID, Repository: repository, Branch: request.Branch, Commit: request.Commit, Token: token}
	if err = i.jobs.Start(i.runner(job), i.crashed(job)); err != nil {
		if errors.Is(err, ErrBusy) {
			_ = i.updateJob(ctx, jobID, "failed", 100, "too many imports are already running")
			return "", errors.New("two repository imports are already running; retry when one finishes")
		}
		_ = i.updateJob(ctx, jobID, "failed", 100, "import worker could not start")
		return "", err
	}
	i.record(ctx, "info", "project.import.queued", map[string]string{"user_id": request.OwnerID, "project_id": request.ProjectID, "job_id": jobID, "repository": request.Repository})
	return jobID, nil
}

// Retry starts a new import from a failed one, using the same Project,
// repository, branch and commit. The new job starts with no resumes.
func (i *Imports) Retry(ctx context.Context, owner, jobID string) (string, error) {
	job, found, err := i.store.FindImportJob(ctx, jobID, owner)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("import job not found")
	}
	if job.Status != "failed" {
		return "", errors.New("only a failed import can be retried")
	}
	return i.Request(ctx, ImportRequest{OwnerID: owner, ProjectID: job.ProjectID, Repository: job.Repository, Branch: job.Branch, Commit: job.Commit})
}

// Resume starts every import the previous shutdown interrupted, and returns how
// many there were. It runs once at startup, before any request is served.
func (i *Imports) Resume(ctx context.Context) (int, error) {
	jobs, err := i.store.ListResumableImports(ctx)
	if err != nil {
		return 0, err
	}
	for _, queued := range jobs {
		job, err := i.resumedJob(ctx, queued)
		if err != nil {
			i.failResumed(ctx, queued.JobID, err)
			continue
		}
		i.jobs.Queue(i.runner(job), i.crashed(job))
	}
	return len(jobs), nil
}

// resumedJob prepares the job for an interrupted import. The repository is read
// again from the host, because the job stores only its name.
func (i *Imports) resumedJob(ctx context.Context, queued ResumableImport) (*importJob, error) {
	token, connected, err := i.store.FindGitHubToken(ctx, queued.OwnerID)
	if err != nil {
		return nil, err
	}
	if !connected {
		return nil, errors.New("reconnect GitHub to resume this import")
	}
	repository, err := i.host.Repository(ctx, token, queued.Repository)
	if err != nil {
		return nil, err
	}
	return &importJob{JobID: queued.JobID, OwnerID: queued.OwnerID, ProjectID: queued.ProjectID, Repository: repository, Branch: queued.Branch, Commit: queued.Commit, Token: token}, nil
}

func (i *Imports) failResumed(ctx context.Context, jobID string, cause error) {
	_ = i.updateJob(ctx, jobID, "failed", 100, "resume failed: "+cause.Error())
	_ = i.store.DiscardJobStaging(ctx, jobID)
}

// SetEntryPoint records the Entrypoint the Account confirmed for the Project's
// latest Snapshot, when it is one of the candidates the scan found.
func (i *Imports) SetEntryPoint(ctx context.Context, owner, projectID, snapshotID, entryPoint string) error {
	project, found, err := i.store.GetProject(ctx, projectID, owner)
	if err != nil {
		return err
	}
	switch {
	case !found:
		return errors.New("project not found")
	case project.SnapshotID != snapshotID:
		return errors.New("entrypoint selection must target the latest snapshot")
	}
	if err = validateEntrypoint(project.MetadataJSON, entryPoint); err != nil {
		return err
	}
	return i.store.SetProjectEntryPoint(ctx, SetProjectEntryPoint{OwnerID: owner, ProjectID: projectID, SnapshotID: snapshotID, EntryPoint: entryPoint})
}

// CancelOwner stops the running imports of an Account, and returns how many.
// The Account's queued imports are cancelled in the Store by the caller.
func (i *Imports) CancelOwner(owner string) int { return i.jobs.CancelOwner(owner) }

// Drain makes Request refuse new imports and waits for the running ones, or
// until ctx ends.
func (i *Imports) Drain(ctx context.Context) error { return i.jobs.Drain(ctx) }

// Wait waits for the running and queued imports, or until ctx ends.
func (i *Imports) Wait(ctx context.Context) error { return i.jobs.Wait(ctx) }

func validateEntrypoint(metadataJSON, entrypoint string) error {
	var metadata snapshotMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return errors.New("project scan metadata is unavailable")
	}
	if entrypoint == "" {
		return nil
	}
	for _, candidate := range metadata.Scan.Candidates {
		if candidate.Path == entrypoint {
			return nil
		}
	}
	return errors.New("entrypoint must match a detected candidate")
}

// runner returns the function that runs job, and audits how it ended.
func (i *Imports) runner(job *importJob) func() {
	return func() { i.finish(job, i.run(context.Background(), job)) }
}

// crashed returns the function that records a panic in job as a failed import.
func (i *Imports) crashed(job *importJob) func(error) {
	return func(err error) {
		err = fmt.Errorf("repository import panicked: %v", err)
		ctx := context.Background()
		_ = i.updateJob(ctx, job.JobID, "failed", 100, err.Error())
		_ = i.store.DiscardJobStaging(ctx, job.JobID)
		i.finish(job, err)
	}
}

// finish audits how an import ended.
func (i *Imports) finish(job *importJob, err error) {
	ctx := context.Background()
	if err != nil {
		i.record(ctx, "error", "project.import.failed", map[string]string{"user_id": job.OwnerID, "project_id": job.ProjectID, "job_id": job.JobID, "reason": "import_failed"})
		return
	}
	i.record(ctx, "info", "project.import.completed", map[string]string{"user_id": job.OwnerID, "project_id": job.ProjectID, "job_id": job.JobID, "repository": job.Repository.FullName, "commit": job.Commit})
}

func (i *Imports) record(ctx context.Context, level, name string, fields map[string]string) {
	i.audit.Record(ctx, AuditEvent{Level: level, Name: name, Fields: fields})
}

func (i *Imports) updateJob(ctx context.Context, id, status string, progress int, reason string) error {
	return i.store.UpdateImportJob(ctx, UpdateImportJob{ID: id, Status: status, Progress: progress, Error: reason})
}

func (i *Imports) fail(ctx context.Context, job *importJob, public string, err error) error {
	_ = i.updateJob(ctx, job.JobID, "failed", 100, public)
	_ = i.store.DiscardJobStaging(ctx, job.JobID)
	return fmt.Errorf("%s: %w", public, err)
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

// findStaging returns the Staging Snapshot of an interrupted import when it has
// a manifest and every stored file is listed in that manifest with the same
// hash. Any other Staging Snapshot is corrupt, or was interrupted before its
// scan finished, so it is discarded and nil is returned.
func (i *Imports) findStaging(ctx context.Context, job *importJob) (*stagedImport, error) {
	found, err := i.store.FindStagingSnapshot(ctx, job.JobID)
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
	if err = i.store.DiscardJobStaging(ctx, job.JobID); err != nil {
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
func filesMatchManifest(files []SnapshotFile, manifest []manifestEntry) bool {
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
func manifestOf(files []SnapshotFile) []manifestEntry {
	manifest := make([]manifestEntry, 0, len(files))
	for _, file := range files {
		manifest = append(manifest, manifestEntry{Path: file.Path, Mode: file.Mode, Hash: file.Hash, Size: int64(len(file.Content))})
	}
	return manifest
}

func scanFiles(files []SnapshotFile) scan.Result {
	input := make([]scan.File, len(files))
	for k, file := range files {
		input[k] = scan.File{Path: file.Path, Content: file.Content}
	}
	return scan.Files(input)
}

func (i *Imports) run(parent context.Context, job *importJob) error {
	tracked, release := i.jobs.Track(parent, job.OwnerID)
	defer release()
	ctx, cancel := context.WithTimeout(tracked, importTimeout)
	defer cancel()
	staged, err := i.findStaging(ctx, job)
	if err != nil {
		return i.fail(ctx, job, "snapshot could not be initialized", err)
	}
	if staged != nil {
		err = i.storeStaged(ctx, job, staged)
		if !errors.Is(err, errStagingCorrupt) {
			return err
		}
		if err = i.store.DiscardJobStaging(ctx, job.JobID); err != nil {
			return i.fail(ctx, job, "snapshot could not be initialized", err)
		}
	}
	return i.importFromClone(ctx, job)
}

// storeStaged finishes an interrupted import from its Staging Snapshot. The
// repository is cloned only when the manifest lists files that are not stored.
// It returns errStagingCorrupt when the clone disagrees with the manifest.
func (i *Imports) storeStaged(ctx context.Context, job *importJob, staged *stagedImport) error {
	pending := staged.pending()
	if len(pending) == 0 {
		if err := i.updateJob(ctx, job.JobID, "storing", 95, ""); err != nil {
			return err
		}
		return i.completeImport(ctx, job, staged.snapshotID, staged.manifest)
	}
	tree, err := i.cloneCommit(ctx, job)
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
		return i.fail(ctx, job, "repository scan failed", err)
	}
	if !filesMatchManifest(files, pending) {
		return errStagingCorrupt
	}
	if err = i.updateJob(ctx, job.JobID, "storing", 70, ""); err != nil {
		return err
	}
	if err = i.storeFiles(ctx, job, staged.snapshotID, files); err != nil {
		return err
	}
	return i.completeImport(ctx, job, staged.snapshotID, staged.manifest)
}

// importFromClone imports the selected commit from a fresh clone. It scans the
// repository, starts a Staging Snapshot that keeps the manifest, and stores
// every file.
func (i *Imports) importFromClone(ctx context.Context, job *importJob) error {
	tree, err := i.cloneCommit(ctx, job)
	if err != nil {
		return err
	}
	defer tree.Close()
	if err = i.updateJob(ctx, job.JobID, "scanning", 45, ""); err != nil {
		return err
	}
	files, err := tree.Files(nil)
	if err != nil {
		return i.fail(ctx, job, "repository scan failed", err)
	}
	found := scanFiles(files)
	metadata, err := json.Marshal(snapshotMetadata{Repository: job.Repository, Scan: found})
	if err != nil {
		return i.fail(ctx, job, "repository metadata could not be encoded", err)
	}
	manifest := manifestOf(files)
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return i.fail(ctx, job, "repository manifest could not be encoded", err)
	}
	snapshotID, err := i.beginStaging(ctx, job, found, metadata, manifestJSON)
	if err != nil {
		return i.fail(ctx, job, "snapshot could not be initialized", err)
	}
	if err = i.updateJob(ctx, job.JobID, "storing", 70, ""); err != nil {
		return err
	}
	if err = i.storeFiles(ctx, job, snapshotID, files); err != nil {
		return err
	}
	return i.completeImport(ctx, job, snapshotID, manifest)
}

// cloneCommit fetches the selected commit and returns its tree. The caller must
// call Close on it after it has read the files.
func (i *Imports) cloneCommit(ctx context.Context, job *importJob) (SourceTree, error) {
	if err := i.updateJob(ctx, job.JobID, "cloning", 15, ""); err != nil {
		return nil, err
	}
	tree, err := i.source.Open(ctx, SourceRequest{Repository: job.Repository.FullName, Branch: job.Branch, Commit: job.Commit, Token: job.Token})
	if err != nil {
		var failure *SourceError
		if errors.As(err, &failure) {
			return nil, i.fail(ctx, job, failure.Message, failure.Err)
		}
		return nil, i.fail(ctx, job, "repository clone failed", err)
	}
	return tree, nil
}

// beginStaging starts a Staging Snapshot for the commit and returns its ID.
func (i *Imports) beginStaging(ctx context.Context, job *importJob, found scan.Result, metadata, manifest []byte) (string, error) {
	snapshotID := uuid.NewString()
	err := i.store.BeginSnapshot(ctx, BeginSnapshot{ID: snapshotID, JobID: job.JobID, OwnerID: job.OwnerID, ProjectID: job.ProjectID, RepositoryID: job.Repository.ID, Repository: job.Repository.FullName, Branch: job.Branch, Commit: job.Commit, DefaultBranch: job.Repository.DefaultBranch, Language: dominantLanguage(found.Languages), Runtime: found.Runtime, Stars: job.Repository.Stars, Forks: job.Repository.Forks, MetadataJSON: string(metadata), Manifest: string(manifest)})
	if err != nil {
		return "", err
	}
	return snapshotID, nil
}

// storeFiles saves files into the Staging Snapshot in batches, reporting progress.
func (i *Imports) storeFiles(ctx context.Context, job *importJob, snapshotID string, files []SnapshotFile) error {
	for start := 0; start < len(files); start += 100 {
		end := min(start+100, len(files))
		if err := i.store.AddSnapshotFiles(ctx, snapshotID, files[start:end]); err != nil {
			return i.fail(ctx, job, "snapshot storage failed", err)
		}
		progress := 70 + (25*end)/max(1, len(files))
		_ = i.updateJob(ctx, job.JobID, "storing", progress, "")
	}
	return nil
}

// completeImport makes the Staging Snapshot the active Snapshot of its Project.
// The totals come from the manifest, which describes every file of the commit.
func (i *Imports) completeImport(ctx context.Context, job *importJob, snapshotID string, manifest []manifestEntry) error {
	var total int64
	for _, entry := range manifest {
		total += entry.Size
	}
	err := i.store.CompleteSnapshot(ctx, CompleteSnapshot{SnapshotID: snapshotID, JobID: job.JobID, FileCount: len(manifest), TotalBytes: total})
	if err != nil {
		return i.fail(ctx, job, "snapshot finalization failed", err)
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
