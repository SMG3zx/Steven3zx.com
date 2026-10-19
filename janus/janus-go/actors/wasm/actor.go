package wasm

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"janus/actors/accounting"
	"janus/actors/compressor"
	dockeractor "janus/actors/docker"
	"janus/actors/jobrunner"
	"janus/actors/persistence"
	actortrace "janus/actors/telemetry"
	"janus/actors/wasmbuilder"
)

const (
	pageSize         = 200
	maxBuildLogBytes = 64 << 10
	maxArtifactBytes = 128 << 20
	buildTimeout     = 10 * time.Minute
)

var validDockerImage = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)

var validTarget = map[string]bool{
	"wasm32-unknown-unknown": true,
	"wasm32-wasip1":          true,
}

// Runner is the narrow seam used by the actor to execute a build in a sandbox.
// Implementations return a ZIP containing one or more .wasm outputs.
type Runner interface {
	Build(context.Context, string, string) (string, []byte, []byte, error)
}

// Actor coordinates build jobs, delegates compilation, and stores artifacts.
type Actor struct {
	persistence *actor.PID
	accounting  *actor.PID
	runner      Runner
	rustBuilder *actor.PID
	goBuilder   *actor.PID
	docker      *actor.PID
	compressor  *actor.PID
	jobs        *jobrunner.Runner
}

var _ actor.Actor = (*Actor)(nil)

// New creates a build actor that uses image through the configured Docker
// controller runner.
func New(persistencePID, accountingPID *actor.PID, image string) *Actor {
	return NewWithRunner(persistencePID, accountingPID, dockerRunner{image: strings.TrimSpace(image)})
}

// NewWithRunner creates a build actor using runner, which is useful for custom
// builder integrations and tests.
func NewWithRunner(persistencePID, accountingPID *actor.PID, runner Runner) *Actor {
	return &Actor{persistence: persistencePID, accounting: accountingPID, runner: runner, jobs: jobrunner.New(2)}
}

// NewWithBuilders creates a build actor that delegates to the Rust, Go, and
// compressor actors.
func NewWithBuilders(persistencePID, accountingPID, rustBuilderPID, goBuilderPID, compressorPID *actor.PID) *Actor {
	return &Actor{persistence: persistencePID, accounting: accountingPID, rustBuilder: rustBuilderPID, goBuilder: goBuilderPID, compressor: compressorPID, jobs: jobrunner.New(2)}
}

