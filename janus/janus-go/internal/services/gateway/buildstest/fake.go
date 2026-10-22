// Package buildstest holds an in-memory builds service for the gateway's tests
// that need WASM Builds without a container or the service. It answers with the
// contract's types, completes each Build as it is started and keeps its states and
// Checks itself, as the builds service does.
package buildstest

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"janus/internal/contract"
	contractbuilds "janus/internal/contract/builds"
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

// Fake is a builds service that builds nothing. It returns an Artifact holding
// Module, and answers each Build's checks with the Result set by Answer, or with
// the error set by Break. It is safe for concurrent use.
type Fake struct {
	mu      sync.Mutex
	result  Result
	err     error
	produce []byte
	builds  map[string]*build
}

type build struct {
	contractbuilds.Build
	artifact []byte
	check    *contractbuilds.Check
}

// NewFake returns a Fake whose checks pass.
func NewFake() *Fake {
	return &Fake{result: Result{Passed: true, Log: "checks passed\n"}, builds: map[string]*build{}}
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

// Request completes a Build of the request's Snapshot and runs its checks.
func (f *Fake) Request(ctx context.Context, request contractbuilds.RequestBuild) (contractbuilds.Build, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.builds[request.ID]; exists {
		return contractbuilds.Build{}, errors.New("a WASM build with this ID exists")
	}
	stored := &build{Build: contractbuilds.Build{ID: request.ID, OwnerID: request.OwnerID, ProjectID: request.ProjectID,
		SnapshotID: request.SnapshotID, Target: request.Target, Status: "queued"}}
	f.builds[request.ID] = stored
	data := f.produce
	if data == nil {
		var err error
		if data, err = pack(ctx); err != nil {
			return contractbuilds.Build{}, err
		}
	}
	stored.Status, stored.Progress, stored.ArtifactName, stored.artifact = "complete", 100, "wasm-artifacts.zip"+artifact.ZstdExtension, data
	f.check(stored)
	return stored.Build, nil
}

// check runs the checks of a complete Build and keeps the result.
func (f *Fake) check(stored *build) {
	check := &contractbuilds.Check{BuildID: stored.ID, Status: "passed", Log: f.result.Log, NoTests: f.result.NoTests}
	switch {
	case f.err != nil:
		check.Status, check.Error, check.Log = "failed", f.err.Error(), ""
	case !f.result.Passed:
		check.Status = "failed"
	}
	stored.check = check
}

// Retry starts a new Build of a failed Build's Snapshot.
func (f *Fake) Retry(ctx context.Context, owner, buildID string) (contractbuilds.Build, error) {
	f.mu.Lock()
	stored, ok := f.builds[buildID]
	f.mu.Unlock()
	if !ok || stored.OwnerID != owner {
		return contractbuilds.Build{}, contract.Errorf(contract.StatusNotFound, "WASM build not found")
	}
	if stored.Status != "failed" {
		return contractbuilds.Build{}, contract.Errorf(contract.StatusInvalid, "only a failed WASM build can be retried")
	}
	return f.Request(ctx, contractbuilds.RequestBuild{ID: buildID + "-retry", OwnerID: owner, ProjectID: stored.ProjectID, SnapshotID: stored.SnapshotID, Target: stored.Target})
}

// Find returns one of an Owner's Builds.
func (f *Fake) Find(_ context.Context, owner, buildID string) (contractbuilds.Build, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, ok := f.builds[buildID]
	if !ok || stored.OwnerID != owner {
		return contractbuilds.Build{}, false, nil
	}
	return stored.Build, true, nil
}

// Artifact returns the Artifact of one of an Owner's complete Builds with its
// bytes. Found is false when there is none.
func (f *Fake) Artifact(_ context.Context, owner, buildID string) (contractbuilds.ArtifactFound, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, ok := f.builds[buildID]
	if !ok || stored.OwnerID != owner || stored.Status != "complete" {
		return contractbuilds.ArtifactFound{}, nil, nil
	}
	return contractbuilds.ArtifactFound{Found: true, ArtifactName: stored.ArtifactName}, append([]byte(nil), stored.artifact...), nil
}

// Check returns the Check of one of an Owner's Builds, as it was when the Build was
// checked.
func (f *Fake) Check(_ context.Context, owner, buildID string) (contractbuilds.Check, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, ok := f.builds[buildID]
	if !ok || stored.OwnerID != owner || stored.check == nil {
		return contractbuilds.Check{}, false, nil
	}
	return *stored.check, true, nil
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
