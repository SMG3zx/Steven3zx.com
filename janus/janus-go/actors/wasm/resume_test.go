package wasm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"

	"janus/actors/builder"
	"janus/actors/persistence"
)

// buildFixture is a persistence actor holding one owner and one project whose
// active Snapshot was imported in language, plus a WASM build actor that uses
// runner.
type buildFixture struct {
	t      *testing.T
	ctx    context.Context
	db     *actor.PID
	builds *actor.PID
	system actor.ActorSystem
}

func newBuildFixture(t *testing.T, language string, runner builder.Builder) *buildFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	system, err := actor.NewActorSystem("wasm-resume-test", actor.WithLogger(golog.DiscardLogger))
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
	db, err := system.Spawn(ctx, "persistence", persistence.New(filepath.Join(t.TempDir(), "wasm-resume.db"), strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_, _ = actor.Ask(closeCtx, db, &persistence.CloseDatabase{}, time.Second)
	})
	f := &buildFixture{t: t, ctx: ctx, db: db, system: system}
	f.ask(&persistence.CreateLocalUser{ID: "owner", Email: "owner@example.com", PasswordHash: "hash"})
	f.importProject("project", language)
	f.builds, err = system.Spawn(ctx, "wasm", New(db, nil, runner))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *buildFixture) ask(message any) any {
	f.t.Helper()
	response, err := actor.Ask(f.ctx, f.db, message, 5*time.Second)
	if err != nil {
		f.t.Fatal(err)
	}
	return response
}

// importProject creates a project whose active Snapshot is complete and detected
// as language, with the Cargo.toml a Rust build needs.
func (f *buildFixture) importProject(project, language string) {
	f.t.Helper()
	commit := strings.Repeat("b", 40)
	job := project + "-import"
	snapshot := project + "-snapshot"
	f.ask(&persistence.CreateProject{ID: project, OwnerID: "owner", Name: project})
	f.ask(&persistence.CreateImportJob{ID: job, OwnerID: "owner", ProjectID: project, Repository: "owner/" + project, Branch: "main", Commit: commit})
	f.ask(&persistence.BeginSnapshot{ID: snapshot, JobID: job, OwnerID: "owner", ProjectID: project, Repository: "owner/" + project, Branch: "main", Commit: commit, MetadataJSON: "{}", Language: language})
	f.ask(&persistence.AddSnapshotFiles{SnapshotID: snapshot, Files: []persistence.SnapshotFile{
		{Path: "Cargo.toml", Content: []byte("[package]\nname='fixture'\nversion='0.1.0'\nedition='2021'\n"), Mode: "100644"},
		{Path: "src/lib.rs", Content: []byte("#![no_std]\n"), Mode: "100644"},
	}})
	for _, status := range []string{"cloning", "scanning", "storing"} {
		f.ask(&persistence.UpdateImportJob{ID: job, Status: status, Progress: 50})
	}
	f.ask(&persistence.CompleteSnapshot{SnapshotID: snapshot, JobID: job, FileCount: 2, TotalBytes: 70})
}

