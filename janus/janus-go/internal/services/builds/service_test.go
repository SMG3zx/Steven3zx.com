package builds

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/contract"
	contractaudit "janus/internal/contract/audit"
	contractbuilds "janus/internal/contract/builds"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
	"janus/internal/services/audit"
)

// pageBytes is how much content the fake projects service sends in one page. It
// is small, so a file is cut across pages in every test that reads source.
const pageBytes = 10

// The fake Snapshot every test builds: a Rust project with files that are cut
// across pages.
var sourceFiles = map[string]string{
	"Cargo.toml":  "[package]\nname = \"demo\"\n",
	"src/lib.rs":  "pub fn answer() -> u32 { 42 }\n",
	"src/main.rs": "fn main() {}\n",
}

const (
	testOwner    = "alice"
	testProject  = "p1"
	testSnapshot = "s1"
)

// harness is a builds service on its own NATS server, with a fake projects
// service that answers the subjects the builds service calls.
type harness struct {
	t    *testing.T
	hub  *natsx.Hub
	conn *nats.Conn
	svc  *Service
	path string

	// running is whether the service is started and not yet stopped.
	running bool

	mu      sync.Mutex
	compile CompileFunc
	check   CheckFunc
	// seen receives the workspace each compile was given, read before it runs.
	seen chan map[string]string
	// audits receives the audit events the service writes for builds.
	audits chan contractaudit.Event
	events chan *nats.Msg
}

