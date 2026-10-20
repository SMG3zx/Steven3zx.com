package wasm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"
	"janus/actors/builder"
	"janus/actors/persistence"
)

// fakeBuilder builds any Rust workspace into a fixed Artifact.
type fakeBuilder struct{ calls int }

func (b *fakeBuilder) Build(_ context.Context, _ string, request builder.Request) (builder.Result, error) {
	b.calls++
	if request.Target != "wasm32-unknown-unknown" {
		return builder.Result{}, os.ErrInvalid
	}
	if _, err := os.Stat(filepath.Join(request.Workspace, "Cargo.toml")); err != nil {
		return builder.Result{}, err
	}
	return builder.Result{Name: "wasm-artifacts.zip", Artifact: []byte("zip fixture"), Log: []byte("Finished release build")}, nil
}

func TestBuildActorExportsSnapshotBuildsAndReturnsOwnerScopedArtifact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	system, err := actor.NewActorSystem("wasm-build-test", actor.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = system.Stop(stop)
	})
	// This one test runs against a real database, so the whole path from the
	// build actor through the Store to a stored Artifact is covered once.
	dbPID, err := system.Spawn(ctx, "persistence", persistence.New(filepath.Join(t.TempDir(), "wasm.db"), strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_, _ = actor.Ask(closeCtx, dbPID, &persistence.CloseDatabase{}, time.Second)
	})
	store := persistence.NewClient(dbPID, 5*time.Second)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	commit := strings.Repeat("a", 40)
	must(store.CreateLocalUser(ctx, persistence.CreateLocalUser{ID: "owner", Email: "owner@example.com", PasswordHash: "hash"}))
	_, err = store.CreateProject(ctx, persistence.CreateProject{ID: "project", OwnerID: "owner", Name: "Rust WASM"})
	must(err)
	must(store.CreateImportJob(ctx, persistence.CreateImportJob{ID: "import", OwnerID: "owner", ProjectID: "project", Repository: "owner/rust", Branch: "main", Commit: commit}))
	must(store.BeginSnapshot(ctx, persistence.BeginSnapshot{ID: "snapshot", JobID: "import", OwnerID: "owner", ProjectID: "project", Repository: "owner/rust", Branch: "main", Commit: commit, MetadataJSON: "{}", Language: "Rust"}))
	must(store.AddSnapshotFiles(ctx, "snapshot", []persistence.SnapshotFile{
		{Path: "Cargo.toml", Content: []byte("[package]\nname='fixture'\nversion='0.1.0'\nedition='2021'\n"), Mode: "100644"},
		{Path: "src/lib.rs", Content: []byte("#![no_std]\n"), Mode: "100644"},
	}))
	for _, status := range []string{"cloning", "scanning", "storing"} {
		must(store.UpdateImportJob(ctx, persistence.UpdateImportJob{ID: "import", Status: status, Progress: 50}))
	}
	must(store.CompleteSnapshot(ctx, persistence.CompleteSnapshot{SnapshotID: "snapshot", JobID: "import", FileCount: 2, TotalBytes: 70}))
	runner := &fakeBuilder{}
	buildPID, err := system.Spawn(ctx, "wasm", New(store, nil, runner))
	if err != nil {
		t.Fatal(err)
	}
	response, err := actor.Ask(ctx, buildPID, &RequestBuild{OwnerID: "owner", ProjectID: "project", Target: "wasm32-unknown-unknown"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	queued := response.(*BuildRequested)
	if queued.Err != nil || queued.JobID == "" {
		t.Fatalf("build request = %#v: %v", queued, queued.Err)
	}
	var result *persistence.WASMBuildFound
	for {
		response, err = actor.Ask(ctx, buildPID, &Build{OwnerID: "owner", JobID: queued.JobID}, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		result = response.(*BuildFound).BuildFound()
		if result.Build.Status == "complete" || result.Build.Status == "failed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("build did not finish: %#v", result.Build)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if result.Err != nil || result.Build.Status != "complete" || result.Build.SnapshotID != "snapshot" || runner.calls != 1 {
		t.Fatalf("build result = %#v, runner calls=%d", result, runner.calls)
	}
	artifact, err := store.FindWASMBuildArtifact(ctx, queued.JobID, "owner")
	must(err)
	if !artifact.Found || artifact.Name != "wasm-artifacts.zip" || string(artifact.Data) != "zip fixture" {
		t.Fatalf("artifact result = %#v", artifact)
	}
	other, err := store.FindWASMBuildArtifact(ctx, queued.JobID, "other")
	must(err)
	if other.Found {
		t.Fatal("another owner could retrieve the build artifact")
	}
}

func (f *BuildFound) BuildFound() *persistence.WASMBuildFound {
	return &persistence.WASMBuildFound{Build: f.Build, Found: f.Found, Err: f.Err}
}

func TestWriteSourceFileRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../escape", "src/../../escape", `C:\outside`, "src\\..\\escape", "/absolute"} {
		t.Run(name, func(t *testing.T) {
			if err := writeSourceFile(t.TempDir(), persistence.SnapshotFile{Path: name}); err == nil {
				t.Fatalf("accepted unsafe source path %q", name)
			}
		})
	}
}
