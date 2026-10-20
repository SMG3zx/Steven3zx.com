package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"janus/internal/adapters/audittest"
	"janus/internal/adapters/buildertest"
	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

const rustTarget = "wasm32-unknown-unknown"

// importedProject creates owner's project whose active Snapshot is complete,
// was detected as language, and holds the Cargo.toml a Rust build needs.
func importedProject(t *testing.T, store app.Store, owner, project, language string) {
	t.Helper()
	ctx := context.Background()
	if found, err := store.FindUser(ctx, owner); err != nil {
		t.Fatal(err)
	} else if !found.Found {
		must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: owner, Email: owner + "@example.com", PasswordHash: "hash"}))
	}
	commit := strings.Repeat("b", 40)
	job, snapshot := project+"-import", project+"-snapshot"
	_, err := store.CreateProject(ctx, app.CreateProject{ID: project, OwnerID: owner, Name: project})
	must(t, err)
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: job, OwnerID: owner, ProjectID: project, Repository: owner + "/" + project, Branch: "main", Commit: commit}))
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: snapshot, JobID: job, OwnerID: owner, ProjectID: project, Repository: owner + "/" + project, Branch: "main", Commit: commit, MetadataJSON: "{}", Language: language}))
	must(t, store.AddSnapshotFiles(ctx, snapshot, []app.SnapshotFile{
		{Path: "Cargo.toml", Content: []byte("[package]\nname='fixture'\nversion='0.1.0'\nedition='2021'\n"), Mode: "100644"},
		{Path: "src/lib.rs", Content: []byte("#![no_std]\n"), Mode: "100644"},
	}))
	for _, status := range []string{"cloning", "scanning", "storing"} {
		must(t, store.UpdateImportJob(ctx, app.UpdateImportJob{ID: job, Status: status, Progress: 50}))
	}
	must(t, store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: snapshot, JobID: job, FileCount: 2, TotalBytes: 70}))
}

type buildFixture struct {
	t      *testing.T
	store  app.Store
	audit  *audittest.Recorder
	builds *app.Builds
}

func newBuildFixture(t *testing.T, language string, builder app.Builder) *buildFixture {
	t.Helper()
	store := storetest.NewMemory()
	importedProject(t, store, "owner", "project", language)
	audit := audittest.NewRecorder()
	return &buildFixture{t: t, store: store, audit: audit, builds: app.NewBuilds(store, builder, audit)}
}

func (f *buildFixture) wait() {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := f.builds.Wait(ctx); err != nil {
		f.t.Fatal(err)
	}
}

func (f *buildFixture) build(id string) app.WASMBuild {
	f.t.Helper()
	build, found, err := f.store.FindWASMBuild(context.Background(), id, "owner")
	must(f.t, err)
	if !found {
		f.t.Fatalf("build %s not found", id)
	}
	return build
}

func TestBuildCompilesTheSnapshotAndStoresAnOwnerScopedArtifact(t *testing.T) {
	builder := buildertest.NewFake()
	f := newBuildFixture(t, "Rust", builder)
	ctx := context.Background()

	id, err := f.builds.Request(ctx, app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget})
	must(t, err)
	f.wait()

	build := f.build(id)
	if build.Status != "complete" || build.SnapshotID != "project-snapshot" || len(builder.Requests()) != 1 {
		t.Fatalf("build = %+v after %d builder calls", build, len(builder.Requests()))
	}
	found, ok, err := f.builds.Find(ctx, "owner", id)
	if err != nil || !ok || found.Status != "complete" {
		t.Fatalf("Find = %+v, %v, %v", found, ok, err)
	}
	artifact, err := f.builds.Artifact(ctx, "owner", id)
	must(t, err)
	if !artifact.Found || artifact.Name != "wasm-artifacts.zip.zst" || len(artifact.Data) == 0 {
		t.Fatalf("artifact = %+v", artifact)
	}
	if other, err := f.builds.Artifact(ctx, "other", id); err != nil || other.Found {
		t.Fatalf("another owner got the artifact: %+v, %v", other, err)
	}
	events := f.audit.Events()
	if len(events) != 2 || events[0].Name != "project.wasm.build.queued" || events[1].Name != "project.wasm.build.completed" {
		t.Fatalf("audit events = %+v", events)
	}
}

