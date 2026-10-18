package wasm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tochemey/goakt/v4/actor"

	"janus/actors/persistence"
)

// buildableLanguages are the languages that have a WASM builder. A Snapshot in
// any other language can still be imported and recorded; it just cannot be
// built yet.
var buildableLanguages = map[string]bool{"rust": true, "go": true}

func unbuildableLanguageError(language string) error {
	if language == "" {
		return errors.New("no buildable language was detected in this snapshot")
	}
	return fmt.Errorf("WASM builds are not available for %s projects yet", language)
}

// buildSource returns the Snapshot a build compiles and its detected language.
// The project's active Snapshot is used unless the request names one.
func (a *Actor) buildSource(ctx context.Context, msg *RequestBuild, project persistence.Project) (string, string, error) {
	if msg.SnapshotID == "" {
		if project.SnapshotID == "" {
			return "", "", errors.New("import a repository before building it")
		}
		return project.SnapshotID, project.Language, nil
	}
	response, err := a.ask(ctx, &persistence.ListSnapshots{OwnerID: msg.OwnerID, ProjectID: msg.ProjectID})
	if err != nil {
		return "", "", err
	}
	listed, ok := response.(*persistence.SnapshotsListed)
	if !ok {
		return "", "", fmt.Errorf("unexpected persistence response %T", response)
	}
	if listed.Err != nil {
		return "", "", listed.Err
	}
	for _, snapshot := range listed.Snapshots {
		if snapshot.ID == msg.SnapshotID {
			return snapshot.ID, snapshot.Language, nil
		}
	}
	return "", "", errors.New("snapshot not found")
}

// resumeBuilds starts every build that was interrupted by the previous shutdown.
// A build whose Snapshot is no longer buildable fails instead of starting.
func (a *Actor) resumeBuilds(ctx context.Context, system actor.ActorSystem) (int, error) {
	response, err := a.ask(ctx, &persistence.ListResumableBuilds{})
	if err != nil {
		return 0, err
	}
	listed, ok := response.(*persistence.ResumableBuildsListed)
	if !ok {
		return 0, fmt.Errorf("unexpected persistence response %T", response)
	}
	if listed.Err != nil {
		return 0, listed.Err
	}
	for _, build := range listed.Builds {
		if !buildableLanguages[strings.ToLower(build.Language)] {
			_ = a.update(ctx, build.JobID, "failed", 100, unbuildableLanguageError(build.Language).Error())
			continue
		}
		a.queueBuild(ctx, system, &RunBuild{OwnerID: build.OwnerID, ProjectID: build.ProjectID, SnapshotID: build.SnapshotID, JobID: build.JobID, Target: build.Target, Language: build.Language})
	}
	return len(listed.Builds), nil
}

// queueBuild starts msg once a build slot is free. It does not block the caller.
func (a *Actor) queueBuild(ctx context.Context, system actor.ActorSystem, msg *RunBuild) {
	go func() {
		a.slots <- struct{}{}
		if err := a.spawnBuild(context.WithoutCancel(ctx), system, msg); err != nil {
			<-a.slots
			_ = a.update(ctx, msg.JobID, "failed", 100, "WASM build worker could not start")
		}
	}()
}

// spawnBuild starts a build worker for msg. The caller must already hold a build
// slot; the worker releases it when it finishes.
func (a *Actor) spawnBuild(ctx context.Context, system actor.ActorSystem, msg *RunBuild) error {
	a.jobs.Add(1)
	worker := &Actor{persistence: a.persistence, accounting: a.accounting, runner: a.runner, rustBuilder: a.rustBuilder, goBuilder: a.goBuilder, compressor: a.compressor, jobs: a.jobs, slots: a.slots}
	pid, err := system.Spawn(ctx, "wasm-build-"+strings.ReplaceAll(msg.JobID, "-", ""), worker)
	if err != nil {
		a.jobs.Done()
		return err
	}
	if err = actor.Tell(context.WithoutCancel(ctx), pid, msg); err != nil {
		a.jobs.Done()
		return err
	}
	return nil
}

// retryBuild starts a new build of the failed build's Snapshot.
func (a *Actor) retryBuild(rctx *actor.ReceiveContext, msg *RetryBuild) (string, error) {
	response, err := a.ask(rctx.Context(), &persistence.FindWASMBuild{ID: msg.JobID, OwnerID: msg.OwnerID})
	if err != nil {
		return "", err
	}
	found, ok := response.(*persistence.WASMBuildFound)
	if !ok {
		return "", fmt.Errorf("unexpected persistence response %T", response)
	}
	if found.Err != nil {
		return "", found.Err
	}
	if !found.Found {
		return "", errors.New("WASM build not found")
	}
	if found.Build.Status != "failed" {
		return "", errors.New("only a failed WASM build can be retried")
	}
	return a.start(rctx, &RequestBuild{OwnerID: msg.OwnerID, ProjectID: found.Build.ProjectID, Target: found.Build.Target, SnapshotID: found.Build.SnapshotID})
}
