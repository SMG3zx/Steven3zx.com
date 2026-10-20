package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"janus/internal/domain/artifact"
	"janus/internal/domain/templates"
)

const (
	buildPageSize       = 200
	maxConcurrentBuilds = 2
	buildTimeout        = 10 * time.Minute
)

// NewBuild asks for a WASM Build of a Project's Snapshot.
type NewBuild struct {
	OwnerID, ProjectID, Target string
	// SnapshotID selects the Snapshot to build. When empty, the Project's active
	// Snapshot is built.
	SnapshotID string
}

// Builds compiles imported source to WASM Artifacts. Each Build runs on a
// goroutine the use case owns, at most two at once, and is recorded in the Store
// as it moves through its states.
type Builds struct {
	store   Store
	builder Builder
	audit   Audit
	jobs    *JobRunner
}

// NewBuilds returns a Builds that reads source and records Builds in store and
// compiles with builder.
func NewBuilds(store Store, builder Builder, audit Audit) *Builds {
	return &Builds{store: store, builder: builder, audit: audit, jobs: NewJobRunner(maxConcurrentBuilds)}
}

// buildJob is one Build to run.
type buildJob struct {
	OwnerID, ProjectID, SnapshotID string
	JobID, Target, Language        string
	// status is the status the job is in, as the use case last moved it. It is
	// the From of the next transition, so the Store detects a concurrent change.
	status string
}

// Request queues a Build and starts it. It returns the Build's ID, and a plain
// error whose text is meant for the Account when the Build cannot start.
func (b *Builds) Request(ctx context.Context, request NewBuild) (string, error) {
	if !b.jobs.Accepting() {
		return "", errors.New("WASM builds are shutting down")
	}
	if b.builder == nil {
		return "", errors.New("WASM build runner is not configured")
	}
	if !templates.KnownTarget(request.Target) {
		return "", errors.New("unsupported WASM target")
	}
	if request.OwnerID == "" || request.ProjectID == "" {
		return "", errors.New("project is required")
	}
	project, found, err := b.store.GetProject(ctx, request.ProjectID, request.OwnerID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("project not found")
	}
	snapshotID, source, err := b.buildSource(ctx, request, project)
	if err != nil {
		return "", err
	}
	template, ok := templates.Buildable(source)
	if !ok {
		return "", unbuildableLanguageError(source)
	}
	if err = template.CheckTarget(request.Target); err != nil {
		return "", err
	}
	jobID := uuid.NewString()
	if err = b.store.CreateWASMBuild(ctx, CreateWASMBuild{ID: jobID, OwnerID: request.OwnerID, ProjectID: request.ProjectID, SnapshotID: snapshotID, Target: request.Target}); err != nil {
		return "", err
	}
	job := &buildJob{OwnerID: request.OwnerID, ProjectID: request.ProjectID, SnapshotID: snapshotID, JobID: jobID, Target: request.Target, Language: strings.ToLower(template.Language), status: "queued"}
	if err = b.jobs.Start(b.runner(job), b.crashed(job)); err != nil {
		if errors.Is(err, ErrBusy) {
			_ = b.update(ctx, job, "failed", 100, "two WASM builds are already running")
			return "", errors.New("two WASM builds are already running; retry when one finishes")
		}
		_ = b.update(ctx, job, "failed", 100, "WASM build worker could not start")
		return "", err
	}
	b.record(ctx, "info", "project.wasm.build.queued", map[string]string{"user_id": request.OwnerID, "project_id": request.ProjectID, "job_id": jobID, "target": request.Target})
	return jobID, nil
}

// Find returns one of an Account's Builds.
func (b *Builds) Find(ctx context.Context, owner, jobID string) (WASMBuild, bool, error) {
	return b.store.FindWASMBuild(ctx, jobID, owner)
}

// Artifact returns the Artifact of one of an Account's completed Builds.
func (b *Builds) Artifact(ctx context.Context, owner, jobID string) (WASMBuildArtifact, error) {
	return b.store.FindWASMBuildArtifact(ctx, jobID, owner)
}

// Retry starts a new Build of a failed Build's Snapshot.
func (b *Builds) Retry(ctx context.Context, owner, jobID string) (string, error) {
	build, found, err := b.store.FindWASMBuild(ctx, jobID, owner)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("WASM build not found")
	}
	if build.Status != "failed" {
		return "", errors.New("only a failed WASM build can be retried")
	}
	return b.Request(ctx, NewBuild{OwnerID: owner, ProjectID: build.ProjectID, Target: build.Target, SnapshotID: build.SnapshotID})
}

