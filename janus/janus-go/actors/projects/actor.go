package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"janus/actors/github"
	"janus/actors/jobrunner"
	actortrace "janus/actors/telemetry"
	"janus/internal/app"
)

// Actor starts and resumes repository imports and owns Snapshot activation and
// Entrypoints. Plain project operations and GitHub access are not here: callers
// use the persistence Store and the github module directly.
type Actor struct {
	store  app.Store
	source app.Source
	audit  app.Audit
	github *github.Service
	runner *jobrunner.Runner
}

// ImportWorker owns the long-running clone, scan, and snapshot persistence
// workflow for one repository import job.
type ImportWorker struct {
	store  app.Store
	source app.Source
	audit  app.Audit
	runner *jobrunner.Runner
}

var _ goakt.Actor = (*ImportWorker)(nil)

func (*ImportWorker) PreStart(*goakt.Context) error { return nil }

func (a *ImportWorker) Receive(rctx *goakt.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *RunImport:
		// An import clones and stores a whole repository, which takes minutes.
		// It runs off the mailbox thread so it never holds one of the dispatcher's
		// few workers; the outcome returns as an importFinished message.
		self := rctx.Self()
		task := func() (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					result = &importFinished{Import: msg, Err: fmt.Errorf("repository import panicked: %v", recovered)}
				}
			}()
			return &importFinished{Import: msg, Err: a.runImport(context.Background(), msg)}, nil
		}
		if err := self.PipeTo(context.WithoutCancel(rctx.Context()), self, task); err != nil {
			a.finish(rctx.Context(), &importFinished{Import: msg, Err: fmt.Errorf("repository import could not start: %w", err)})
			rctx.Shutdown()
		}
	case *importFinished:
		a.finish(rctx.Context(), msg)
		rctx.Shutdown()
	default:
		rctx.Unhandled()
	}
}

