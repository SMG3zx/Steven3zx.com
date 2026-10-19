package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"

	"janus/actors/builder"
	wasmactor "janus/actors/wasm"
)

// blockingBuilder holds every build until the test releases it, so the test
// decides when a build is running. It records the Language of each build.
type blockingBuilder struct {
	started   chan string
	release   chan struct{}
	running   atomic.Int32
	cancelled atomic.Int32
}

func newBlockingBuilder() *blockingBuilder {
	return &blockingBuilder{started: make(chan string, 8), release: make(chan struct{}, 8)}
}

func (b *blockingBuilder) Build(ctx context.Context, language string, _ builder.Request) (builder.Result, error) {
	b.running.Add(1)
	defer b.running.Add(-1)
	b.started <- language
	select {
	case <-b.release:
		return builder.Result{Name: "wasm-artifacts.zip", Artifact: []byte("archive"), Log: []byte("built " + language)}, nil
	case <-ctx.Done():
		b.cancelled.Add(1)
		return builder.Result{Log: []byte("stopped")}, ctx.Err()
	}
}

// concurrencyFixture is a running application with three projects: two Rust and
// one Go, each with a completed Snapshot.
type concurrencyFixture struct {
	t        *testing.T
	server   *httptest.Server
	cookie   string
	builds   *actor.PID
	rustA    string
	rustB    string
	goLang   string
	blocking *blockingBuilder
}

func newConcurrencyFixture(t *testing.T) *concurrencyFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	blocking := newBlockingBuilder()
	persistencePID, buildsPID, app := startJanusWith(t, ctx, blocking)
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	userID, cookie := registerUser(t, server.URL)
	f := &concurrencyFixture{t: t, server: server, cookie: cookie, builds: buildsPID, blocking: blocking}
	seed := func(name, language, manifest, content string) string {
		status, body := f.request(http.MethodPost, "/api/projects", `{"name":"`+name+`"}`)
		if status != http.StatusCreated {
			t.Fatalf("create project %s: status %d: %s", name, status, body)
		}
		var project struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(body), &project); err != nil || project.ID == "" {
			t.Fatalf("create project %s: %q: %v", name, body, err)
		}
		if err := seedLanguageSnapshot(ctx, persistencePID, userID, project.ID, "import-"+name, "snapshot-"+name, importCommit, `{}`, language, manifest, content); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		return project.ID
	}
	f.rustA = seed("rust-a", "Rust", "Cargo.toml", "[package]\nname='a'\n")
	f.rustB = seed("rust-b", "Rust", "Cargo.toml", "[package]\nname='b'\n")
	f.goLang = seed("go-c", "Go", "go.mod", "module c\n")
	return f
}

func (f *concurrencyFixture) request(method, path, body string) (int, string) {
	f.t.Helper()
	request, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "janus_session", Value: f.cookie})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(data)
}