func TestBuildForAnUnbuildableLanguageIsRejectedAndNothingIsQueued(t *testing.T) {
	builder := buildertest.NewFake()
	f := newBuildFixture(t, "Python", builder)
	_, err := f.builds.Request(context.Background(), app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget})
	if err == nil || !strings.Contains(err.Error(), "not available for Python projects yet") {
		t.Fatalf("error = %v, want an unbuildable-language rejection", err)
	}
	if len(builder.Requests()) != 0 {
		t.Fatalf("builder ran for an unbuildable language: %d calls", len(builder.Requests()))
	}
	if queued, err := f.store.ListResumableBuilds(context.Background()); err != nil || len(queued) != 0 {
		t.Fatalf("a rejected build was queued: %#v, %v", queued, err)
	}
}

func TestBuildRefusesWhatItCannotBuild(t *testing.T) {
	f := newBuildFixture(t, "Rust", buildertest.NewFake())
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		request app.NewBuild
		want    string
	}{
		{"unknown target", app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: "x86"}, "unsupported WASM target"},
		{"no project", app.NewBuild{OwnerID: "owner", ProjectID: "", Target: rustTarget}, "project is required"},
		{"another owner", app.NewBuild{OwnerID: "intruder", ProjectID: "project", Target: rustTarget}, "project not found"},
		{"unknown snapshot", app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget, SnapshotID: "nope"}, "snapshot not found"},
		{"target of another language", app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: "wasip1-wasm"}, ""},
	} {
		_, err := f.builds.Request(ctx, c.request)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want %q", c.name, err, c.want)
		}
	}
	nobuilder := app.NewBuilds(f.store, nil, f.audit)
	if _, err := nobuilder.Request(ctx, app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget}); err == nil || err.Error() != "WASM build runner is not configured" {
		t.Errorf("no builder: %v", err)
	}
}

func TestJavaScriptSnapshotIsBuiltAsJavaScript(t *testing.T) {
	js := &recordingBuilder{}
	f := newBuildFixture(t, "JavaScript/TypeScript", js)
	id, err := f.builds.Request(context.Background(), app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: "wasm32-wasip1"})
	must(t, err)
	f.wait()
	if !strings.EqualFold(js.language, "JavaScript/TypeScript") || js.request.Target != "wasm32-wasip1" || js.request.Workspace == "" {
		t.Fatalf("builder asked for %s %+v", js.language, js.request)
	}
	if build := f.build(id); build.Status != "complete" {
		t.Fatalf("build status = %q (%s)", build.Status, build.Error)
	}
}

func TestJavaScriptSnapshotRejectsAGoOrRustTarget(t *testing.T) {
	f := newBuildFixture(t, "JavaScript/TypeScript", buildertest.NewFake())
	_, err := f.builds.Request(context.Background(), app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: "wasip1-wasm"})
	if err == nil || !strings.Contains(err.Error(), "require target wasm32-wasip1") {
		t.Fatalf("error = %v, want a target rejection", err)
	}
}

func TestResumedBuildRestartsFromScratchAndCompletes(t *testing.T) {
	builder := buildertest.NewFake()
	f := newBuildFixture(t, "Rust", builder)
	must(t, f.store.CreateWASMBuild(context.Background(), app.CreateWASMBuild{ID: "build-1", OwnerID: "owner", ProjectID: "project", SnapshotID: "project-snapshot", Target: rustTarget}))

	count, err := f.builds.Resume(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("resume = %d, %v; want one build", count, err)
	}
	f.wait()
	if build := f.build("build-1"); build.Status != "complete" {
		t.Fatalf("resumed build status = %q (%s)", build.Status, build.Error)
	}
	if len(builder.Requests()) != 1 {
		t.Fatalf("builder calls = %d, want exactly one full build from scratch", len(builder.Requests()))
	}
}

