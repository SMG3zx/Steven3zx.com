package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"janus/internal/adapters/runnertest"
	"janus/internal/adapters/storetest"
	"janus/internal/app"
	"janus/internal/domain/artifact"
)

// compressedArtifact returns the stored form of an Artifact whose only module is module.
func compressedArtifact(t *testing.T, module []byte) (name string, data []byte) {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("main.wasm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(module); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	compressed, err := artifact.Compress(context.Background(), archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return "wasm-artifacts.zip" + artifact.ZstdExtension, compressed.Data
}

// completedBuild stores a finished Build of module for owner and returns its ID.
func completedBuild(t *testing.T, store app.Store, owner, id string, module []byte) string {
	t.Helper()
	ctx := context.Background()
	if found, err := store.FindUser(ctx, owner); err != nil {
		t.Fatal(err)
	} else if !found.Found {
		must(t, store.CreateLocalUser(ctx, app.CreateLocalUser{ID: owner, Email: owner + "@example.com", PasswordHash: "hash"}))
		_, err = store.CreateProject(ctx, app.CreateProject{ID: owner + "-project", OwnerID: owner, Name: "p"})
		must(t, err)
	}
	project := owner + "-project"
	commit := strings.Repeat("a", 40)
	job, snapshot := id+"-job", id+"-snapshot"
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: job, OwnerID: owner, ProjectID: project, Repository: "o/r", Branch: "main", Commit: commit}))
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: snapshot, JobID: job, OwnerID: owner, ProjectID: project, Repository: "o/r", Branch: "main", Commit: commit, MetadataJSON: "{}", Language: "Go"}))
	storetest.AdvanceImport(t, store, job, "queued", "cloning", "scanning", "storing")
	must(t, store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: snapshot, JobID: job}))
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: id, OwnerID: owner, ProjectID: project, SnapshotID: snapshot, Target: "wasip1-wasm"}))
	name, data := compressedArtifact(t, module)
	must(t, store.CompleteWASMBuild(ctx, app.CompleteWASMBuild{ID: id, ArtifactName: name, Artifact: data, Log: []byte("ok")}))
	return id
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func waitRuns(t *testing.T, runs *app.Runs) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runs.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRunsExecuteACompletedBuildAndKeepItsOutput(t *testing.T) {
	store := storetest.NewMemory()
	build := completedBuild(t, store, "alice", "build-1", runnertest.Hello)
	runs := app.NewRuns(store, runnertest.NewFake())

	id, err := runs.Request(context.Background(), "alice", build, []string{"one"})
	must(t, err)
	waitRuns(t, runs)

	run, found := runs.Get("alice", id)
	if !found || run.Status != "complete" || run.Stdout != "hi\n" || run.BuildID != build || run.Module != "main.wasm" || run.Usage.Fuel == 0 {
		t.Fatalf("run = %+v, found %v", run, found)
	}
	if _, found = runs.Get("bob", id); found {
		t.Fatal("another Account saw the run")
	}
	if _, found = runs.Get("alice", "unknown"); found {
		t.Fatal("an unknown run was found")
	}
}

func TestRunsRefuseWhatCannotRun(t *testing.T) {
	store := storetest.NewMemory()
	build := completedBuild(t, store, "alice", "build-1", runnertest.Hello)
	runs := app.NewRuns(store, runnertest.NewFake())
	ctx := context.Background()

	if _, err := runs.Request(ctx, "bob", build, nil); err == nil || err.Error() != "completed WASM build not found" {
		t.Errorf("another Account's build: %v", err)
	}
	if _, err := runs.Request(ctx, "alice", "missing", nil); err == nil || err.Error() != "completed WASM build not found" {
		t.Errorf("unknown build: %v", err)
	}
	if _, err := runs.Request(ctx, "alice", build, make([]string, 65)); err == nil || err.Error() != "at most 64 WASI arguments are allowed" {
		t.Errorf("too many arguments: %v", err)
	}
	if _, err := runs.Request(ctx, "alice", build, []string{strings.Repeat("x", 4097)}); err == nil || err.Error() != "WASI argument exceeds 4096 bytes" {
		t.Errorf("long argument: %v", err)
	}
}

// blockingRunner holds every Run until release is closed.
type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingRunner) Run(context.Context, []byte, app.RunInput) (app.RunResult, error) {
	b.started <- struct{}{}
	<-b.release
	return app.RunResult{Stdout: []byte("done")}, nil
}

func TestRunsLimitConcurrentRunsToTwo(t *testing.T) {
	store := storetest.NewMemory()
	build := completedBuild(t, store, "alice", "build-1", runnertest.Hello)
	runner := &blockingRunner{started: make(chan struct{}, 3), release: make(chan struct{})}
	runs := app.NewRuns(store, runner)
	ctx := context.Background()

	first, err := runs.Request(ctx, "alice", build, nil)
	must(t, err)
	_, err = runs.Request(ctx, "alice", build, nil)
	must(t, err)
	<-runner.started
	<-runner.started
	if _, err = runs.Request(ctx, "alice", build, nil); err == nil || err.Error() != "two WASM runs are already active" {
		t.Fatalf("third concurrent run: %v", err)
	}
	if run, _ := runs.Get("alice", first); run.Status != "running" {
		t.Fatalf("status while running = %q", run.Status)
	}
	close(runner.release)
	waitRuns(t, runs)
	if _, err = runs.Request(ctx, "alice", build, nil); err != nil {
		t.Fatalf("a run after the others finished: %v", err)
	}
	<-runner.started
	waitRuns(t, runs)
}