func (f *buildFixture) waitForBuild(job string) persistence.WASMBuild {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		found := f.ask(&persistence.FindWASMBuild{ID: job, OwnerID: "owner"}).(*persistence.WASMBuildFound)
		if found.Build.Status == "complete" || found.Build.Status == "failed" {
			return found.Build
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("build %s did not finish", job)
	return persistence.WASMBuild{}
}

func TestResumedBuildRestartsFromScratchAndCompletes(t *testing.T) {
	runner := &fakeBuilder{}
	f := newBuildFixture(t, "Rust", runner)
	created := f.ask(&persistence.CreateWASMBuild{ID: "build-1", OwnerID: "owner", ProjectID: "project", SnapshotID: "project-snapshot", Target: "wasm32-unknown-unknown"}).(*persistence.WASMBuildCreated)
	if created.Err != nil {
		t.Fatal(created.Err)
	}

	response, err := actor.Ask(f.ctx, f.builds, &ResumeBuilds{}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if resumed := response.(*BuildsResumed); resumed.Err != nil || resumed.Count != 1 {
		t.Fatalf("resume = %#v, want one build", resumed)
	}
	build := f.waitForBuild("build-1")
	if build.Status != "complete" {
		t.Fatalf("resumed build status = %q (%s)", build.Status, build.Error)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want exactly one full build from scratch", runner.calls)
	}
}

func TestBuildForAnUnbuildableLanguageIsRejectedAndNothingIsQueued(t *testing.T) {
	runner := &fakeBuilder{}
	f := newBuildFixture(t, "Python", runner)

	response, err := actor.Ask(f.ctx, f.builds, &RequestBuild{OwnerID: "owner", ProjectID: "project", Target: "wasm32-unknown-unknown"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	requested := response.(*BuildRequested)
	if requested.Err == nil || !strings.Contains(requested.Err.Error(), "not available for Python projects yet") {
		t.Fatalf("build request = %#v, want an unbuildable-language rejection", requested)
	}
	if runner.calls != 0 {
		t.Fatalf("runner ran for an unbuildable language: %d calls", runner.calls)
	}
	queued := f.ask(&persistence.ListResumableBuilds{}).(*persistence.ResumableBuildsListed)
	if len(queued.Builds) != 0 {
		t.Fatalf("a rejected build was queued: %#v", queued.Builds)
	}
}

func TestRetryBuildUsesTheFailedBuildsSnapshot(t *testing.T) {
	runner := &fakeBuilder{}
	f := newBuildFixture(t, "Rust", runner)
	f.ask(&persistence.CreateWASMBuild{ID: "build-failed", OwnerID: "owner", ProjectID: "project", SnapshotID: "project-snapshot", Target: "wasm32-unknown-unknown"})
	f.ask(&persistence.UpdateWASMBuild{ID: "build-failed", Status: "failed", Progress: 100, Error: "compiler crashed"})

	response, err := actor.Ask(f.ctx, f.builds, &RetryBuild{OwnerID: "owner", JobID: "build-failed"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	requested := response.(*BuildRequested)
	if requested.Err != nil || requested.JobID == "" || requested.JobID == "build-failed" {
		t.Fatalf("retry = %#v, want a new build", requested)
	}
	build := f.waitForBuild(requested.JobID)
	if build.Status != "complete" || build.SnapshotID != "project-snapshot" {
		t.Fatalf("retried build = status %q snapshot %q", build.Status, build.SnapshotID)
	}
}

func TestRetryBuildRejectsABuildThatHasNotFailed(t *testing.T) {
	f := newBuildFixture(t, "Rust", &fakeBuilder{})
	f.ask(&persistence.CreateWASMBuild{ID: "build-queued", OwnerID: "owner", ProjectID: "project", SnapshotID: "project-snapshot", Target: "wasm32-unknown-unknown"})

	response, err := actor.Ask(f.ctx, f.builds, &RetryBuild{OwnerID: "owner", JobID: "build-queued"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if requested := response.(*BuildRequested); requested.Err == nil || !strings.Contains(requested.Err.Error(), "only a failed WASM build") {
		t.Fatalf("retry of a queued build = %#v, want rejection", requested)
	}
}

// recordingBuilder answers every build with a fixed module and remembers the
// language and request it received.
type recordingBuilder struct {
	language chan string
	got      chan builder.Request
}

func (b *recordingBuilder) Build(_ context.Context, language string, request builder.Request) (builder.Result, error) {
	b.language <- language
	b.got <- request
	return builder.Result{Name: "wasm-artifacts.zip", Artifact: []byte("zip fixture"), Log: []byte("bundled and compiled")}, nil
}

func TestJavaScriptSnapshotIsBuiltAsJavaScript(t *testing.T) {
	js := &recordingBuilder{language: make(chan string, 1), got: make(chan builder.Request, 1)}
	f := newBuildFixture(t, "JavaScript/TypeScript", js)

	response, err := actor.Ask(f.ctx, f.builds, &RequestBuild{OwnerID: "owner", ProjectID: "project", Target: "wasm32-wasip1"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	requested := response.(*BuildRequested)
	if requested.Err != nil {
		t.Fatal(requested.Err)
	}
	select {
	case got := <-js.got:
		if language := <-js.language; !strings.EqualFold(language, "JavaScript/TypeScript") || got.Target != "wasm32-wasip1" || got.Workspace == "" {
			t.Fatalf("builder asked for %s %+v", language, got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the builder was never asked")
	}
	if build := f.waitForBuild(requested.JobID); build.Status != "complete" {
		t.Fatalf("build status = %q (%s)", build.Status, build.Error)
	}
}

func TestJavaScriptSnapshotRejectsAGoOrRustTarget(t *testing.T) {
	f := newBuildFixture(t, "JavaScript/TypeScript", &fakeBuilder{})
	response, err := actor.Ask(f.ctx, f.builds, &RequestBuild{OwnerID: "owner", ProjectID: "project", Target: "wasip1-wasm"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if requested := response.(*BuildRequested); requested.Err == nil || !strings.Contains(requested.Err.Error(), "require target wasm32-wasip1") {
		t.Fatalf("build request = %#v, want a target rejection", requested)
	}
}