func TestRetryBuildUsesTheFailedBuildsSnapshot(t *testing.T) {
	f := newBuildFixture(t, "Rust", buildertest.NewFake())
	ctx := context.Background()
	must(t, f.store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "build-failed", OwnerID: "owner", ProjectID: "project", SnapshotID: "project-snapshot", Target: rustTarget}))
	must(t, f.store.UpdateWASMBuild(ctx, app.UpdateWASMBuild{ID: "build-failed", Status: "failed", Progress: 100, Error: "compiler crashed"}))

	id, err := f.builds.Retry(ctx, "owner", "build-failed")
	if err != nil || id == "" || id == "build-failed" {
		t.Fatalf("retry = %q, %v; want a new build", id, err)
	}
	f.wait()
	if build := f.build(id); build.Status != "complete" || build.SnapshotID != "project-snapshot" {
		t.Fatalf("retried build = status %q snapshot %q", build.Status, build.SnapshotID)
	}
}

func TestRetryBuildRejectsABuildThatHasNotFailedOrDoesNotExist(t *testing.T) {
	f := newBuildFixture(t, "Rust", buildertest.NewFake())
	ctx := context.Background()
	must(t, f.store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "build-queued", OwnerID: "owner", ProjectID: "project", SnapshotID: "project-snapshot", Target: rustTarget}))
	if _, err := f.builds.Retry(ctx, "owner", "build-queued"); err == nil || !strings.Contains(err.Error(), "only a failed WASM build") {
		t.Fatalf("retry of a queued build = %v, want rejection", err)
	}
	if _, err := f.builds.Retry(ctx, "owner", "nope"); err == nil || err.Error() != "WASM build not found" {
		t.Fatalf("retry of an unknown build = %v", err)
	}
}

// blockingBuilder holds every build until release is closed, and counts how many
// ran at once.
type blockingBuilder struct {
	started chan string
	release chan struct{}
	mu      sync.Mutex
	running int
	peak    int
	cancels chan struct{}
}

func newBlockingBuilder() *blockingBuilder {
	return &blockingBuilder{started: make(chan string, 8), release: make(chan struct{}), cancels: make(chan struct{}, 8)}
}

func (b *blockingBuilder) Build(ctx context.Context, language string, _ app.BuildRequest) (app.BuildResult, error) {
	b.mu.Lock()
	b.running++
	b.peak = max(b.peak, b.running)
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.running--
		b.mu.Unlock()
	}()
	b.started <- language
	select {
	case <-b.release:
		return app.BuildResult{Name: "wasm-artifacts.zip", Artifact: []byte("archive"), Log: []byte("built")}, nil
	case <-ctx.Done():
		b.cancels <- struct{}{}
		return app.BuildResult{Log: []byte("stopped")}, ctx.Err()
	}
}

func TestBuildsOverlapUpToTwoAndAThirdIsRefused(t *testing.T) {
	blocking := newBlockingBuilder()
	f := newBuildFixture(t, "Rust", blocking)
	for _, project := range []string{"second", "third"} {
		importedProject(t, f.store, "owner", project, "Rust")
	}
	ctx := context.Background()
	request := func(project string) (string, error) {
		return f.builds.Request(ctx, app.NewBuild{OwnerID: "owner", ProjectID: project, Target: rustTarget})
	}
	first, err := request("project")
	must(t, err)
	second, err := request("second")
	must(t, err)
	<-blocking.started
	<-blocking.started
	blocking.mu.Lock()
	overlapping := blocking.peak
	blocking.mu.Unlock()
	if overlapping != 2 {
		t.Fatalf("builds that overlapped = %d, want 2", overlapping)
	}
	_, err = request("third")
	if err == nil || err.Error() != "two WASM builds are already running; retry when one finishes" {
		t.Fatalf("third build: %v", err)
	}
	close(blocking.release)
	f.wait()
	for _, id := range []string{first, second} {
		if build := f.build(id); build.Status != "complete" {
			t.Fatalf("build %s = %+v", id, build)
		}
	}
	if _, err = request("third"); err != nil {
		t.Fatalf("a build after the others finished: %v", err)
	}
	<-blocking.started
	f.wait()
}

