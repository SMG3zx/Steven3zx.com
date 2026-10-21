package builds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	contractbuilds "janus/internal/contract/builds"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/domain/artifact"
	"janus/internal/domain/jobs"
	"janus/internal/domain/templates"
	"janus/internal/platform/natsx"
)

// buildConsumer is the durable consumer of Build jobs in JOBS.
const buildConsumer = "builds-builds"

// recordTimeout bounds the writes that record how a Build or Check ended, which
// run even when the Build's own context has ended.
const recordTimeout = 30 * time.Second

// maxProgress is the highest progress reported while the source is read.
const maxProgress = 30

// buildRun is one Build being run by this process. status is the status the
// Build is in as this run last moved it: the From of the next transition, so
// the store detects a concurrent change.
type buildRun struct {
	build  contractbuilds.Build
	status string
}

// startWorkers starts Workers consumers of Build jobs, each taking one job at a
// time, so this process runs at most Workers Builds at once and leaves the rest
// in JOBS for other processes.
func (s *Service) startWorkers(ctx context.Context, stream jetstream.Stream) error {
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       buildConsumer,
		FilterSubject: contractbuilds.SubjectBuildJob,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       s.cfg.AckWait,
		MaxDeliver:    maxDeliveries,
	})
	if err != nil {
		return fmt.Errorf("declare the build consumer: %w", err)
	}
	for range s.cfg.Workers {
		worker, err := consumer.Consume(s.handleBuild, jetstream.PullMaxMessages(1))
		if err != nil {
			return fmt.Errorf("consume build jobs: %w", err)
		}
		s.workers = append(s.workers, worker)
	}
	return nil
}

// handleBuild runs one delivery of a Build job. It acknowledges the job once the
// Build is finished, one way or the other; a job whose Build is not finished,
// because this process is stopping or the database failed, is left for JetStream
// to deliver again, and the Build starts over.
func (s *Service) handleBuild(msg jetstream.Msg) {
	s.wg.Add(1)
	defer s.wg.Done()
	var queued contractbuilds.BuildJobMessage
	if err := json.Unmarshal(msg.Data(), &queued); err != nil || queued.JobID == "" {
		_ = msg.Term()
		return
	}
	ctx := s.stopCtx
	build, found, err := getBuild(ctx, s.db, queued.JobID, "")
	switch {
	case err != nil:
		_ = msg.NakWithDelay(time.Second)
		return
	case !found, jobs.WASM.Terminal(build.Status):
		_ = msg.Ack()
		return
	}
	meta, err := msg.Metadata()
	last := err == nil && meta.NumDelivered >= maxDeliveries
	if build.Status == contractbuilds.StatusBuilding {
		// A worker stopped part-way. Start the Build again, unless this was the
		// last delivery.
		if last {
			s.failInterrupted(ctx, build, "build could not be finished after repeated interruptions; retry it")
			_ = msg.Ack()
			return
		}
		if err := transitionBuild(ctx, s.db, build.ID, contractbuilds.StatusBuilding, contractbuilds.StatusQueued, 0, "", ""); err != nil {
			_ = msg.NakWithDelay(time.Second)
			return
		}
		build.Status = contractbuilds.StatusQueued
	}
	keepAlive := s.keepAlive(msg)
	err = s.runBuildSafely(ctx, build)
	keepAlive()
	if s.closing.Load() {
		return // not acknowledged: JetStream hands it out again
	}
	after, found, findErr := getBuild(context.Background(), s.db, build.ID, "")
	switch {
	case findErr == nil && (!found || jobs.WASM.Terminal(after.Status)):
		_ = msg.Ack()
		s.finished(build, err)
	case findErr == nil && last:
		// No delivery is left to finish it: fail it rather than leave it queued.
		s.failInterrupted(context.Background(), after, "build could not be run; retry it")
		_ = msg.Ack()
		s.finished(build, err)
	default:
		_ = msg.NakWithDelay(5 * time.Second)
	}
}

// keepAlive tells JetStream the job is still being worked on until the returned
// function is called.
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

// finished audits how a Build ended.
func (s *Service) finished(build contractbuilds.Build, err error) {
	ctx := context.Background()
	if err != nil {
		s.audit(ctx, "error", "project.wasm.build.failed", map[string]string{"user_id": build.OwnerID, "project_id": build.ProjectID, "job_id": build.ID, "reason": "build_failed"})
		return
	}
	s.audit(ctx, "info", "project.wasm.build.completed", map[string]string{"user_id": build.OwnerID, "project_id": build.ProjectID, "job_id": build.ID})
}

// failInterrupted fails a Build that stopped part-way too often.
func (s *Service) failInterrupted(ctx context.Context, build contractbuilds.Build, reason string) {
	_ = transitionBuild(ctx, s.db, build.ID, build.Status, contractbuilds.StatusFailed, 100, reason, "")
}