func TestRunsKeepAtMostOneHundredRuns(t *testing.T) {
	store := storetest.NewMemory()
	build := completedBuild(t, store, "alice", "build-1", runnertest.Hello)
	runs := app.NewRuns(store, runnertest.NewFake())
	var ids []string
	for i := 0; i < 130; i++ {
		var id string
		var err error
		for {
			id, err = runs.Request(context.Background(), "alice", build, nil)
			if err == nil || err.Error() != "two WASM runs are already active" {
				break
			}
			waitRuns(t, runs)
		}
		must(t, err)
		ids = append(ids, id)
		time.Sleep(time.Millisecond) // distinct creation times, so "oldest" is well defined
	}
	waitRuns(t, runs)
	kept := 0
	for _, id := range ids {
		if _, found := runs.Get("alice", id); found {
			kept++
		}
	}
	if kept != 100 {
		t.Fatalf("kept %d runs, want 100", kept)
	}
	if _, found := runs.Get("alice", ids[0]); found {
		t.Fatal("the oldest run was not evicted")
	}
	if _, found := runs.Get("alice", ids[len(ids)-1]); !found {
		t.Fatal("the newest run was evicted")
	}
}

func TestRunsHoldTheirLimitsUnderManyCallers(t *testing.T) {
	store := storetest.NewMemory()
	build := completedBuild(t, store, "alice", "build-1", runnertest.Hello)
	runner := &blockingRunner{started: make(chan struct{}, 64), release: make(chan struct{})}
	runs := app.NewRuns(store, runner)
	var accepted, refused int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			_, err := runs.Request(context.Background(), "alice", build, nil)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				accepted++
			} else if err.Error() == "two WASM runs are already active" {
				refused++
			}
		})
	}
	wg.Wait()
	if accepted != 2 || refused != 30 {
		t.Fatalf("accepted %d and refused %d, want 2 and 30", accepted, refused)
	}
	close(runner.release)
	waitRuns(t, runs)
}

func TestRunsRecordAPanicAsAFailedRun(t *testing.T) {
	store := storetest.NewMemory()
	module := []byte("\x00asm\x01\x00\x00\x00panics")
	build := completedBuild(t, store, "alice", "build-1", module)
	runner := runnertest.NewFake()
	runner.Handle(module, func(app.RunInput) (app.RunResult, error) { panic("boom") })
	runs := app.NewRuns(store, runner)

	id, err := runs.Request(context.Background(), "alice", build, nil)
	must(t, err)
	waitRuns(t, runs)
	run, _ := runs.Get("alice", id)
	if run.Status != "failed" || !strings.Contains(run.Error, "boom") {
		t.Fatalf("run = %+v", run)
	}
	if _, err = runs.Request(context.Background(), "alice", build, nil); err != nil {
		t.Fatalf("the slot was not freed after a panic: %v", err)
	}
	waitRuns(t, runs)
}

func TestRunsRecordARunnerFailure(t *testing.T) {
	store := storetest.NewMemory()
	build := completedBuild(t, store, "alice", "build-1", runnertest.Spins)
	runs := app.NewRuns(store, runnertest.NewFake())
	id, err := runs.Request(context.Background(), "alice", build, nil)
	must(t, err)
	waitRuns(t, runs)
	run, _ := runs.Get("alice", id)
	if run.Status != "failed" || run.Error == "" || run.Usage.Fuel == 0 {
		t.Fatalf("run = %+v", run)
	}
}

func TestDrainRefusesNewRunsAndWaitsForActiveOnes(t *testing.T) {
	store := storetest.NewMemory()
	build := completedBuild(t, store, "alice", "build-1", runnertest.Hello)
	runner := &blockingRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	runs := app.NewRuns(store, runner)
	id, err := runs.Request(context.Background(), "alice", build, nil)
	must(t, err)
	<-runner.started

	drained := make(chan error, 1)
	go func() { drained <- runs.Drain(context.Background()) }()
	// Drain sets the flag before it waits; a request is refused once it has.
	deadline := time.After(5 * time.Second)
	for {
		_, err = runs.Request(context.Background(), "alice", build, nil)
		if err != nil && err.Error() == "WASM runtime is shutting down" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("a request during Drain: %v", err)
		default:
		}
	}
	select {
	case err = <-drained:
		t.Fatalf("Drain returned before the run finished: %v", err)
	default:
	}
	close(runner.release)
	if err = <-drained; err != nil {
		t.Fatal(err)
	}
	if run, _ := runs.Get("alice", id); run.Status != "complete" {
		t.Fatalf("run after Drain = %+v", run)
	}
}

func TestDrainGivesUpWhenItsContextEnds(t *testing.T) {
	store := storetest.NewMemory()
	build := completedBuild(t, store, "alice", "build-1", runnertest.Hello)
	runner := &blockingRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	runs := app.NewRuns(store, runner)
	_, err := runs.Request(context.Background(), "alice", build, nil)
	must(t, err)
	<-runner.started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err = runs.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain = %v, want the deadline error", err)
	}
	close(runner.release)
	waitRuns(t, runs)
}

type unavailableRunner struct{ blockingRunner }

func (*unavailableRunner) Available() bool { return false }

func TestRunsReportAnUnavailableRuntime(t *testing.T) {
	store := storetest.NewMemory()
	build := completedBuild(t, store, "alice", "build-1", runnertest.Hello)
	runs := app.NewRuns(store, &unavailableRunner{})
	_, err := runs.Request(context.Background(), "alice", build, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "Wasmtime runtime unavailable") {
		t.Fatalf("error = %v", err)
	}
	if runs.Wait(context.Background()) != nil {
		t.Fatal("nothing was started, so Wait should return at once")
	}
}
