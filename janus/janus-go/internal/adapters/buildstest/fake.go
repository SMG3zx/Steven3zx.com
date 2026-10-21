// Package buildstest holds an in-memory app.WASMBuilds for tests that need WASM
// Builds without a container or the builds service. It completes each Build as
// it is started and records its states in a Store, the way the builds events
// would.
package buildstest

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"janus/internal/app"
	"janus/internal/domain/artifact"
)

// Module is the WASM module every Build compiles to, so a test can check the
// Artifact it gets back.
var Module = []byte("\x00asm\x01\x00\x00\x00")

// Result is what the checks of a Build answer with.
type Result struct {
	Passed, NoTests bool
	Log             string
}

// Fake is an app.WASMBuilds that builds nothing. It returns an Artifact holding
// Module, and answers each Build's checks with the Result set by Answer, or with
// the error set by Break. It is safe for concurrent use.
type Fake struct {
	store app.Store

	mu      sync.Mutex
	result  Result
	err     error
	produce []byte
	builds  map[string]*build
}

var _ app.WASMBuilds = (*Fake)(nil)

type build struct {
	app.WASMBuild
	owner    string
	artifact []byte
}

// NewFake returns a Fake whose checks pass. It records each Build's state in
// store, as the builds service's events would.
func NewFake(store app.Store) *Fake {
	return &Fake{store: store, result: Result{Passed: true, Log: "checks passed\n"}, builds: map[string]*build{}}
}

// Answer makes the checks of later Builds answer with result.
func (f *Fake) Answer(result Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result, f.err = result, nil
}

// Break makes the checks of later Builds fail with err, as a container that
// crashed would.
func (f *Fake) Break(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Produce makes later Builds hold data as their Artifact, instead of a ZIP of
// Module. A test that needs the bytes it stored uses it.
func (f *Fake) Produce(data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.produce = append([]byte(nil), data...)
}

// StartBuild completes a Build of the request's Snapshot and runs its checks.
func (f *Fake) StartBuild(ctx context.Context, request app.StartBuild) (app.WASMBuild, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.builds[request.ID]; exists {
		return app.WASMBuild{}, errors.New("a WASM build with this ID exists")
	}
	stored := &build{owner: request.OwnerID, WASMBuild: app.WASMBuild{ID: request.ID, ProjectID: request.ProjectID,
		SnapshotID: request.SnapshotID, Target: request.Target, Status: "queued"}}
	f.builds[request.ID] = stored
	if err := f.record(ctx, stored); err != nil {
		return app.WASMBuild{}, err
	}
	data := f.produce
	if data == nil {
		var err error
		if data, err = pack(ctx); err != nil {
			return app.WASMBuild{}, err
		}
	}
	stored.Status, stored.Progress, stored.ArtifactName, stored.artifact = "complete", 100, "wasm-artifacts.zip"+artifact.ZstdExtension, data
	if err := f.record(ctx, stored); err != nil {
		return app.WASMBuild{}, err
	}
	if err := f.check(ctx, stored); err != nil {
		return app.WASMBuild{}, err
	}
	return stored.WASMBuild, nil
}

// check runs the checks of a complete Build and records them.
func (f *Fake) check(ctx context.Context, stored *build) error {
	change := app.CheckChange{BuildID: stored.ID, OwnerID: stored.owner, Status: "passed", Log: f.result.Log, NoTests: f.result.NoTests}
	switch {
	case f.err != nil:
		change.Status, change.Error, change.Log = "failed", f.err.Error(), ""
	case !f.result.Passed:
		change.Status = "failed"
	}
	return f.store.ApplyCheck(ctx, change)
}

// record stores a Build's state in the Store, as its event would.
func (f *Fake) record(ctx context.Context, stored *build) error {
	return f.store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: stored.ID, OwnerID: stored.owner, ProjectID: stored.ProjectID,
		SnapshotID: stored.SnapshotID, Target: stored.Target, Status: stored.Status, Error: stored.Error})
}

// RetryBuild starts a new Build of a failed Build's Snapshot.
func (f *Fake) RetryBuild(ctx context.Context, owner, buildID string) (app.WASMBuild, error) {
	f.mu.Lock()
	stored, ok := f.builds[buildID]
	f.mu.Unlock()
	if !ok || stored.owner != owner {
		return app.WASMBuild{}, errors.New("WASM build not found")
	}
	if stored.Status != "failed" {
		return app.WASMBuild{}, errors.New("only a failed WASM build can be retried")
	}
	return f.StartBuild(ctx, app.StartBuild{ID: buildID + "-retry", OwnerID: owner, ProjectID: stored.ProjectID, SnapshotID: stored.SnapshotID, Target: stored.Target})
}

// FindBuild returns one of an Owner's Builds.
func (f *Fake) FindBuild(_ context.Context, owner, buildID string) (app.WASMBuild, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, ok := f.builds[buildID]
	if !ok || stored.owner != owner {
		return app.WASMBuild{}, false, nil
	}
	return stored.WASMBuild, true, nil
}

// Artifact returns the Artifact of one of an Owner's complete Builds.
func (f *Fake) Artifact(_ context.Context, owner, buildID string) (app.WASMBuildArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, ok := f.builds[buildID]
	if !ok || stored.owner != owner || stored.Status != "complete" {
		return app.WASMBuildArtifact{}, nil
	}
	return app.WASMBuildArtifact{Name: stored.ArtifactName, Data: append([]byte(nil), stored.artifact...), Found: true}, nil
}

// DeleteOwner forgets an Owner's Builds.
func (f *Fake) DeleteOwner(_ context.Context, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, stored := range f.builds {
		if stored.owner == owner {
			delete(f.builds, id)
		}
	}
	return nil
}

// pack returns the Artifact of a Build: a compressed ZIP holding Module.
func pack(ctx context.Context) ([]byte, error) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("main.wasm")
	if err == nil {
		_, err = file.Write(Module)
	}
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("pack the artifact: %w", err)
	}
	compressed, err := artifact.Compress(ctx, archive.Bytes())
	if err != nil {
		return nil, err
	}
	return compressed.Data, nil
}
