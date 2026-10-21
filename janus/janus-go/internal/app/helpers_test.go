package app_test

import (
	"context"
	"strings"
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