func TestResumedBuildsQueueBehindTheLimitAndStartWhenASlotFrees(t *testing.T) {
	blocking := newBlockingBuilder()
	f := newBuildFixture(t, "Rust", blocking)
	for _, project := range []string{"second", "third"} {
		importedProject(t, f.store, "owner", project, "Rust")
	}
	for _, project := range []string{"project", "second", "third"} {
		must(t, f.store.CreateWASMBuild(context.Background(), app.CreateWASMBuild{ID: project + "-build", OwnerID: "owner", ProjectID: project, SnapshotID: project + "-snapshot", Target: rustTarget}))
	}
	count, err := f.builds.Resume(context.Background())
	if err != nil || count != 3 {
		t.Fatalf("resume = %d, %v", count, err)
	}
	<-blocking.started
	<-blocking.started
	select {
	case <-blocking.started:
		t.Fatal("a third build started while two were running")
	case <-time.After(100 * time.Millisecond):
	}
	blocking.release <- struct{}{} // frees one slot
	<-blocking.started
	close(blocking.release)
	f.wait()
	for _, project := range []string{"project", "second", "third"} {
		if build := f.build(project + "-build"); build.Status != "complete" {
			t.Fatalf("%s build = %+v", project, build)
		}
	}
}

func TestAPanickingBuilderFailsTheBuildAndKeepsTheProcess(t *testing.T) {
	f := newBuildFixture(t, "Rust", panicBuilder{})
	id, err := f.builds.Request(context.Background(), app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget})
	must(t, err)
	f.wait()
	build := f.build(id)
	if build.Status != "failed" || !strings.Contains(build.Error, "boom") {
		t.Fatalf("build = %+v, want a failed build that keeps the panic", build)
	}
	if _, err = f.builds.Request(context.Background(), app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget}); err != nil {
		t.Fatalf("the slot was not freed after a panic: %v", err)
	}
	f.wait()
	var failures int
	for _, event := range f.audit.Events() {
		if event.Name == "project.wasm.build.failed" {
			failures++
		}
	}
	if failures != 2 {
		t.Fatalf("failed audit events = %d, want 2", failures)
	}
}

func TestCancelOwnerStopsARunningBuild(t *testing.T) {
	blocking := newBlockingBuilder()
	f := newBuildFixture(t, "Rust", blocking)
	id, err := f.builds.Request(context.Background(), app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget})
	must(t, err)
	<-blocking.started
	if got := f.builds.CancelOwner("owner"); got != 1 {
		t.Fatalf("CancelOwner = %d, want 1", got)
	}
	<-blocking.cancels
	f.wait()
	if build := f.build(id); build.Status == "complete" {
		t.Fatalf("a cancelled build completed: %+v", build)
	}
	if got := f.builds.CancelOwner("owner"); got != 0 {
		t.Fatalf("CancelOwner after the build ended = %d", got)
	}
}

func TestDrainRefusesNewBuildsAndWaitsForRunningOnes(t *testing.T) {
	blocking := newBlockingBuilder()
	f := newBuildFixture(t, "Rust", blocking)
	id, err := f.builds.Request(context.Background(), app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget})
	must(t, err)
	<-blocking.started

	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err = f.builds.Drain(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain with a build running = %v", err)
	}
	if _, err = f.builds.Request(context.Background(), app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget}); err == nil || err.Error() != "WASM builds are shutting down" {
		t.Fatalf("request while draining: %v", err)
	}
	close(blocking.release)
	if err = f.builds.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if build := f.build(id); build.Status != "complete" {
		t.Fatalf("build after Drain = %+v", build)
	}
}

// recordingBuilder answers every build with a fixed module and remembers the
// language and request it received.
type recordingBuilder struct {
	language string
	request  app.BuildRequest
}

func (b *recordingBuilder) Build(_ context.Context, language string, request app.BuildRequest) (app.BuildResult, error) {
	b.language, b.request = language, request
	return app.BuildResult{Name: "wasm-artifacts.zip", Artifact: []byte("zip fixture"), Log: []byte("bundled and compiled")}, nil
}

type panicBuilder struct{}

func (panicBuilder) Build(context.Context, string, app.BuildRequest) (app.BuildResult, error) {
	panic("boom")
}