// Resume starts every Build the previous shutdown interrupted, and returns how
// many there were. A Build whose Snapshot is no longer buildable fails instead
// of starting. It runs once at startup.
func (b *Builds) Resume(ctx context.Context) (int, error) {
	builds, err := b.store.ListResumableBuilds(ctx)
	if err != nil {
		return 0, err
	}
	for _, build := range builds {
		if _, ok := templates.Buildable(build.Language); !ok {
			_ = b.update(ctx, &buildJob{JobID: build.JobID, status: "queued"}, "failed", 100, unbuildableLanguageError(build.Language).Error())
			continue
		}
		job := &buildJob{OwnerID: build.OwnerID, ProjectID: build.ProjectID, SnapshotID: build.SnapshotID, JobID: build.JobID, Target: build.Target, Language: build.Language, status: "queued"}
		b.jobs.Queue(b.runner(job), b.crashed(job))
	}
	return len(builds), nil
}

// CancelOwner stops the running Builds of an Account, and returns how many. The
// Account's queued Builds are cancelled in the Store by the caller.
func (b *Builds) CancelOwner(owner string) int { return b.jobs.CancelOwner(owner) }

// Drain makes Request refuse new Builds and waits for the running ones, or
// until ctx ends.
func (b *Builds) Drain(ctx context.Context) error { return b.jobs.Drain(ctx) }

// Wait waits for the running and queued Builds, or until ctx ends.
func (b *Builds) Wait(ctx context.Context) error { return b.jobs.Wait(ctx) }

func unbuildableLanguageError(language string) error {
	if language == "" {
		return errors.New("no buildable language was detected in this snapshot")
	}
	return fmt.Errorf("WASM builds are not available for %s projects yet", language)
}

// buildSource returns the Snapshot a Build compiles and its detected language.
// The Project's active Snapshot is used unless the request names one.
func (b *Builds) buildSource(ctx context.Context, request NewBuild, project Project) (string, string, error) {
	if request.SnapshotID == "" {
		if project.SnapshotID == "" {
			return "", "", errors.New("import a repository before building it")
		}
		return project.SnapshotID, project.Language, nil
	}
	snapshots, err := b.store.ListSnapshots(ctx, request.OwnerID, request.ProjectID)
	if err != nil {
		return "", "", err
	}
	for _, snapshot := range snapshots {
		if snapshot.ID == request.SnapshotID {
			return snapshot.ID, snapshot.Language, nil
		}
	}
	return "", "", errors.New("snapshot not found")
}

// runner returns the function that runs job, records how it ended and audits it.
func (b *Builds) runner(job *buildJob) func() {
	return func() { b.finish(job, b.run(context.Background(), job)) }
}

// crashed returns the function that records a panic in job as a failed Build.
func (b *Builds) crashed(job *buildJob) func(error) {
	return func(err error) {
		err = fmt.Errorf("WASM build panicked: %v", err)
		_ = b.update(context.Background(), job, "failed", 100, err.Error())
		b.finish(job, err)
	}
}

// finish audits how a Build ended.
func (b *Builds) finish(job *buildJob, err error) {
	ctx := context.Background()
	if err != nil {
		b.record(ctx, "error", "project.wasm.build.failed", map[string]string{"user_id": job.OwnerID, "project_id": job.ProjectID, "job_id": job.JobID, "reason": "build_failed"})
		return
	}
	b.record(ctx, "info", "project.wasm.build.completed", map[string]string{"user_id": job.OwnerID, "project_id": job.ProjectID, "job_id": job.JobID})
}