// finish records how an import ended and frees its slot. It runs once per import.
func (a *ImportWorker) finish(ctx context.Context, done *importFinished) {
	if a.runner != nil {
		defer a.runner.Finish()
	}
	_, span := actortrace.StartSpan(ctx, "actor.project_import.finish", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	msg := done.Import
	if done.Err != nil {
		span.SetStatus(codes.Error, "repository import failed")
		actortrace.RecordError(span, done.Err)
		a.record(ctx, "error", "project.import.failed", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": msg.JobID, "reason": "import_failed"})
		return
	}
	a.record(ctx, "info", "project.import.completed", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": msg.JobID, "repository": msg.Repository.FullName, "commit": msg.Commit})
}

func (*ImportWorker) PostStop(*goakt.Context) error { return nil }

var _ goakt.Actor = (*Actor)(nil)

// New creates a project actor backed by store, reading GitHub through gh and
// repositories through source. Project audit events go to audit.
func New(store app.Store, gh *github.Service, source app.Source, audit app.Audit) *Actor {
	return &Actor{store: store, github: gh, source: source, audit: audit, runner: jobrunner.New(2)}
}
func (a *Actor) PreStart(*goakt.Context) error { return nil }

func (a *Actor) Receive(rctx *goakt.ReceiveContext) {
	ctx, span := actortrace.StartSpan(rctx.Context(), "actor.projects.receive", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	span.SetAttributes(attribute.String("actor.message.type", fmt.Sprintf("%T", rctx.Message())))
	switch msg := rctx.Message().(type) {
	case *RequestImport:
		span.SetAttributes(
			attribute.String("project.id", msg.ProjectID),
			attribute.String("project.owner_id", msg.OwnerID),
			attribute.String("project.import.repository", msg.Repository),
			attribute.String("project.import.branch", msg.Branch),
			attribute.String("project.import.commit", msg.Commit),
		)
		jobID, err := a.startImport(rctx, msg)
		actortrace.RecordError(span, err)
		if err == nil {
			span.SetAttributes(attribute.String("project.import.job_id", jobID), attribute.String("project.import.repository", msg.Repository), attribute.String("project.import.commit", msg.Commit))
			a.record(ctx, "info", "project.import.queued", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": jobID, "repository": msg.Repository})
		}
		rctx.Response(&ImportRequested{JobID: jobID, Err: err})
	case *Drain:
		a.runner.StopAccepting()
		rctx.Response(&Drained{Pending: a.runner.Pending()})
	case *SetEntryPoint:
		span.SetAttributes(
			attribute.String("project.id", msg.ProjectID),
			attribute.String("project.snapshot.id", msg.SnapshotID),
		)
		err := a.setEntryPoint(ctx, msg)
		actortrace.RecordError(span, err)
		rctx.Response(&EntryPointSet{Err: err})
	case *ResumeImports:
		count, err := a.resumeImports(ctx, rctx.ActorSystem())
		rctx.Response(&ImportsResumed{Count: count, Err: err})
	case *RetryImport:
		jobID, err := a.retryImport(rctx, msg)
		rctx.Response(&ImportRequested{JobID: jobID, Err: err})
	case *DeleteAccount:
		result, err := a.deleteAccount(ctx, msg)
		if err != nil {
			result.Err = err
		}
		rctx.Response(&result)
	default:
		rctx.Unhandled()
	}
}

// setEntryPoint records the Entrypoint the Account confirmed for the project's
// latest Snapshot, when it is one of the candidates the Scan found.
func (a *Actor) setEntryPoint(ctx context.Context, msg *SetEntryPoint) error {
	project, found, err := a.store.GetProject(ctx, msg.ProjectID, msg.OwnerID)
	if err != nil {
		return err
	}
	switch {
	case !found:
		return errors.New("project not found")
	case project.SnapshotID != msg.SnapshotID:
		return errors.New("entrypoint selection must target the latest snapshot")
	}
	if err = validateEntrypoint(project.MetadataJSON, msg.EntryPoint); err != nil {
		return err
	}
	return a.store.SetProjectEntryPoint(ctx, app.SetProjectEntryPoint{OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, SnapshotID: msg.SnapshotID, EntryPoint: msg.EntryPoint})
}

func (a *Actor) updateJob(ctx context.Context, id, status string, progress int, reason string) error {
	return a.store.UpdateImportJob(ctx, app.UpdateImportJob{ID: id, Status: status, Progress: progress, Error: reason})
}

func (a *Actor) startImport(rctx *goakt.ReceiveContext, msg *RequestImport) (string, error) {
	if !a.runner.Accepting() {
		return "", errors.New("project imports are shutting down")
	}
	if msg.OwnerID == "" || msg.ProjectID == "" || msg.Repository == "" || msg.Branch == "" || len(msg.Commit) != 40 {
		return "", errors.New("project, repository, branch and full commit SHA are required")
	}
	_, found, err := a.store.GetProject(rctx.Context(), msg.ProjectID, msg.OwnerID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("project not found")
	}
	token, connected, err := a.github.Token(rctx.Context(), msg.OwnerID)
	if err != nil {
		return "", err
	}
	if !connected {
		return "", errors.New("connect GitHub before importing a repository")
	}
	repository, err := a.github.API.Repository(rctx.Context(), token, msg.Repository)
	if err != nil {
		return "", err
	}
	if repository.FullName != msg.Repository {
		return "", errors.New("GitHub returned a different repository")
	}
	branches, err := a.github.API.Branches(rctx.Context(), token, msg.Repository)
	if err != nil {
		return "", err
	}
	branchFound := false
	for _, branch := range branches {
		if branch.Name == msg.Branch {
			branchFound = true
			break
		}
	}
	if !branchFound {
		return "", errors.New("selected branch does not belong to this repository")
	}
	commits, err := a.github.API.Commits(rctx.Context(), token, msg.Repository, msg.Branch)
	if err != nil {
		return "", err
	}
	commitFound := false
	for _, commit := range commits {
		if strings.EqualFold(commit.SHA, msg.Commit) {
			commitFound = true
			break
		}
	}
	if !commitFound {
		return "", errors.New("selected commit is not among the branch's 50 most recent commits")
	}
	jobID := uuid.NewString()
	if err = a.store.CreateImportJob(rctx.Context(), app.CreateImportJob{ID: jobID, OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, Repository: repository.FullName, Branch: msg.Branch, Commit: msg.Commit}); err != nil {
		return "", err
	}
	worker := &ImportWorker{store: a.store, source: a.source, audit: a.audit, runner: a.runner}
	run := &RunImport{JobID: jobID, OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, Repository: repository, Branch: msg.Branch, Commit: msg.Commit, Token: token}
	if err = a.runner.Start(rctx.Context(), rctx.ActorSystem(), importWorkerName(jobID), worker, run); err != nil {
		if errors.Is(err, jobrunner.ErrBusy) {
			_ = a.updateJob(rctx.Context(), jobID, "failed", 100, "too many imports are already running")
			return "", errors.New("two repository imports are already running; retry when one finishes")
		}
		_ = a.updateJob(rctx.Context(), jobID, "failed", 100, "import worker could not start")
		return "", err
	}
	return jobID, nil
}

type snapshotMetadata struct {
	Repository github.Repository `json:"repository"`
	Scan       ScanResult        `json:"scan"`
}

func (a *ImportWorker) updateJob(ctx context.Context, id, status string, progress int, reason string) error {
	return a.store.UpdateImportJob(ctx, app.UpdateImportJob{ID: id, Status: status, Progress: progress, Error: reason})
}
func (a *ImportWorker) fail(ctx context.Context, msg *RunImport, public string, err error) error {
	_ = a.updateJob(ctx, msg.JobID, "failed", 100, public)
	_ = a.store.DiscardJobStaging(ctx, msg.JobID)
	return fmt.Errorf("%s: %w", public, err)
}

func (a *ImportWorker) record(ctx context.Context, level, name string, fields map[string]string) {
	a.audit.Record(ctx, app.AuditEvent{Level: level, Name: name, Fields: fields})
}

func (a *Actor) record(ctx context.Context, level, name string, fields map[string]string) {
	a.audit.Record(ctx, app.AuditEvent{Level: level, Name: name, Fields: fields})
}

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
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (*Actor) PostStop(*goakt.Context) error { return nil }
