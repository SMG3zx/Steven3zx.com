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
	dbPath := filepath.Join(t.TempDir(), "wasm.db")
	dbPID, err := system.Spawn(ctx, "persistence", persistence.New(dbPath, strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_, _ = actor.Ask(closeCtx, dbPID, &persistence.CloseDatabase{}, time.Second)
	})
	ask := func(message any) any {
		t.Helper()
		response, askErr := actor.Ask(ctx, dbPID, message, 5*time.Second)
		if askErr != nil {
			t.Fatal(askErr)
		}
		return response
	}
	if got := ask(&persistence.CreateLocalUser{ID: "owner", Email: "owner@example.com", PasswordHash: "hash"}).(*persistence.LocalUserCreated); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := ask(&persistence.CreateProject{ID: "project", OwnerID: "owner", Name: "Rust WASM"}).(*persistence.ProjectCreated); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := ask(&persistence.CreateImportJob{ID: "import", OwnerID: "owner", ProjectID: "project", Repository: "owner/rust", Branch: "main", Commit: strings.Repeat("a", 40)}).(*persistence.ImportJobCreated); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := ask(&persistence.BeginSnapshot{ID: "snapshot", JobID: "import", OwnerID: "owner", ProjectID: "project", Repository: "owner/rust", Branch: "main", Commit: strings.Repeat("a", 40), MetadataJSON: "{}", Language: "Rust"}).(*persistence.BeginSnapshotResult); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := ask(&persistence.AddSnapshotFiles{SnapshotID: "snapshot", Files: []persistence.SnapshotFile{
		{Path: "Cargo.toml", Content: []byte("[package]\nname='fixture'\nversion='0.1.0'\nedition='2021'\n"), Mode: "100644"},
		{Path: "src/lib.rs", Content: []byte("#![no_std]\n"), Mode: "100644"},
	}}).(*persistence.SnapshotFilesAdded); got.Err != nil {
		t.Fatal(got.Err)
	}
	for _, status := range []string{"cloning", "scanning", "storing"} {
		ask(&persistence.UpdateImportJob{ID: "import", Status: status, Progress: 50})
	}
	if got := ask(&persistence.CompleteSnapshot{SnapshotID: "snapshot", JobID: "import", FileCount: 2, TotalBytes: 70}).(*persistence.SnapshotCompleted); got.Err != nil {
		t.Fatal(got.Err)
	}
	runner := &fakeBuilder{}
	buildPID, err := system.Spawn(ctx, "wasm", New(persistence.NewClient(dbPID, 5*time.Second), nil, runner))
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
	response, err = actor.Ask(ctx, dbPID, &persistence.FindWASMBuildArtifact{ID: queued.JobID, OwnerID: "owner"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	artifact := response.(*persistence.WASMBuildArtifact)
	if artifact.Err != nil || !artifact.Found || artifact.Name != "wasm-artifacts.zip" || string(artifact.Data) != "zip fixture" {
		t.Fatalf("artifact result = %#v", artifact)
	}
	response, err = actor.Ask(ctx, dbPID, &persistence.FindWASMBuildArtifact{ID: queued.JobID, OwnerID: "other"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if response.(*persistence.WASMBuildArtifact).Found {
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
