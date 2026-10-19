package wasm

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
	"github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"janus/actors/accounting"
	"janus/actors/artifact"
	"janus/actors/builder"
	"janus/actors/jobrunner"
	"janus/actors/persistence"
	actortrace "janus/actors/telemetry"
	"janus/actors/templates"
)

const (
	pageSize            = 200
	maxConcurrentBuilds = 2
	buildTimeout        = 10 * time.Minute
)

// Actor coordinates build jobs, delegates compilation to a builder, and stores
// artifacts.
type Actor struct {
	persistence *actor.PID
	accounting  *actor.PID
	builder     builder.Builder
	jobs        *jobrunner.Runner
}

var _ actor.Actor = (*Actor)(nil)

// New creates a build actor that compiles with b. Each build runs on a worker
// actor the job runner starts, so this actor stays responsive.
func New(persistencePID, accountingPID *actor.PID, b builder.Builder) *Actor {
	return &Actor{persistence: persistencePID, accounting: accountingPID, builder: b, jobs: jobrunner.New(maxConcurrentBuilds)}
}

func (a *Actor) PreStart(*actor.Context) error { return nil }

func (a *Actor) Receive(rctx *actor.ReceiveContext) {
	ctx, span := actortrace.StartSpan(rctx.Context(), "actor.wasm.receive", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	switch msg := rctx.Message().(type) {
	case *RequestBuild:
		jobID, err := a.start(rctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		} else {
			span.SetAttributes(attribute.String("wasm.build.job_id", jobID), attribute.String("wasm.target", msg.Target))
			a.audit(ctx, "info", "project.wasm.build.queued", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": jobID, "target": msg.Target})
		}
		rctx.Response(&BuildRequested{JobID: jobID, Err: err})
	case *Build:
		response, err := a.ask(ctx, &persistence.FindWASMBuild{ID: msg.JobID, OwnerID: msg.OwnerID})
		if err != nil {
			rctx.Response(&BuildFound{Err: err})
			return
		}
		result, ok := response.(*persistence.WASMBuildFound)
		if !ok {
			rctx.Response(&BuildFound{Err: fmt.Errorf("unexpected persistence response %T", response)})
			return
		}
		rctx.Response(&BuildFound{Build: result.Build, Found: result.Found, Err: result.Err})
	case *ResumeBuilds:
		count, err := a.resumeBuilds(ctx, rctx.ActorSystem())
		rctx.Response(&BuildsResumed{Count: count, Err: err})
	case *RetryBuild:
		jobID, err := a.retryBuild(rctx, msg)
		rctx.Response(&BuildRequested{JobID: jobID, Err: err})
	case *Drain:
		a.jobs.StopAccepting()
		rctx.Response(&Drained{Pending: a.jobs.Pending()})
	case *CancelBuilds:
		rctx.Response(&BuildsCancelled{Count: a.jobs.CancelOwner(msg.OwnerID)})
	case *RunBuild:
		span.SetAttributes(
			attribute.String("project.id", msg.ProjectID),
			attribute.String("project.snapshot.id", msg.SnapshotID),
			attribute.String("wasm.target", msg.Target),
		)
		// The build runs for minutes, so it runs off the mailbox thread: a handler
		// that blocked here would hold one of the dispatcher's few workers for the
		// whole build, and every actor shares them. The outcome comes back as a
		// buildFinished message. The task reads no actor state.
		self := rctx.Self()
		task := func() (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					result = &buildFinished{Build: msg, Err: fmt.Errorf("WASM build panicked: %v", recovered)}
				}
			}()
			return &buildFinished{Build: msg, Err: a.run(context.Background(), msg)}, nil
		}
		if err := self.PipeTo(context.WithoutCancel(ctx), self, task); err != nil {
			a.finish(ctx, span, &buildFinished{Build: msg, Err: fmt.Errorf("WASM build could not start: %w", err)})
			rctx.Shutdown()
		}
	case *buildFinished:
		a.finish(ctx, span, msg)
		rctx.Shutdown()
	default:
		rctx.Unhandled()
	}
}

