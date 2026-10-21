package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"janus/internal/adapters/catalogtest"
	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

const rustTarget = "wasm32-unknown-unknown"

// importedProject gives owner a project in catalog whose active Snapshot is
// complete, was detected as language, and holds the Cargo.toml a Rust build
// needs, and records the project and Snapshot in store as a Build would.
func importedProject(t *testing.T, catalog *catalogtest.Memory, store app.Store, owner, project, language string) {
	t.Helper()
	ctx := context.Background()
	must(t, store.AddOwner(ctx, owner))
	_, err := catalog.CreateProject(ctx, app.CreateProject{ID: project, OwnerID: owner, Name: project})
	must(t, err)
	catalog.SeedSnapshot(owner, project, catalogtest.Seed{ID: project + "-snapshot", Repository: owner + "/" + project, Commit: strings.Repeat("b", 40), Language: language, Files: []app.SnapshotFile{
		{Path: "Cargo.toml", Content: []byte("[package]\nname='fixture'\nversion='0.1.0'\nedition='2021'\n"), Mode: "100644"},
		{Path: "src/lib.rs", Content: []byte("#![no_std]\n"), Mode: "100644"},
	}})
	storetest.ReadySnapshot(t, store, owner, project, project+"-snapshot", language)
}

// storedBuilds is an app.WASMBuilds that serves the Artifacts the tests put in
// it, so a test controls the bytes a Run or a Deployment gets. The Build
// operations are not used by those tests and refuse.
type storedBuilds struct {
	mu        sync.Mutex
	artifacts map[string]storedArtifact
}

type storedArtifact struct {
	owner string
	app.WASMBuildArtifact
}

func newStoredBuilds() *storedBuilds {
	return &storedBuilds{artifacts: map[string]storedArtifact{}}
}

// put stores the Artifact of a Build of owner.
func (b *storedBuilds) put(owner, buildID string, artifact app.WASMBuildArtifact) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.artifacts[buildID] = storedArtifact{owner: owner, WASMBuildArtifact: artifact}
}

func (b *storedBuilds) Artifact(_ context.Context, owner, buildID string) (app.WASMBuildArtifact, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, ok := b.artifacts[buildID]
	if !ok || stored.owner != owner {
		return app.WASMBuildArtifact{}, nil
	}
	return stored.WASMBuildArtifact, nil
}

var errNotUsed = errors.New("the test does not build")

func (b *storedBuilds) StartBuild(context.Context, app.StartBuild) (app.WASMBuild, error) {
	return app.WASMBuild{}, errNotUsed
}

func (b *storedBuilds) RetryBuild(context.Context, string, string) (app.WASMBuild, error) {
	return app.WASMBuild{}, errNotUsed
}

func (b *storedBuilds) FindCheck(context.Context, string, string) (app.Check, bool, error) {
	return app.Check{}, false, errNotUsed
}

func (b *storedBuilds) FindBuild(context.Context, string, string) (app.WASMBuild, bool, error) {
	return app.WASMBuild{}, false, errNotUsed
}

func (b *storedBuilds) DeleteOwner(context.Context, string) error { return errNotUsed }

var _ app.BuildArtifacts = (*storedBuilds)(nil)