func start(t *testing.T) *harness {
	t.Helper()
	hub := natstest.Start(t)
	h := &harness{t: t, hub: hub, conn: hub.Conn(), path: filepath.Join(t.TempDir(), "builds.db"),
		seen: make(chan map[string]string, 16), audits: make(chan contractaudit.Event, 200), events: make(chan *nats.Msg, 200)}
	h.compile = func(context.Context, string, CompileRequest) (CompileResult, error) {
		return CompileResult{Name: "wasm-artifacts.zip", Artifact: []byte("zip"), Log: []byte("compiled")}, nil
	}
	h.check = func(context.Context, string, CheckRequest) (CheckResult, error) {
		return CheckResult{Passed: true, Log: []byte("ok")}, nil
	}
	h.fakeProjects()
	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.DeclareStream(context.Background(), js); err != nil {
		t.Fatal(err)
	}
	if _, err := h.conn.Subscribe(contractaudit.Subject(contractbuilds.ServiceName), func(msg *nats.Msg) {
		var event contractaudit.Event
		if json.Unmarshal(msg.Data, &event) == nil {
			h.audits <- event
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.conn.Subscribe(contractbuilds.SubjectEvents, func(msg *nats.Msg) { h.events <- msg }); err != nil {
		t.Fatal(err)
	}
	h.startService()
	return h
}

// fakeProjects answers the projects subjects the builds service calls.
func (h *harness) fakeProjects() {
	t := h.t
	projects, err := micro.AddService(h.conn, micro.Config{Name: "projects-fake", Version: "0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = projects.Stop() })
	err = errors.Join(
		natsx.AddEndpoint(projects, "get", contractprojects.SubjectGetProject, func(_ context.Context, key contractprojects.ProjectKey) (contractprojects.ProjectFound, error) {
			if key.OwnerID != testOwner || key.ProjectID != testProject {
				return contractprojects.ProjectFound{}, nil
			}
			return contractprojects.ProjectFound{Found: true, Project: contractprojects.Project{ID: testProject, OwnerID: testOwner, SnapshotID: testSnapshot, Language: "Rust", Runtime: "wasi"}}, nil
		}),
		natsx.AddEndpoint(projects, "list", contractprojects.SubjectListSnapshots, func(_ context.Context, key contractprojects.ProjectKey) (contractprojects.Snapshots, error) {
			if key.OwnerID != testOwner || key.ProjectID != testProject {
				return contractprojects.Snapshots{}, contract.ErrNotFound
			}
			return contractprojects.Snapshots{Snapshots: []contractprojects.Snapshot{{ID: testSnapshot, Language: "Rust", Runtime: "wasi", Active: true}}}, nil
		}),
		natsx.AddEndpoint(projects, "files", contractprojects.SubjectSnapshotFiles, func(_ context.Context, request contractprojects.SnapshotFiles) (contractprojects.Files, error) {
			if request.OwnerID != testOwner || request.SnapshotID != testSnapshot {
				return contractprojects.Files{}, contract.ErrNotFound
			}
			return pageFiles(request), nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
}

// pageFiles answers a snapshot-files request the way the projects service does:
// files in path order, with at most pageBytes of content, cutting the last file
// when the page is full.
func pageFiles(request contractprojects.SnapshotFiles) contractprojects.Files {
	paths := make([]string, 0, len(sourceFiles))
	for path := range sourceFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var out []contractprojects.File
	budget := pageBytes
	index := request.Offset
	for index < len(paths) && budget > 0 {
		content := sourceFiles[paths[index]]
		start := 0
		if index == request.Offset {
			start = request.ContentOffset
		}
		rest := content[start:]
		n := min(len(rest), budget)
		out = append(out, contractprojects.File{Path: paths[index], Mode: "100644", Size: int64(len(content)), ContentOffset: start, Content: []byte(rest[:n])})
		budget -= n
		if n < len(rest) {
			return contractprojects.Files{Files: out, More: true}
		}
		index++
	}
	return contractprojects.Files{Files: out, More: index < len(paths)}
}

// startService starts a builds service on the harness's database. The compile
// and check functions are looked up on each call, so a test can change them.
func (h *harness) startService() {
	h.t.Helper()
	h.svc = New(Config{
		DatabasePath: h.path,
		Compile: func(ctx context.Context, language string, request CompileRequest) (CompileResult, error) {
			h.mu.Lock()
			compile := h.compile
			h.mu.Unlock()
			h.seen <- readWorkspace(h.t, request.Workspace)
			return compile(ctx, language, request)
		},
		Check: func(ctx context.Context, language string, request CheckRequest) (CheckResult, error) {
			h.mu.Lock()
			check := h.check
			h.mu.Unlock()
			return check(ctx, language, request)
		},
		AckWait: 3 * time.Second,
	})
	if err := h.svc.Start(context.Background(), h.conn); err != nil {
		h.t.Fatal(err)
	}
	svc := h.svc
	h.running = true
	h.t.Cleanup(func() {
		if h.running && h.svc == svc {
			_ = svc.Stop(context.Background())
		}
	})
}

// stopService stops the running service, as a shutdown does, and waits for it.
func (h *harness) stopService() {
	h.t.Helper()
	h.running = false
	if err := h.svc.Stop(context.Background()); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) setCompile(f CompileFunc) {
	h.mu.Lock()
	h.compile = f
	h.mu.Unlock()
}

func (h *harness) setCheck(f CheckFunc) {
	h.mu.Lock()
	h.check = f
	h.mu.Unlock()
}

func (h *harness) call(subject string, request, reply any) error {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := natsx.Call(ctx, h.conn, subject, request, reply)
	var remote *natsx.RemoteError
	if errors.As(err, &remote) {
		return contract.FromStatus(remote.Status, remote.Message)
	}
	return err
}

func (h *harness) must(subject string, request, reply any) {
	h.t.Helper()
	if err := h.call(subject, request, reply); err != nil {
		h.t.Fatalf("%s: %v", subject, err)
	}
}

// request queues a Build of the fake Project and returns its ID.
func (h *harness) request(id string) string {
	h.t.Helper()
	var found contractbuilds.BuildFound
	h.must(contractbuilds.SubjectRequestBuild, contractbuilds.RequestBuild{ID: id, OwnerID: testOwner, ProjectID: testProject, Target: "wasm32-wasip1"}, &found)
	return found.Build.ID
}

// waitFor returns the name of the first audit event about jobID that is one of
// names, and fails if none comes.
func (h *harness) waitFor(jobID string, names ...string) string {
	h.t.Helper()
	for {
		select {
		case event := <-h.audits:
			if event.Fields["job_id"] != jobID {
				continue
			}
			for _, name := range names {
				if event.Name == name {
					return name
				}
			}
		case <-time.After(30 * time.Second):
			h.t.Fatalf("build %s: no %v event", jobID, names)
		}
	}
}

// waitName returns once the service has written an audit event called name.
func (h *harness) waitName(name string) {
	h.t.Helper()
	for {
		select {
		case event := <-h.audits:
			if event.Name == name {
				return
			}
		case <-time.After(30 * time.Second):
			h.t.Fatalf("no %s event", name)
		}
	}
}

// waitEvent returns the next event with subject from the service.
func (h *harness) waitEvent(subject string) *nats.Msg {
	h.t.Helper()
	for {
		select {
		case msg := <-h.events:
			if msg.Subject == subject {
				return msg
			}
		case <-time.After(10 * time.Second):
			h.t.Fatalf("no %s event", subject)
		}
	}
}

func (h *harness) find(id string) contractbuilds.BuildFound {
	h.t.Helper()
	var found contractbuilds.BuildFound
	h.must(contractbuilds.SubjectFindBuild, contractbuilds.BuildKey{OwnerID: testOwner, BuildID: id}, &found)
	return found
}

// objectStore opens the artifacts store the way the service and the monolith do.
func (h *harness) objectStore() jetstream.ObjectStore {
	h.t.Helper()
	js, err := h.hub.JetStream()
	if err != nil {
		h.t.Fatal(err)
	}
	store, err := natsx.DeclareObjectStore(context.Background(), js, natsx.ArtifactsBucket)
	if err != nil {
		h.t.Fatal(err)
	}
	return store
}

// readWorkspace returns every file under root, by slash-separated path.
func readWorkspace(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestBuildCompletesWithArtifactAndCheck(t *testing.T) {
	h := start(t)
	id := h.request("b1")
	if got := h.waitFor(id, "project.wasm.build.completed"); got == "" {
		t.Fatal("build did not complete")
	}

	workspace := <-h.seen
	for path, want := range sourceFiles {
		if workspace[path] != want {
			t.Fatalf("workspace %s = %q, want %q", path, workspace[path], want)
		}
	}
	build := h.find(id)
	if !build.Found || build.Build.Status != contractbuilds.StatusComplete || build.Build.ArtifactName != "wasm-artifacts.zip" {
		t.Fatalf("find after completion = %+v", build)
	}
	if build.Build.Log != "compiled" {
		t.Fatalf("build log = %q", build.Build.Log)
	}

	var check contractbuilds.CheckFound
	h.must(contractbuilds.SubjectFindCheck, contractbuilds.BuildKey{OwnerID: testOwner, BuildID: id}, &check)
	if !check.Found || check.Check.Status != checkPassed || check.Check.Log != "ok" {
		t.Fatalf("check = %+v", check)
	}

	var artifact contractbuilds.ArtifactFound
	h.must(contractbuilds.SubjectFindArtifact, contractbuilds.BuildKey{OwnerID: testOwner, BuildID: id}, &artifact)
	if !artifact.Found || artifact.ObjectName != id {
		t.Fatalf("artifact = %+v", artifact)
	}
	data, err := h.objectStore().GetBytes(context.Background(), id)
	if err != nil || string(data) != "zip" {
		t.Fatalf("artifact object = %q, %v", data, err)
	}
}

func TestOtherOwnersCannotSeeABuild(t *testing.T) {
	h := start(t)
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.completed")
	var found contractbuilds.BuildFound
	h.must(contractbuilds.SubjectFindBuild, contractbuilds.BuildKey{OwnerID: "bob", BuildID: id}, &found)
	if found.Found {
		t.Fatalf("bob found alice's build: %+v", found)
	}
}

func TestBuildChangesArePublished(t *testing.T) {
	h := start(t)
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.completed")
	var statuses []string
	for len(statuses) < 3 {
		msg := h.waitEvent(contractbuilds.SubjectBuildChanged)
		var event contractbuilds.BuildChanged
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			t.Fatal(err)
		}
		if event.BuildID == id {
			statuses = append(statuses, event.Status)
		}
	}
	if strings.Join(statuses, ",") != "queued,building,complete" {
		t.Fatalf("statuses = %v", statuses)
	}
}

func TestCompileFailureKeepsTheLog(t *testing.T) {
	h := start(t)
	h.setCompile(func(context.Context, string, CompileRequest) (CompileResult, error) {
		return CompileResult{Log: []byte("error[E0308]")}, errors.New("exit status 101")
	})
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.failed")
	build := h.find(id).Build
	if build.Status != contractbuilds.StatusFailed || build.Error != "Rust WASM build failed: exit status 101" || build.Log != "error[E0308]" {
		t.Fatalf("build = %+v", build)
	}
}

func TestManifestErrorIsTheAccountsMessage(t *testing.T) {
	h := start(t)
	h.setCompile(func(context.Context, string, CompileRequest) (CompileResult, error) {
		return CompileResult{}, &ManifestError{Message: "Cargo.toml is missing", Err: errors.New("no file")}
	})
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.failed")
	if got := h.find(id).Build; got.Status != contractbuilds.StatusFailed || got.Error != "Cargo.toml is missing: no file" {
		t.Fatalf("build = %+v", got)
	}
}

func TestOneActiveBuildPerProject(t *testing.T) {
	h := start(t)
	release := make(chan struct{})
	h.setCompile(func(ctx context.Context, _ string, _ CompileRequest) (CompileResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return CompileResult{Name: "wasm-artifacts.zip", Artifact: []byte("zip")}, nil
	})
	first := h.request("b1")
	var second contractbuilds.BuildFound
	err := h.call(contractbuilds.SubjectRequestBuild, contractbuilds.RequestBuild{ID: "b2", OwnerID: testOwner, ProjectID: testProject, Target: "wasm32-wasip1"}, &second)
	if !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("second build error = %v, want a conflict", err)
	}
	close(release)
	h.waitFor(first, "project.wasm.build.completed")
}

func TestFailedBuildCanBeRetried(t *testing.T) {
	h := start(t)
	h.setCompile(func(context.Context, string, CompileRequest) (CompileResult, error) {
		return CompileResult{}, errors.New("boom")
	})
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.failed")

	h.setCompile(func(context.Context, string, CompileRequest) (CompileResult, error) {
		return CompileResult{Name: "wasm-artifacts.zip", Artifact: []byte("zip")}, nil
	})
	var retried contractbuilds.BuildFound
	h.must(contractbuilds.SubjectRetryBuild, contractbuilds.BuildKey{OwnerID: testOwner, BuildID: id}, &retried)
	h.waitFor(retried.Build.ID, "project.wasm.build.completed")
	if got := h.find(retried.Build.ID).Build; got.Status != contractbuilds.StatusComplete || got.SnapshotID != testSnapshot {
		t.Fatalf("retried build = %+v", got)
	}
}

func TestFailedCheckLeavesTheBuildComplete(t *testing.T) {
	h := start(t)
	h.setCheck(func(context.Context, string, CheckRequest) (CheckResult, error) {
		return CheckResult{Passed: false, NoTests: true, Log: []byte("FAILED")}, nil
	})
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.completed")
	var check contractbuilds.CheckFound
	h.must(contractbuilds.SubjectFindCheck, contractbuilds.BuildKey{OwnerID: testOwner, BuildID: id}, &check)
	if check.Check.Status != checkFailed || !check.Check.NoTests || check.Check.Log != "FAILED" {
		t.Fatalf("check = %+v", check.Check)
	}
	if got := h.find(id).Build; got.Status != contractbuilds.StatusComplete {
		t.Fatalf("build = %+v", got)
	}
}

// A Build whose worker stops part-way is handed out again and finishes.
func TestStoppedBuildIsRunAgain(t *testing.T) {
	h := start(t)
	running := make(chan struct{})
	h.setCompile(func(ctx context.Context, _ string, _ CompileRequest) (CompileResult, error) {
		close(running)
		<-ctx.Done()
		return CompileResult{}, ctx.Err()
	})
	id := h.request("b1")
	<-running
	h.stopService()

	h.setCompile(func(context.Context, string, CompileRequest) (CompileResult, error) {
		return CompileResult{Name: "wasm-artifacts.zip", Artifact: []byte("zip")}, nil
	})
	h.startService()
	h.waitFor(id, "project.wasm.build.completed")
	if got := h.find(id).Build; got.Status != contractbuilds.StatusComplete {
		t.Fatalf("build = %+v", got)
	}
}

func TestProjectDeletionRemovesItsBuildsAndArtifacts(t *testing.T) {
	h := start(t)
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.completed")

	js, err := h.hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(contractprojects.ProjectDeletedEvent{ProjectID: testProject, OwnerID: testOwner})
	if _, err := js.Publish(context.Background(), contractprojects.SubjectProjectDeleted, data); err != nil {
		t.Fatal(err)
	}
	h.waitName("project.wasm.builds.removed")
	if found := h.find(id); found.Found {
		t.Fatalf("build survived its project: %+v", found)
	}
	if _, err := h.objectStore().GetBytes(context.Background(), id); !errors.Is(err, jetstream.ErrObjectNotFound) {
		t.Fatalf("artifact object survived its project: %v", err)
	}
}

func TestOwnerDeletionRemovesBuilds(t *testing.T) {
	h := start(t)
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.completed")
	h.must(contractbuilds.SubjectDeleteOwner, contractbuilds.Owner{OwnerID: testOwner}, nil)
	if found := h.find(id); found.Found {
		t.Fatalf("build survived its owner: %+v", found)
	}
}

func TestMissingArtifactIsCopiedBackFromBuildsDB(t *testing.T) {
	h := start(t)
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.completed")
	store := h.objectStore()
	if err := store.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	var restored contractbuilds.Empty
	h.must(contractbuilds.SubjectRestoreArtifact, contractbuilds.BuildKey{OwnerID: testOwner, BuildID: id}, &restored)
	data, err := store.GetBytes(context.Background(), id)
	if err != nil || string(data) != "zip" {
		t.Fatalf("restored artifact = %q, %v", data, err)
	}
}

func TestUnknownProjectIsRefused(t *testing.T) {
	h := start(t)
	err := h.call(contractbuilds.SubjectRequestBuild, contractbuilds.RequestBuild{ID: "b1", OwnerID: testOwner, ProjectID: "missing", Target: "wasm32-wasip1"}, nil)
	if !errors.Is(err, contract.ErrInvalid) {
		t.Fatalf("error = %v, want invalid", err)
	}
}

// Deleting an Owner cancels the Build it is running, and the Build's job is
// finished rather than handed out again.
func TestOwnerDeletionCancelsARunningBuild(t *testing.T) {
	h := start(t)
	running := make(chan struct{})
	cancelled := make(chan struct{})
	h.setCompile(func(ctx context.Context, _ string, _ CompileRequest) (CompileResult, error) {
		close(running)
		<-ctx.Done()
		close(cancelled)
		return CompileResult{}, ctx.Err()
	})
	id := h.request("b1")
	<-running
	h.must(contractbuilds.SubjectDeleteOwner, contractbuilds.Owner{OwnerID: testOwner}, nil)
	select {
	case <-cancelled:
	case <-time.After(30 * time.Second):
		t.Fatal("the running build was not cancelled")
	}
	if found := h.find(id); found.Found {
		t.Fatalf("the build survived its owner: %+v", found)
	}
}

// A panic in the compiler fails the Build and leaves the service running.
func TestAPanickingCompileFailsTheBuild(t *testing.T) {
	h := start(t)
	h.setCompile(func(context.Context, string, CompileRequest) (CompileResult, error) {
		panic("compiler crashed")
	})
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.failed")
	if got := h.find(id).Build; got.Status != contractbuilds.StatusFailed || !strings.Contains(got.Error, "WASM build panicked: compiler crashed") {
		t.Fatalf("build = %+v", got)
	}
	var found contractbuilds.BuildFound
	h.must(contractbuilds.SubjectFindBuild, contractbuilds.BuildKey{OwnerID: testOwner, BuildID: id}, &found)
	if !found.Found {
		t.Fatal("the service stopped answering after the panic")
	}
}