// build requests a build and returns the status and, when accepted, its job ID.
func (f *concurrencyFixture) build(projectID, target string) (int, string, string) {
	f.t.Helper()
	status, body := f.request(http.MethodPost, "/api/projects/"+projectID+"/wasm-builds", `{"target":"`+target+`"}`)
	var job struct {
		JobID string `json:"job_id"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &job)
	return status, job.JobID, job.Error
}

func (f *concurrencyFixture) status(jobID string) string {
	f.t.Helper()
	_, body := f.request(http.MethodGet, "/api/wasm-builds/"+jobID, "")
	var job struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal([]byte(body), &job)
	return job.Status
}

func (f *concurrencyFixture) waitStatus(jobID, want string) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f.status(jobID) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("build %s is %q, want %q", jobID, f.status(jobID), want)
}

func (f *concurrencyFixture) startedLanguage() string {
	f.t.Helper()
	select {
	case language := <-f.blocking.started:
		return language
	case <-time.After(10 * time.Second):
		f.t.Fatal("a build never started")
		return ""
	}
}

func TestTwoBuildsOfDifferentLanguagesRunAtTheSameTime(t *testing.T) {
	f := newConcurrencyFixture(t)

	status, rustJob, message := f.build(f.rustA, "wasm32-unknown-unknown")
	if status != http.StatusAccepted {
		t.Fatalf("Rust build request = %d %s", status, message)
	}
	status, goJob, message := f.build(f.goLang, "wasip1-wasm")
	if status != http.StatusAccepted {
		t.Fatalf("Go build request = %d %s", status, message)
	}

	// Both builders are inside Build at once, and neither has finished.
	languages := map[string]bool{strings.ToLower(f.startedLanguage()): true, strings.ToLower(f.startedLanguage()): true}
	if !languages["rust"] || !languages["go"] {
		t.Fatalf("builds that started = %v, want a Rust and a Go build", languages)
	}
	if got := f.blocking.running.Load(); got != 2 {
		t.Fatalf("%d builds running, want 2", got)
	}
	f.waitStatus(rustJob, "building")
	f.waitStatus(goJob, "building")

	// A third request while two run is refused with the existing message.
	status, _, message = f.build(f.rustB, "wasm32-unknown-unknown")
	if status != http.StatusConflict || !strings.Contains(message, "two WASM builds are already running") {
		t.Fatalf("third build request = %d %q, want 409 and the busy message", status, message)
	}

	// While both are blocked, the rest of the site still answers quickly.
	for _, probe := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/me", ""},
		{http.MethodGet, "/projects/" + f.rustB, ""},
		{http.MethodPost, "/auth/login", `{"email":"playwright@example.com","password":"correct horse battery staple"}`},
	} {
		started := time.Now()
		status, _ := f.request(probe.method, probe.path, probe.body)
		if elapsed := time.Since(started); status >= 500 || elapsed > 2*time.Second {
			t.Errorf("%s %s while builds run = %d in %s", probe.method, probe.path, status, elapsed)
		}
	}

	// Releasing one frees its slot, and the refused build can now start.
	f.blocking.release <- struct{}{}
	deadline := time.Now().Add(10 * time.Second)
	var thirdJob string
	for time.Now().Before(deadline) {
		var code int
		code, thirdJob, _ = f.build(f.rustB, "wasm32-unknown-unknown")
		if code == http.StatusAccepted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if thirdJob == "" {
		t.Fatal("a build was still refused after a slot freed")
	}
	f.startedLanguage()
	f.blocking.release <- struct{}{}
	f.blocking.release <- struct{}{}
	for _, job := range []string{rustJob, goJob, thirdJob} {
		f.waitStatus(job, "complete")
	}
}

func TestShutdownRefusesNewBuildsAndWaitsForRunningOnes(t *testing.T) {
	f := newConcurrencyFixture(t)
	status, job, message := f.build(f.rustA, "wasm32-unknown-unknown")
	if status != http.StatusAccepted {
		t.Fatalf("build request = %d %s", status, message)
	}
	f.startedLanguage()

	drained := make(chan error, 1)
	go func() { drained <- wasmactor.WaitDrained(context.Background(), f.builds) }()
	select {
	case err := <-drained:
		t.Fatalf("shutdown finished while a build was running: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	if status, _, message = f.build(f.rustB, "wasm32-unknown-unknown"); status == http.StatusAccepted || !strings.Contains(message, "shutting down") {
		t.Fatalf("build during shutdown = %d %q, want a refusal that says it is shutting down", status, message)
	}

	f.blocking.release <- struct{}{}
	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not finish after the build was released")
	}
	f.waitStatus(job, "complete")
}

func TestCancellingAnAccountsBuildsStopsTheRunningBuild(t *testing.T) {
	f := newConcurrencyFixture(t)
	status, _, message := f.build(f.rustA, "wasm32-unknown-unknown")
	if status != http.StatusAccepted {
		t.Fatalf("build request = %d %s", status, message)
	}
	f.startedLanguage()
	owner := ownerOf(t, f)

	response, err := actor.Ask(context.Background(), f.builds, &wasmactor.CancelBuilds{OwnerID: owner}, 5*time.Second)
	if err != nil || response.(*wasmactor.BuildsCancelled).Count != 1 {
		t.Fatalf("CancelBuilds = %#v, %v; want one running build signalled", response, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for f.blocking.cancelled.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if f.blocking.cancelled.Load() != 1 {
		t.Fatal("the running build was never told to stop")
	}
}

// ownerOf returns the ID of the signed-in Account.
func ownerOf(t *testing.T, f *concurrencyFixture) string {
	t.Helper()
	status, body := f.request(http.MethodGet, "/api/me", "")
	var me struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &me); status != http.StatusOK || err != nil || me.ID == "" {
		t.Fatal(errors.New(fmt.Sprintf("GET /api/me = %d %q: %v", status, body, err)))
	}
	return me.ID
}

// Sign-ins hash passwords, which takes real CPU time. They are plain calls on
// each request's goroutine, so a burst of them all succeed instead of
// queueing behind one handler.
func TestManySignInsAtOnceAllSucceed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, _, app := startJanusWith(t, ctx, newBlockingBuilder())
	server := httptest.NewServer(app)
	defer server.Close()
	registerUser(t, server.URL)

	const signIns = 16
	var failures atomic.Int32
	var wg sync.WaitGroup
	started := time.Now()
	for range signIns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := http.Post(server.URL+"/auth/login", "application/json", strings.NewReader(`{"email":"playwright@example.com","password":"correct horse battery staple"}`))
			if err != nil {
				failures.Add(1)
				return
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failed := failures.Load(); failed != 0 {
		t.Fatalf("%d of %d concurrent sign-ins failed", failed, signIns)
	}
	t.Logf("%d concurrent sign-ins took %s", signIns, time.Since(started))
}