func (b *Builds) run(parent context.Context, job *buildJob) error {
	tracked, release := b.jobs.Track(parent, job.OwnerID)
	defer release()
	ctx, cancel := context.WithTimeout(tracked, buildTimeout)
	defer cancel()
	if err := b.update(ctx, job, "building", 5, ""); err != nil {
		return err
	}
	workspace, err := os.MkdirTemp("", "janus-wasm-build-")
	if err != nil {
		return b.fail(ctx, job, "could not allocate build workspace", err)
	}
	defer os.RemoveAll(workspace)
	for offset := 0; ; offset += buildPageSize {
		files, listErr := b.store.ListProjectFiles(ctx, ListProjectFiles{OwnerID: job.OwnerID, ProjectID: job.ProjectID, SnapshotID: job.SnapshotID, Offset: offset, Limit: buildPageSize})
		if listErr != nil {
			return b.fail(ctx, job, "could not read the imported source snapshot", listErr)
		}
		for _, file := range files {
			if err = writeSourceFile(workspace, file); err != nil {
				return b.fail(ctx, job, "imported source contains an unsafe path", err)
			}
		}
		if len(files) < buildPageSize {
			break
		}
		if err = b.update(ctx, job, "building", min(30, 5+offset/buildPageSize), ""); err != nil {
			return err
		}
	}
	request := BuildRequest{Workspace: workspace, Target: job.Target}
	if strings.EqualFold(job.Language, "JavaScript/TypeScript") {
		request.Entrypoint = b.snapshotEntrypoint(ctx, job)
	}
	built, err := b.builder.Build(ctx, job.Language, request)
	name, data, log := built.Name, built.Artifact, built.Log
	if err != nil {
		var manifest *ManifestError
		if errors.As(err, &manifest) {
			return b.fail(ctx, job, manifest.Message, manifest.Err)
		}
		label := languageLabel(job.Language)
		_ = b.updateWithLog(ctx, job, "failed", 100, label+" WASM build failed: "+err.Error(), log)
		return fmt.Errorf("%s WASM build failed: %w", label, err)
	}
	if len(data) == 0 || len(data) > artifact.MaxBytes {
		return b.failWithLog(ctx, job, "WASM artifact is empty or exceeds the 128 MiB limit", log, errors.New("artifact size limit exceeded"))
	}
	err = b.store.CompleteWASMBuild(ctx, CompleteWASMBuild{ID: job.JobID, ArtifactName: name, Artifact: data, Log: log})
	if err != nil {
		return b.failWithLog(ctx, job, "WASM artifact could not be stored", log, err)
	}
	return nil
}

// snapshotEntrypoint returns the Entrypoint the Account confirmed for the
// Build's Snapshot, or "" when there is none or it cannot be read.
func (b *Builds) snapshotEntrypoint(ctx context.Context, job *buildJob) string {
	snapshots, err := b.store.ListSnapshots(ctx, job.OwnerID, job.ProjectID)
	if err != nil {
		return ""
	}
	for _, snapshot := range snapshots {
		if snapshot.ID == job.SnapshotID {
			return snapshot.EntryPoint
		}
	}
	return ""
}

func writeSourceFile(root string, file SnapshotFile) error {
	name := file.Path
	if !artifact.SafePath(name) {
		return errors.New("invalid repository path")
	}
	destination := filepath.Join(root, filepath.FromSlash(path.Clean(name)))
	if !filepath.IsLocal(mustRel(root, destination)) {
		return errors.New("repository path escapes build workspace")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	return os.WriteFile(destination, file.Content, 0600)
}

func mustRel(root, destination string) string {
	rel, err := filepath.Rel(root, destination)
	if err != nil {
		return ".."
	}
	return rel
}

func (b *Builds) update(ctx context.Context, job *buildJob, status string, progress int, reason string) error {
	return b.updateWithLog(ctx, job, status, progress, reason, nil)
}

// updateWithLog moves job to status, from the status it is in.
func (b *Builds) updateWithLog(ctx context.Context, job *buildJob, status string, progress int, reason string, log []byte) error {
	err := b.store.TransitionWASMBuild(ctx, JobTransition{ID: job.JobID, From: job.status, To: status, Progress: progress, Error: reason, Log: string(log)})
	if err == nil {
		job.status = status
	}
	return err
}

func (b *Builds) fail(ctx context.Context, job *buildJob, reason string, cause error) error {
	if cause != nil {
		reason += ": " + cause.Error()
	}
	_ = b.update(ctx, job, "failed", 100, reason)
	return fmt.Errorf("%s: %w", reason, cause)
}

func (b *Builds) failWithLog(ctx context.Context, job *buildJob, reason string, log []byte, cause error) error {
	if cause != nil {
		reason += ": " + cause.Error()
	}
	_ = b.updateWithLog(ctx, job, "failed", 100, reason, log)
	return fmt.Errorf("%s: %w", reason, cause)
}

func (b *Builds) record(ctx context.Context, level, name string, fields map[string]string) {
	b.audit.Record(ctx, AuditEvent{Level: level, Name: name, Fields: fields})
}

func languageLabel(language string) string {
	switch {
	case strings.EqualFold(language, "Go"):
		return "Go"
	case strings.EqualFold(language, "JavaScript/TypeScript"):
		return "JavaScript/TypeScript"
	}
	return "Rust"
}