// track registers a running Build so its Owner's deletion can cancel it, and
// returns its context and the function that unregisters it.
func (s *Service) track(parent context.Context, build contractbuilds.Build) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(parent, s.cfg.BuildTimeout)
	s.mu.Lock()
	s.running[build.ID] = runningBuild{owner: build.OwnerID, cancel: cancel}
	s.mu.Unlock()
	return ctx, func() {
		s.mu.Lock()
		delete(s.running, build.ID)
		s.mu.Unlock()
		cancel()
	}
}

// cancelOwner cancels the Owner's Builds running in this process.
func (s *Service) cancelOwner(owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, running := range s.running {
		if running.owner == owner {
			running.cancel()
		}
	}
}

// runBuildSafely is runBuild, except that a panic fails the Build rather than the
// process: a Build compiles and checks untrusted source.
func (s *Service) runBuildSafely(ctx context.Context, build contractbuilds.Build) (err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		err = fmt.Errorf("WASM build panicked: %v", r)
		// The Build is failed from the status it is in when the panic happened.
		current, found, lookupErr := getBuild(context.Background(), s.db, build.ID, "")
		if lookupErr == nil && found && !jobs.WASM.Terminal(current.Status) {
			_ = transitionBuild(context.Background(), s.db, build.ID, current.Status, contractbuilds.StatusFailed, 100, err.Error(), "")
		}
	}()
	return s.runBuild(ctx, build)
}

// runBuild reads the Build's source from the projects service, compiles it and
// stores the Artifact, then runs its Check. It records how the Build ended
// before it returns.
func (s *Service) runBuild(parent context.Context, build contractbuilds.Build) error {
	ctx, release := s.track(parent, build)
	defer release()
	run := &buildRun{build: build, status: build.Status}
	if err := s.move(ctx, run, contractbuilds.StatusBuilding, 5, "", ""); err != nil {
		return err
	}
	snapshot, found, err := s.snapshot(ctx, build.OwnerID, build.ProjectID, build.SnapshotID)
	switch {
	case err != nil:
		return s.fail(ctx, run, "could not read the imported source snapshot", err)
	case !found:
		return s.fail(ctx, run, "snapshot not found", nil)
	}
	template, ok := templates.Buildable(snapshot.Language)
	if !ok {
		return s.fail(ctx, run, unbuildableLanguageError(snapshot.Language).Error(), nil)
	}
	workspace, err := os.MkdirTemp("", "janus-wasm-build-")
	if err != nil {
		return s.fail(ctx, run, "could not allocate build workspace", err)
	}
	defer os.RemoveAll(workspace)
	if err := s.fetchSource(ctx, run, workspace); err != nil {
		return s.fail(ctx, run, "could not read the imported source snapshot", err)
	}
	request := CompileRequest{Workspace: workspace, Target: build.Target}
	if strings.EqualFold(template.Language, "JavaScript/TypeScript") {
		request.Entrypoint = snapshot.EntryPoint
	}
	compiled, err := s.cfg.Compile(ctx, template.Language, request)
	name, data, log := compiled.Name, compiled.Artifact, compiled.Log
	if err != nil {
		var manifest *ManifestError
		if errors.As(err, &manifest) {
			return s.fail(ctx, run, manifest.Message, manifest.Err)
		}
		label := languageLabel(template.Language)
		return s.failWithLog(ctx, run, label+" WASM build failed: "+err.Error(), log, err)
	}
	if len(data) == 0 || len(data) > artifact.MaxBytes {
		cause := errors.New("artifact size limit exceeded")
		return s.failWithLog(ctx, run, withCause("WASM artifact is empty or exceeds the 128 MiB limit", cause), log, cause)
	}
	if err := completeBuild(ctx, s.db, build.ID, name, data, string(log)); err != nil {
		return s.failWithLog(ctx, run, withCause("WASM artifact could not be stored", err), log, err)
	}
	run.status = contractbuilds.StatusComplete
	s.copyArtifact(build, data)
	s.check(ctx, run, template.Language, workspace)
	return nil
}

// copyArtifact copies a stored Artifact to the artifacts store. builds.db already
// has it, so a failure only means the copy is restored when it is next read.
func (s *Service) copyArtifact(build contractbuilds.Build, data []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()
	if _, err := s.artifacts.PutBytes(ctx, build.ID, data); err != nil {
		s.audit(ctx, "error", "project.wasm.artifact.copy_failed", map[string]string{"user_id": build.OwnerID, "project_id": build.ProjectID, "job_id": build.ID})
	}
}