// NewWithDocker creates a build actor with an explicit Docker controller PID.
func NewWithDocker(persistencePID, accountingPID, dockerPID *actor.PID, image string) *Actor {
	a := New(persistencePID, accountingPID, image)
	a.docker = dockerPID
	a.runner = dockerRunner{image: strings.TrimSpace(image), docker: dockerPID}
	return a
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
		rctx.Response(&Drained{Err: a.jobs.Drain(ctx)})
	case *RunBuild:
		span.SetAttributes(
			attribute.String("project.id", msg.ProjectID),
			attribute.String("project.snapshot.id", msg.SnapshotID),
			attribute.String("wasm.target", msg.Target),
		)
		defer a.jobs.Finish()
		if err := a.run(ctx, msg); err != nil {
			actortrace.RecordError(span, err)
			span.SetStatus(codes.Error, "WASM build failed")
			a.audit(ctx, "error", "project.wasm.build.failed", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": msg.JobID, "reason": "build_failed"})
		} else {
			a.audit(ctx, "info", "project.wasm.build.completed", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": msg.JobID})
		}
		rctx.Shutdown()
	default:
		rctx.Unhandled()
	}
}

func (a *Actor) start(rctx *actor.ReceiveContext, msg *RequestBuild) (string, error) {
	if !a.jobs.Accepting() {
		return "", errors.New("WASM builds are shutting down")
	}
	if a.runner == nil && a.rustBuilder == nil && a.goBuilder == nil {
		return "", errors.New("WASM build runner is not configured")
	}
	if !validTarget[msg.Target] && msg.Target != "wasip1-wasm" {
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
	language := strings.ToLower(source)
	if !buildableLanguages[language] {
		return "", unbuildableLanguageError(source)
	}
	if language == "rust" && msg.Target == "wasip1-wasm" {
		return "", errors.New("unsupported Rust WASM target")
	}
	if language == "go" && msg.Target != "wasip1-wasm" {
		return "", errors.New("Go WASM builds require target wasip1-wasm")
	}
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
	ctx, cancel := context.WithTimeout(parent, buildTimeout)
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
	var name string
	var artifact, log []byte
	if strings.EqualFold(msg.Language, "Go") {
		if _, err = os.Stat(filepath.Join(workspace, "go.mod")); err != nil {
			return a.fail(ctx, msg.JobID, "Go WASM builds require a go.mod at the repository root", err)
		}
		if a.goBuilder == nil {
			return a.fail(ctx, msg.JobID, "Go WASM builder is not configured", errors.New("builder unavailable"))
		}
		response, askErr := actor.Ask(ctx, a.goBuilder, &wasmbuilder.Build{Workspace: workspace, Target: msg.Target}, buildTimeout)
		if askErr != nil {
			err = askErr
		} else if result, ok := response.(*wasmbuilder.Built); !ok {
			err = fmt.Errorf("unexpected Go builder response %T", response)
		} else {
			name, artifact, log, err = result.Name, result.Artifact, result.Log, result.Err
		}
	} else if a.rustBuilder != nil {
		if _, err = os.Stat(filepath.Join(workspace, "Cargo.toml")); err != nil {
			return a.fail(ctx, msg.JobID, "Rust WASM builds require a Cargo.toml at the repository root", err)
		}
		response, askErr := actor.Ask(ctx, a.rustBuilder, &wasmbuilder.Build{Workspace: workspace, Target: msg.Target}, buildTimeout)
		if askErr != nil {
			err = askErr
		} else if result, ok := response.(*wasmbuilder.Built); !ok {
			err = fmt.Errorf("unexpected Rust builder response %T", response)
		} else {
			name, artifact, log, err = result.Name, result.Artifact, result.Log, result.Err
		}
	} else {
		if _, err = os.Stat(filepath.Join(workspace, "Cargo.toml")); err != nil {
			return a.fail(ctx, msg.JobID, "Rust WASM builds require a Cargo.toml at the repository root", err)
		}
		name, artifact, log, err = a.runner.Build(ctx, workspace, msg.Target)
	}
	if err != nil {
		label := "Rust"
		if strings.EqualFold(msg.Language, "Go") {
			label = "Go"
		}
		reason := label + " WASM build failed: " + err.Error()
		_ = a.updateWithLog(ctx, msg.JobID, "failed", 100, reason, log)
		return fmt.Errorf("%s WASM build failed: %w", label, err)
	}
	if a.compressor != nil {
		response, compressErr := actor.Ask(ctx, a.compressor, &compressor.Compress{Archive: artifact}, buildTimeout)
		if compressErr != nil {
			return a.failWithLog(ctx, msg.JobID, "WASM artifact compression failed", log, compressErr)
		}
		compressed, ok := response.(*compressor.Compressed)
		if !ok {
			return a.failWithLog(ctx, msg.JobID, "WASM artifact compression failed", log, fmt.Errorf("unexpected compressor response %T", response))
		}
		if compressed.Err != nil {
			return a.failWithLog(ctx, msg.JobID, "WASM artifact compression failed", log, compressed.Err)
		}
		artifact = compressed.Data
		name += ".zst"
		boundedLog := &limitedBuffer{limit: maxBuildLogBytes}
		_, _ = boundedLog.Write(log)
		_, _ = boundedLog.Write([]byte("\nArtifact compression: " + compressed.Report))
		log = boundedLog.Bytes()
	}
	if len(artifact) == 0 || len(artifact) > maxArtifactBytes {
		return a.failWithLog(ctx, msg.JobID, "WASM artifact is empty or exceeds the 128 MiB limit", log, errors.New("artifact size limit exceeded"))
	}
	response, err := a.ask(ctx, &persistence.CompleteWASMBuild{ID: msg.JobID, ArtifactName: name, Artifact: artifact, Log: log})
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
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.HasPrefix(name, "../") {
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

type dockerRunner struct {
	image  string
	docker *actor.PID
}

func (d dockerRunner) Build(ctx context.Context, workspace, target string) (string, []byte, []byte, error) {
	if !validTarget[target] {
		return "", nil, nil, errors.New("unsupported WASM target")
	}
	if !validDockerImage.MatchString(d.image) {
		return "", nil, nil, errors.New("configure JANUS_WASM_BUILDER_IMAGE with a trusted, pre-provisioned Rust builder image")
	}
	command := "cp -a /src/. /workspace/ && cargo build --locked --offline --release --target " + target + " >&2 && cd /workspace/target/" + target + "/release && find . -type f -name '*.wasm' -print0 | tar --null -T - -cf -"
	if d.docker == nil {
		return "", nil, nil, errors.New("Docker controller is unavailable")
	}
	response, err := actor.Ask(ctx, d.docker, &dockeractor.Run{Image: d.image, Workspace: workspace, Command: command, MaxLog: maxBuildLogBytes, MaxOutput: maxArtifactBytes + (1 << 20)}, buildTimeout)
	if err != nil {
		return "", nil, nil, err
	}
	run, ok := response.(*dockeractor.Ran)
	if !ok {
		return "", nil, nil, fmt.Errorf("unexpected Docker controller response %T", response)
	}
	if run.Err != nil {
		return "", nil, run.Stderr, run.Err
	}
	bundle, err := zipWASMArtifactsFromTar(run.Stdout)
	if err != nil {
		return "", nil, run.Stderr, err
	}
	return "wasm-artifacts.zip", bundle, run.Stderr, nil
}

func zipWASMArtifactsFromTar(data []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	reader := tar.NewReader(bytes.NewReader(data))
	var total int64
	count := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("read WASM artifact stream: %w", err)
		}
		name := strings.TrimPrefix(path.Clean(header.Name), "./")
		if header.Typeflag != tar.TypeReg || !strings.EqualFold(path.Ext(name), ".wasm") {
			continue
		}
		if !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.HasPrefix(name, "../") {
			_ = writer.Close()
			return nil, errors.New("WASM artifact stream contains an unsafe path")
		}
		if header.Size < 0 || header.Size > maxArtifactBytes || total+header.Size > maxArtifactBytes {
			_ = writer.Close()
			return nil, errors.New("WASM output exceeds the 128 MiB artifact limit")
		}
		entry, err := writer.Create(name)
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		written, err := io.CopyN(entry, reader, header.Size)
		if err != nil || written != header.Size {
			_ = writer.Close()
			return nil, fmt.Errorf("read WASM artifact %s: %w", name, err)
		}
		total += written
		count++
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, errors.New("Rust build completed without producing a .wasm artifact")
	}
	if buffer.Len() > maxArtifactBytes {
		return nil, errors.New("WASM archive exceeds the 128 MiB artifact limit")
	}
	return buffer.Bytes(), nil
}

func zipWASMArtifacts(root string) ([]byte, error) {
	var paths []string
	var total int64
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(name), ".wasm") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > maxArtifactBytes {
			return errors.New("WASM output exceeds the 128 MiB artifact limit")
		}
		paths = append(paths, name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, errors.New("Rust build completed without producing a .wasm artifact")
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range paths {
		data, err := os.ReadFile(name)
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		file, err := writer.Create(filepath.ToSlash(rel))
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		if _, err = io.Copy(file, bytes.NewReader(data)); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() > maxArtifactBytes {
		return nil, errors.New("WASM archive exceeds the 128 MiB artifact limit")
	}
	return buffer.Bytes(), nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	written := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			b.truncated = true
			data = data[:remaining]
		}
		_, _ = b.buffer.Write(data)
	} else if len(data) > 0 {
		b.truncated = true
	}
	return written, nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }
