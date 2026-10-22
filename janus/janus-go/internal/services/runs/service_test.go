package runs

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/agent/runnertest"
	"janus/internal/contract"
	contractbuilds "janus/internal/contract/builds"
	contractprojects "janus/internal/contract/projects"
	contractruns "janus/internal/contract/runs"
	"janus/internal/domain/artifact"
	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
	"janus/internal/platform/wasmrun"
)

const (
	testOwner   = "alice"
	testProject = "p1"
	wasiTarget  = "wasm32-wasip1"
)

// fakeBuilds answers the builds subjects the runs service calls. Its Builds and
// Artifacts are the ones a test puts in it.
type fakeBuilds struct {
	mu        sync.Mutex
	builds    map[contractbuilds.BuildKey]contractbuilds.Build
	artifacts map[contractbuilds.BuildKey]contractbuilds.ArtifactFound
}

func newFakeBuilds() *fakeBuilds {
	return &fakeBuilds{builds: map[contractbuilds.BuildKey]contractbuilds.Build{}, artifacts: map[contractbuilds.BuildKey]contractbuilds.ArtifactFound{}}
}

// addBuild records a Build of owner with status and an Artifact named name whose
// object holds data in store.
func (f *fakeBuilds) addBuild(t *testing.T, store jetstream.ObjectStore, owner, id, status string, name string, data []byte) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	key := contractbuilds.BuildKey{OwnerID: owner, BuildID: id}
	f.builds[key] = contractbuilds.Build{ID: id, OwnerID: owner, ProjectID: testProject, SnapshotID: "s1", Target: wasiTarget, Status: status}
	if data != nil {
		if _, err := store.PutBytes(context.Background(), id, data); err != nil {
			t.Fatal(err)
		}
		f.artifacts[key] = contractbuilds.ArtifactFound{Found: true, ObjectName: id, ArtifactName: name}
	}
}

// serve answers the builds subjects on conn until the test ends.
func (f *fakeBuilds) serve(t *testing.T, conn *nats.Conn) {
	t.Helper()
	service, err := micro.AddService(conn, micro.Config{Name: contractbuilds.ServiceName, Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Stop() })
	err = errors.Join(
		natsx.AddEndpoint(service, "find-build", contractbuilds.SubjectFindBuild, func(_ context.Context, key contractbuilds.BuildKey) (contractbuilds.BuildFound, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			build, found := f.builds[key]
			return contractbuilds.BuildFound{Found: found, Build: build}, nil
		}),
		natsx.AddEndpoint(service, "find-artifact", contractbuilds.SubjectFindArtifact, func(_ context.Context, key contractbuilds.BuildKey) (contractbuilds.ArtifactFound, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.artifacts[key], nil
		}),
		natsx.AddEndpoint(service, "restore-artifact", contractbuilds.SubjectRestoreArtifact, func(context.Context, contractbuilds.BuildKey) (contractbuilds.Empty, error) {
			return contractbuilds.Empty{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
}

// serveProjects answers the projects subjects the runs service calls with no
// Snapshots, so every Run gets the default limits.
func serveProjects(t *testing.T, conn *nats.Conn) {
	t.Helper()
	service, err := micro.AddService(conn, micro.Config{Name: contractprojects.ServiceName, Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Stop() })
	err = natsx.AddEndpoint(service, "list-snapshots", contractprojects.SubjectListSnapshots, func(context.Context, contractprojects.ProjectKey) (contractprojects.Snapshots, error) {
		return contractprojects.Snapshots{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// harness is a runs service on its own NATS server, with fake builds and projects
// services beside it.
type harness struct {
	t      *testing.T
	conn   *nats.Conn
	svc    *Service
	store  jetstream.ObjectStore
	builds *fakeBuilds
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	hub := natstest.Start(t)
	conn := hub.Conn()
	ctx := context.Background()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	store, err := natsx.DeclareObjectStore(ctx, js, natsx.ArtifactsBucket)
	if err != nil {
		t.Fatal(err)
	}
	builds := newFakeBuilds()
	builds.serve(t, conn)
	serveProjects(t, conn)
	svc := New(Config{})
	if err := svc.Start(ctx, conn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if svc.draining.Load() {
			return
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := svc.Stop(stopCtx); err != nil {
			t.Error(err)
		}
	})
	return &harness{t: t, conn: conn, svc: svc, store: store, builds: builds}
}

// completedWASI adds a complete WASI Build of owner whose Artifact runs module.
func (h *harness) completedWASI(owner, id string, module []byte) string {
	h.t.Helper()
	name, data := compressedArtifact(h.t, module)
	h.builds.addBuild(h.t, h.store, owner, id, contractbuilds.StatusComplete, name, data)
	return id
}

// request asks the runs service for a run and returns its ID, or the refusal.
func (h *harness) request(owner, buildID string, args []string) (string, error) {
	var accepted contractruns.RunAccepted
	err := natsx.Call(context.Background(), h.conn, contractruns.SubjectRequestRun,
		contractruns.RequestRun{OwnerID: owner, BuildID: buildID, Args: args}, &accepted)
	return accepted.RunID, err
}

// waitRuns waits for the Runs that are executing to finish.
func (h *harness) waitRuns() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.svc.wait(ctx); err != nil {
		h.t.Fatal(err)
	}
}

// get asks the runs service for one of owner's runs.
func (h *harness) get(owner, runID string) (contractruns.RunFound, error) {
	var found contractruns.RunFound
	err := natsx.Call(context.Background(), h.conn, contractruns.SubjectGetRun, contractruns.RunKey{OwnerID: owner, RunID: runID}, &found)
	return found, err
}

// refusedWith returns the status a refusal of the service was answered with, or 0
// when err is not a refusal.
func refusedWith(err error) int {
	var remote *natsx.RemoteError
	if errors.As(err, &remote) {
		return remote.Status
	}
	return 0
}

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

func skipWithoutWasmtime(t *testing.T) {
	t.Helper()
	if !wasmrun.Available() {
		t.Skip("Wasmtime needs a cgo build")
	}
}

func TestRunExecutesACompletedBuildAndKeepsItsOutput(t *testing.T) {
	skipWithoutWasmtime(t)
	h := newHarness(t)
	build := h.completedWASI(testOwner, "build-1", runnertest.Hello)

	id, err := h.request(testOwner, build, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	h.waitRuns()

	found, err := h.get(testOwner, id)
	if err != nil {
		t.Fatal(err)
	}
	run := found.Run
	if !found.Found || run.Status != contractruns.StatusComplete || run.Stdout != "hi\n" || run.BuildID != build || run.Module != "main.wasm" || run.Usage.Fuel == 0 {
		t.Fatalf("run = %+v, found %v", run, found.Found)
	}
	if other, _ := h.get("bob", id); other.Found {
		t.Fatal("another Account saw the run")
	}
	if unknown, _ := h.get(testOwner, "unknown"); unknown.Found {
		t.Fatal("an unknown run was found")
	}
}

func TestRunRecordsAFailedRun(t *testing.T) {
	skipWithoutWasmtime(t)
	h := newHarness(t)
	build := h.completedWASI(testOwner, "build-1", []byte("not a module"))

	id, err := h.request(testOwner, build, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.waitRuns()

	found, err := h.get(testOwner, id)
	if err != nil {
		t.Fatal(err)
	}
	if found.Run.Status != contractruns.StatusFailed || found.Run.Error == "" {
		t.Fatalf("run = %+v", found.Run)
	}
}

func TestRunRefusesWhatCannotRun(t *testing.T) {
	skipWithoutWasmtime(t)
	h := newHarness(t)
	build := h.completedWASI(testOwner, "build-1", runnertest.Hello)
	h.builds.addBuild(t, h.store, testOwner, "build-building", contractbuilds.StatusBuilding, "", nil)

	tests := []struct {
		name   string
		owner  string
		build  string
		args   []string
		status int
	}{
		{name: "another Account's build", owner: "bob", build: build, status: contract.StatusNotFound},
		{name: "unknown build", owner: testOwner, build: "missing", status: contract.StatusNotFound},
		{name: "build not complete", owner: testOwner, build: "build-building", status: contract.StatusInvalid},
		{name: "too many arguments", owner: testOwner, build: build, args: make([]string, 65), status: contract.StatusInvalid},
		{name: "long argument", owner: testOwner, build: build, args: []string{strings.Repeat("x", 4097)}, status: contract.StatusInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := h.request(test.owner, test.build, test.args)
			if got := refusedWith(err); got != test.status {
				t.Fatalf("status = %d, want %d (err %v)", got, test.status, err)
			}
		})
	}
}

func TestRunsKeepAtMostOneHundredRuns(t *testing.T) {
	s := New(Config{})
	first := s.newRun(testOwner, "build-1")
	for range maxRunTable - 1 {
		s.newRun(testOwner, "build-1")
	}
	if len(s.runs) != maxRunTable {
		t.Fatalf("kept %d runs, want %d", len(s.runs), maxRunTable)
	}
	s.newRun(testOwner, "build-1")
	if _, kept := s.runs[first]; kept {
		t.Fatal("the oldest run was not evicted")
	}
	if len(s.runs) != maxRunTable {
		t.Fatalf("kept %d runs, want %d", len(s.runs), maxRunTable)
	}
}

func TestRunsLimitConcurrentRunsToTwo(t *testing.T) {
	s := New(Config{})
	if !s.acquire() || !s.acquire() {
		t.Fatal("two runs were refused")
	}
	if s.acquire() {
		t.Fatal("a third run was accepted")
	}
	s.active.Add(-1)
	if !s.acquire() {
		t.Fatal("a freed slot was not reused")
	}
}

func TestStopRefusesNewRunsAndWaitsForActiveOnes(t *testing.T) {
	skipWithoutWasmtime(t)
	h := newHarness(t)
	build := h.completedWASI(testOwner, "build-1", runnertest.Hello)
	if _, err := h.request(testOwner, build, nil); err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.svc.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if !h.svc.draining.Load() {
		t.Fatal("the service still accepts runs after Stop")
	}
	if got := h.svc.active.Load(); got != 0 {
		t.Fatalf("%d runs still active after Stop", got)
	}
}