// fetchSource writes the Build's Snapshot into workspace, one page of the
// projects service at a time. A file cut across pages is appended to as its
// parts arrive.
func (s *Service) fetchSource(ctx context.Context, run *buildRun, workspace string) error {
	build := run.build
	offset, contentOffset := 0, 0
	for page := 1; ; page++ {
		var files contractprojects.Files
		err := natsx.Call(ctx, s.conn, contractprojects.SubjectSnapshotFiles, contractprojects.SnapshotFiles{
			OwnerID: build.OwnerID, ProjectID: build.ProjectID, SnapshotID: build.SnapshotID,
			Offset: offset, ContentOffset: contentOffset, Limit: contractprojects.MaxFilePage, MaxBytes: contractprojects.DefaultPageBytes,
		}, &files)
		if err != nil {
			return err
		}
		whole := 0
		for _, file := range files.Files {
			if err := writeSourcePart(workspace, file); err != nil {
				return err
			}
			if file.ContentOffset+len(file.Content) >= int(file.Size) {
				whole++
			}
		}
		if !files.More {
			return nil
		}
		// Only the last file of a page can be cut, so the next page starts at the
		// first file that is not whole, at the byte where the cut was.
		nextOffset, nextContent := offset+whole, 0
		if n := len(files.Files); n > 0 {
			last := files.Files[n-1]
			if end := last.ContentOffset + len(last.Content); end < int(last.Size) {
				nextContent = end
			}
		}
		if nextOffset == offset && nextContent == contentOffset {
			return errors.New("source paging made no progress")
		}
		offset, contentOffset = nextOffset, nextContent
		if err := s.move(ctx, run, contractbuilds.StatusBuilding, min(maxProgress, 4+page), "", ""); err != nil {
			return err
		}
	}
}

// check runs the Checks of a complete Build on its workspace and records the
// result. A Check that cannot run to an answer is recorded as failed and keeps
// its error. The Build stays complete either way, so a failed Check only holds
// back a Deploy.
func (s *Service) check(ctx context.Context, run *buildRun, language, workspace string) {
	if s.cfg.Check == nil {
		return
	}
	// The Check's records use their own context, so a timeout or a shutdown during
	// the checks cannot leave the Check running and block Deploy.
	records, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()
	build := run.build
	fields := map[string]string{"user_id": build.OwnerID, "project_id": build.ProjectID, "job_id": build.ID}
	if err := startCheck(records, s.db, build.ID); err != nil {
		fields["reason"] = "check_not_started"
		s.audit(records, "error", "project.wasm.check.failed", fields)
		return
	}
	result, err := s.cfg.Check(ctx, language, CheckRequest{Workspace: workspace})
	status, errText := checkPassed, ""
	switch {
	case err != nil:
		status, errText = checkFailed, err.Error()
	case !result.Passed:
		status = checkFailed
	}
	if err := finishCheck(records, s.db, build.ID, status, errText, string(result.Log), result.NoTests); err != nil {
		fields["reason"] = "check_not_finished"
		s.audit(records, "error", "project.wasm.check.failed", fields)
	}
}

// move moves a run's Build to status, from the status the run is in.
func (s *Service) move(ctx context.Context, run *buildRun, to string, progress int, reason, log string) error {
	if err := transitionBuild(ctx, s.db, run.build.ID, run.status, to, progress, reason, log); err != nil {
		return err
	}
	run.status = to
	return nil
}

// fail records a Build as failed with reason and cause, and returns the error it
// ends with.
func (s *Service) fail(ctx context.Context, run *buildRun, reason string, cause error) error {
	return s.failWithLog(ctx, run, withCause(reason, cause), nil, cause)
}

// failWithLog records a Build as failed with reason, keeping the compiler's log,
// and returns the error it ends with. reason is what the Account sees, so the
// caller says whether the cause is part of it.
func (s *Service) failWithLog(ctx context.Context, run *buildRun, reason string, log []byte, cause error) error {
	_ = transitionBuild(ctx, s.db, run.build.ID, run.status, contractbuilds.StatusFailed, 100, reason, string(log))
	run.status = contractbuilds.StatusFailed
	if cause == nil {
		return errors.New(reason)
	}
	return fmt.Errorf("%s: %w", reason, cause)
}

// withCause appends the cause to a reason the Account sees.
func withCause(reason string, cause error) string {
	if cause == nil {
		return reason
	}
	return reason + ": " + cause.Error()
}

// writeSourcePart writes one part of a Snapshot file under root. The first part
// of a file creates it; later parts append.
func writeSourcePart(root string, file contractprojects.File) error {
	if !artifact.SafePath(file.Path) {
		return errors.New("invalid repository path")
	}
	destination := filepath.Join(root, filepath.FromSlash(path.Clean(file.Path)))
	if !filepath.IsLocal(relative(root, destination)) {
		return errors.New("repository path escapes build workspace")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if file.ContentOffset == 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	out, err := os.OpenFile(destination, flags, 0600)
	if err != nil {
		return err
	}
	_, err = out.Write(file.Content)
	return errors.Join(err, out.Close())
}

// relative is the path from root to destination, or ".." when there is none.
func relative(root, destination string) string {
	rel, err := filepath.Rel(root, destination)
	if err != nil {
		return ".."
	}
	return rel
}