// finish records how a build ended and frees its slot. It runs once per build.
func (a *Actor) finish(ctx context.Context, span trace.Span, done *buildFinished) {
	defer a.jobs.Finish()
	msg := done.Build
	if done.Err != nil {
		actortrace.RecordError(span, done.Err)
		span.SetStatus(codes.Error, "WASM build failed")
		a.audit(ctx, "error", "project.wasm.build.failed", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": msg.JobID, "reason": "build_failed"})
		return
	}
	a.audit(ctx, "info", "project.wasm.build.completed", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": msg.JobID})
}

func (a *Actor) start(rctx *actor.ReceiveContext, msg *RequestBuild) (string, error) {
	if !a.jobs.Accepting() {
		return "", errors.New("WASM builds are shutting down")
	}
	if a.builder == nil {
		return "", errors.New("WASM build runner is not configured")
	}
	if !templates.KnownTarget(msg.Target) {
		return "", errors.New("unsupported WASM target")
	}
	if msg.OwnerID == "" || msg.ProjectID == "" {
		return "", errors.New("project is required")
	}
	projectResponse, err := a.ask(rctx.Context(), &persistence.GetProject{ID: msg.ProjectID, OwnerID: msg.OwnerID})
	if err != nil {
		return "", err
	}
	project, ok := projectResponse.(*persistence.ProjectFound)
	if !ok {
		return "", fmt.Errorf("unexpected persistence response %T", projectResponse)
	}
	if project.Err != nil {
		return "", project.Err
	}
	if !project.Found {
		return "", errors.New("project not found")
	}
	snapshotID, source, err := a.buildSource(rctx.Context(), msg, project.Project)
	if err != nil {
		return "", err
	}
	template, ok := templates.Buildable(source)
	if !ok {
		return "", unbuildableLanguageError(source)
	}
	if err = template.CheckTarget(msg.Target); err != nil {
		return "", err
	}
	language := strings.ToLower(template.Language)
	jobID := uuid.NewString()
	response, err := a.ask(rctx.Context(), &persistence.CreateWASMBuild{ID: jobID, OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, SnapshotID: snapshotID, Target: msg.Target})
	if err != nil {
		return "", err
	}
	created, ok := response.(*persistence.WASMBuildCreated)
	if !ok {
		return "", fmt.Errorf("unexpected persistence response %T", response)
	}
	if created.Err != nil {
		return "", created.Err
	}
	run := &RunBuild{OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, SnapshotID: snapshotID, JobID: jobID, Target: msg.Target, Language: language}
	if err = a.jobs.Start(rctx.Context(), rctx.ActorSystem(), buildWorkerName(jobID), a.worker(), run); err != nil {
		if errors.Is(err, jobrunner.ErrBusy) {
			_ = a.update(rctx.Context(), jobID, "failed", 100, "two WASM builds are already running")
			return "", errors.New("two WASM builds are already running; retry when one finishes")
		}
		_ = a.update(rctx.Context(), jobID, "failed", 100, "WASM build worker could not start")
		return "", err
	}
	return jobID, nil
}

func (a *Actor) run(parent context.Context, msg *RunBuild) error {
	tracked, release := a.jobs.Track(parent, msg.OwnerID)
	defer release()
	ctx, cancel := context.WithTimeout(tracked, buildTimeout)
	defer cancel()
	if err := a.update(ctx, msg.JobID, "building", 5, ""); err != nil {
		return err
	}
	workspace, err := os.MkdirTemp("", "janus-wasm-build-")
	if err != nil {
		return a.fail(ctx, msg.JobID, "could not allocate build workspace", err)
	}
	defer os.RemoveAll(workspace)
	for offset := 0; ; offset += pageSize {
		response, askErr := a.ask(ctx, &persistence.ListProjectFiles{OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, SnapshotID: msg.SnapshotID, Offset: offset, Limit: pageSize})
		if askErr != nil {
			return a.fail(ctx, msg.JobID, "could not read the imported source snapshot", askErr)
		}
		page, ok := response.(*persistence.ProjectFilesListed)
		if !ok {
			return a.fail(ctx, msg.JobID, "could not read the imported source snapshot", fmt.Errorf("unexpected persistence response %T", response))
		}
		if page.Err != nil {
			return a.fail(ctx, msg.JobID, "could not read the imported source snapshot", page.Err)
		}
		for _, file := range page.Files {
			if err = writeSourceFile(workspace, file); err != nil {
				return a.fail(ctx, msg.JobID, "imported source contains an unsafe path", err)
			}
		}
		if len(page.Files) < pageSize {
			break
		}
		if err = a.update(ctx, msg.JobID, "building", min(30, 5+offset/pageSize), ""); err != nil {
			return err
		}
	}
	request := builder.Request{Workspace: workspace, Target: msg.Target}
	if strings.EqualFold(msg.Language, "JavaScript/TypeScript") {
		request.Entrypoint = a.snapshotEntrypoint(ctx, msg)
	}
	built, err := a.builder.Build(ctx, msg.Language, request)
	name, data, log := built.Name, built.Artifact, built.Log
	if err != nil {
		var manifest *builder.ManifestError
		if errors.As(err, &manifest) {
			return a.fail(ctx, msg.JobID, manifest.Message, manifest.Err)
		}
		label := languageLabel(msg.Language)
		_ = a.updateWithLog(ctx, msg.JobID, "failed", 100, label+" WASM build failed: "+err.Error(), log)
		return fmt.Errorf("%s WASM build failed: %w", label, err)
	}
	if len(data) == 0 || len(data) > artifact.MaxBytes {
		return a.failWithLog(ctx, msg.JobID, "WASM artifact is empty or exceeds the 128 MiB limit", log, errors.New("artifact size limit exceeded"))
	}
	response, err := a.ask(ctx, &persistence.CompleteWASMBuild{ID: msg.JobID, ArtifactName: name, Artifact: data, Log: log})
	if err == nil {
		if result, ok := response.(*persistence.WASMBuildCreated); ok {
			err = result.Err
		} else {
			err = fmt.Errorf("unexpected persistence response %T", response)
		}
	}
	if err != nil {
		return a.failWithLog(ctx, msg.JobID, "WASM artifact could not be stored", log, err)
	}
	return nil
}

func writeSourceFile(root string, file persistence.SnapshotFile) error {
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

func (a *Actor) update(ctx context.Context, id, status string, progress int, reason string) error {
	return a.updateWithLog(ctx, id, status, progress, reason, nil)
}

func (a *Actor) updateWithLog(ctx context.Context, id, status string, progress int, reason string, log []byte) error {
	_, err := a.ask(ctx, &persistence.UpdateWASMBuild{ID: id, Status: status, Progress: progress, Error: reason, Log: string(log)})
	return err
}

func (a *Actor) fail(ctx context.Context, id, reason string, cause error) error {
	if cause != nil {
		reason += ": " + cause.Error()
	}
	_ = a.update(ctx, id, "failed", 100, reason)
	return fmt.Errorf("%s: %w", reason, cause)
}

func (a *Actor) failWithLog(ctx context.Context, id, reason string, log []byte, cause error) error {
	if cause != nil {
		reason += ": " + cause.Error()
	}
	_ = a.updateWithLog(ctx, id, "failed", 100, reason, log)
	return fmt.Errorf("%s: %w", reason, cause)
}

func (a *Actor) ask(ctx context.Context, message any) (any, error) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return actor.Ask(c, a.persistence, message, 30*time.Second)
}

func (a *Actor) audit(ctx context.Context, level, name string, fields map[string]string) {
	if a.accounting != nil {
		_ = actor.Tell(ctx, a.accounting, &accounting.Event{Level: level, Name: name, Fields: fields})
	}
}

func (*Actor) PostStop(*actor.Context) error { return nil }

func languageLabel(language string) string {
	switch {
	case strings.EqualFold(language, "Go"):
		return "Go"
	case strings.EqualFold(language, "JavaScript/TypeScript"):
		return "JavaScript/TypeScript"
	}
	return "Rust"
}
