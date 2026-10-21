package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"janus/internal/adapters/localstatetest"
	"janus/internal/adapters/runnertest"
	"janus/internal/app"
	"janus/internal/domain/cgi"
	"janus/internal/domain/usage"
)

// cgiModule is a module the fake runner answers as a CGI program: it echoes the
// request's method, path, query and body, and the folder it was given.
var cgiModule = []byte("\x00asm\x01\x00\x00\x00cgi")

// ctx is the context of calls whose cancellation the test does not exercise.
var ctx = context.Background()

func envValue(env []string, name string) string {
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok && key == name {
			return value
		}
	}
	return ""
}

func cgiRunner() *runnertest.Fake {
	runner := runnertest.NewFake()
	runner.Handle(cgiModule, func(input app.RunInput) (app.RunResult, error) {
		out := fmt.Sprintf("Content-Type: text/plain\r\nX-Folder: %s\r\n\r\n%s %s?%s body=%s",
			input.Dir, envValue(input.Env, "REQUEST_METHOD"), envValue(input.Env, "PATH_INFO"),
			envValue(input.Env, "QUERY_STRING"), input.Stdin)
		return app.RunResult{Stdout: []byte(out), Usage: usage.Usage{Fuel: 42, PeakMemoryBytes: 65536, DurationMS: 3}}, nil
	})
	return runner
}

func hosted(id string, module []byte) app.HostedDeployment {
	sum := sha256.Sum256(module)
	return app.HostedDeployment{ID: id, SHA256: hex.EncodeToString(sum[:]), Module: module,
		Limits: app.RunLimits{Fuel: 1_000_000, MemoryBytes: 16 << 20, Timeout: time.Second}}
}

func newRuntime(t *testing.T, runner app.Runner, state app.LocalState) *app.AgentRuntime {
	t.Helper()
	return app.NewAgentRuntime(runner, state, "/owner/folder", time.Now)
}

func get(path string) app.ServeRequest {
	return app.ServeRequest{Request: cgi.Request{Method: "GET", Path: path, Host: "abc.example", RemoteAddr: "203.0.113.1"}}
}

func nextReport(t *testing.T, runtime *app.AgentRuntime) app.RunReport {
	t.Helper()
	select {
	case report := <-runtime.Reports():
		return report
	case <-time.After(5 * time.Second):
		t.Fatal("no run report")
		return app.RunReport{}
	}
}

func TestAgentRuntimeDeploysAndServesThroughCGI(t *testing.T) {
	runtime := newRuntime(t, cgiRunner(), localstatetest.NewMemory())
	if err := runtime.Deploy(ctx, hosted("dep-1", cgiModule)); err != nil {
		t.Fatal(err)
	}
	probe := nextReport(t, runtime)
	if !probe.Probe || probe.DeploymentID != "dep-1" || probe.Status != usage.StatusOK {
		t.Fatalf("probe report = %+v", probe)
	}
	request := get("/hello")
	request.Method, request.RawQuery, request.Body = "POST", "x=1", []byte("data")
	response, err := runtime.Serve(ctx, "dep-1", request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != 200 || string(response.Body) != "POST /hello?x=1 body=data" || response.Header["X-Folder"][0] != "/owner/folder" {
		t.Fatalf("response = %d %v %q", response.Status, response.Header, response.Body)
	}
	report := nextReport(t, runtime)
	want := app.RunReport{DeploymentID: "dep-1", Usage: usage.Usage{Fuel: 42, PeakMemoryBytes: 65536, DurationMS: 3},
		NetworkBytesIn: 4, NetworkBytesOut: wireBytes(response), Status: usage.StatusOK}
	if report.RunID == "" || report.Probe || report.StartedAt.IsZero() {
		t.Fatalf("report = %+v", report)
	}
	report.RunID, report.StartedAt = "", time.Time{}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	if state := runtime.State(); state.Paused || len(state.Deployments) != 1 || state.Deployments[0].ID != "dep-1" {
		t.Fatalf("state = %+v", state)
	}
}

func TestAgentRuntimeRefusesAModuleThatDoesNotMatchItsHash(t *testing.T) {
	runtime := newRuntime(t, cgiRunner(), localstatetest.NewMemory())
	deployment := hosted("dep-1", cgiModule)
	deployment.SHA256 = strings.Repeat("0", 64)
	if err := runtime.Deploy(ctx, deployment); !errors.Is(err, app.ErrModuleHash) {
		t.Fatalf("err = %v", err)
	}
	if _, err := runtime.Serve(ctx, "dep-1", get("/")); !errors.Is(err, app.ErrDeploymentNotFound) {
		t.Fatalf("serve err = %v", err)
	}
}

func TestAgentRuntimeFailsADeployWhoseProbeTraps(t *testing.T) {
	state := localstatetest.NewMemory()
	runtime := newRuntime(t, runnertest.NewFake(), state)
	if err := runtime.Deploy(ctx, hosted("dep-1", runnertest.Spins)); !errors.Is(err, app.ErrOutOfFuel) {
		t.Fatalf("err = %v", err)
	}
	if report := nextReport(t, runtime); !report.Probe || report.Status != usage.StatusOutOfFuel {
		t.Fatalf("report = %+v", report)
	}
	kept, _ := state.Deployments(ctx)
	if len(kept) != 0 || len(runtime.State().Deployments) != 0 {
		t.Fatal("a failed Deployment was kept")
	}
}

func TestAgentRuntimeDoesNotJudgeTheProbesStatus(t *testing.T) {
	runner := runnertest.NewFake()
	module := []byte("\x00asm\x01\x00\x00\x00notfound")
	runner.Handle(module, func(app.RunInput) (app.RunResult, error) {
		return app.RunResult{Stdout: []byte("Status: 404 Not Found\n\nnothing here")}, nil
	})
	runtime := newRuntime(t, runner, localstatetest.NewMemory())
	if err := runtime.Deploy(ctx, hosted("dep-1", module)); err != nil {
		t.Fatal(err)
	}
	response, err := runtime.Serve(ctx, "dep-1", get("/"))
	if err != nil || response.Status != 404 {
		t.Fatalf("response %d, err %v", response.Status, err)
	}
}

func TestAgentRuntimePauseStopsServingAndResumeRestoresIt(t *testing.T) {
	state := localstatetest.NewMemory()
	runtime := newRuntime(t, cgiRunner(), state)
	if err := runtime.Deploy(ctx, hosted("dep-1", cgiModule)); err != nil {
		t.Fatal(err)
	}
	<-runtime.Changes()
	if err := runtime.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.Changes():
	default:
		t.Fatal("pausing did not signal a change")
	}
	if _, err := runtime.Serve(ctx, "dep-1", get("/")); !errors.Is(err, app.ErrAgentPaused) {
		t.Fatalf("serve while paused: %v", err)
	}
	if err := runtime.Deploy(ctx, hosted("dep-2", cgiModule)); !errors.Is(err, app.ErrAgentPaused) {
		t.Fatalf("deploy while paused: %v", err)
	}
	if paused, _ := state.Paused(ctx); !paused || !runtime.State().Paused {
		t.Fatal("the pause was not kept")
	}
	if err := runtime.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if response, err := runtime.Serve(ctx, "dep-1", get("/")); err != nil || response.Status != 200 {
		t.Fatalf("after resume: %d, %v", response.Status, err)
	}
}

func TestAgentRuntimeTakesAPauseWrittenByItsCommandLine(t *testing.T) {
	state := localstatetest.NewMemory()
	runtime := newRuntime(t, cgiRunner(), state)
	if err := runtime.Deploy(ctx, hosted("dep-1", cgiModule)); err != nil {
		t.Fatal(err)
	}
	// The command line opens the same state and pauses through its own runtime.
	cli := newRuntime(t, cgiRunner(), state.Reopen())
	if err := cli.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Serve(ctx, "dep-1", get("/")); !errors.Is(err, app.ErrAgentPaused) {
		t.Fatalf("serve after a command-line pause: %v", err)
	}
	if err := cli.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Serve(ctx, "dep-1", get("/")); err != nil {
		t.Fatalf("serve after a command-line resume: %v", err)
	}
}

func TestAgentRuntimeResumesItsDeploymentsAfterARestart(t *testing.T) {
	state := localstatetest.NewMemory()
	first := newRuntime(t, cgiRunner(), state)
	if err := first.Deploy(ctx, hosted("dep-1", cgiModule)); err != nil {
		t.Fatal(err)
	}
	if err := first.Pause(ctx); err != nil {
		t.Fatal(err)
	}

	restarted := newRuntime(t, cgiRunner(), state.Reopen())
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !restarted.State().Paused {
		t.Fatal("the pause did not survive the restart")
	}
	if err := restarted.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	response, err := restarted.Serve(ctx, "dep-1", get("/again"))
	if err != nil || string(response.Body) != "GET /again? body=" {
		t.Fatalf("after restart: %q, %v", response.Body, err)
	}
}

func TestAgentRuntimeSkipsAKeptModuleThatChanged(t *testing.T) {
	state := localstatetest.NewMemory()
	good, bad := hosted("good", cgiModule), hosted("bad", cgiModule)
	bad.Module = []byte("\x00asm\x01\x00\x00\x00tampered")
	for _, deployment := range []app.HostedDeployment{good, bad} {
		if err := state.SaveDeployment(ctx, deployment); err != nil {
			t.Fatal(err)
		}
	}
	runtime := newRuntime(t, cgiRunner(), state)
	err := runtime.Start(ctx)
	if !errors.Is(err, app.ErrModuleHash) || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("start err = %v", err)
	}
	if got := runtime.State().Deployments; len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("served %+v", got)
	}
}

func TestAgentRuntimeStopForgetsADeployment(t *testing.T) {
	state := localstatetest.NewMemory()
	runtime := newRuntime(t, cgiRunner(), state)
	if err := runtime.Deploy(ctx, hosted("dep-1", cgiModule)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Stop(ctx, "dep-1"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Stop(ctx, "dep-1"); err != nil {
		t.Fatalf("stopping twice: %v", err)
	}
	if _, err := runtime.Serve(ctx, "dep-1", get("/")); !errors.Is(err, app.ErrDeploymentNotFound) {
		t.Fatalf("serve err = %v", err)
	}
	if kept, _ := state.Deployments(ctx); len(kept) != 0 {
		t.Fatalf("still kept: %+v", kept)
	}
}

func TestAgentRuntimeAnswersALimitWithA5xxAndReportsIt(t *testing.T) {
	runner := cgiRunner()
	timeout, fuel, broken, huge, garbled := []byte("\x00asm\x01\x00\x00\x00timeout"), []byte("\x00asm\x01\x00\x00\x00fuel"),
		[]byte("\x00asm\x01\x00\x00\x00broken"), []byte("\x00asm\x01\x00\x00\x00huge"), []byte("\x00asm\x01\x00\x00\x00garbled")
	var probed sync.Map
	// Each module passes its probe, then fails the way its name says.
	failing := func(name string, fail func(app.RunInput) (app.RunResult, error)) runnertest.Program {
		return func(input app.RunInput) (app.RunResult, error) {
			if _, done := probed.LoadOrStore(name, true); !done {
				return app.RunResult{Stdout: []byte("Content-Type: text/plain\n\nok")}, nil
			}
			return fail(input)
		}
	}
	runner.Handle(timeout, failing("timeout", func(app.RunInput) (app.RunResult, error) {
		return app.RunResult{Usage: usage.Usage{DurationMS: 30_000}}, app.ErrRunTimeout
	}))
	runner.Handle(fuel, failing("fuel", func(app.RunInput) (app.RunResult, error) {
		return app.RunResult{Usage: usage.Usage{Fuel: 1_000_000}}, app.ErrOutOfFuel
	}))
	runner.Handle(broken, failing("broken", func(app.RunInput) (app.RunResult, error) {
		return app.RunResult{}, errors.New("program exited with status 2")
	}))
	runner.Handle(huge, failing("huge", func(input app.RunInput) (app.RunResult, error) {
		return app.RunResult{Stdout: bytes.Repeat([]byte("x"), input.MaxStdout+1)}, nil
	}))
	runner.Handle(garbled, failing("garbled", func(app.RunInput) (app.RunResult, error) {
		return app.RunResult{Stdout: []byte("no headers at all")}, nil
	}))
	runtime := newRuntime(t, runner, localstatetest.NewMemory())
	for _, c := range []struct {
		module []byte
		code   int
		status string
	}{
		{timeout, 504, usage.StatusTimeout},
		{fuel, 500, usage.StatusOutOfFuel},
		{broken, 500, usage.StatusFailed},
		{huge, 500, usage.StatusFailed},
		{garbled, 502, usage.StatusFailed},
	} {
		id := string(c.module[8:])
		if err := runtime.Deploy(ctx, hosted(id, c.module)); err != nil {
			t.Fatalf("%s: deploy: %v", id, err)
		}
		nextReport(t, runtime)
		response, err := runtime.Serve(ctx, id, get("/"))
		if err != nil || response.Status != c.code {
			t.Errorf("%s: response %d, err %v; want %d", id, response.Status, err, c.code)
		}
		if report := nextReport(t, runtime); report.Status != c.status || report.Error == "" {
			t.Errorf("%s: report %+v, want status %s with an error", id, report, c.status)
		}
	}
}

func TestAgentRuntimeRefusesABodyOver10MiB(t *testing.T) {
	runtime := newRuntime(t, cgiRunner(), localstatetest.NewMemory())
	if err := runtime.Deploy(ctx, hosted("dep-1", cgiModule)); err != nil {
		t.Fatal(err)
	}
	request := get("/")
	request.Body = make([]byte, app.MaxBodyBytes+1)
	if _, err := runtime.Serve(ctx, "dep-1", request); !errors.Is(err, app.ErrBodyTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentRuntimeServesAtMost16RequestsAtOnce(t *testing.T) {
	runner := cgiRunner()
	module := []byte("\x00asm\x01\x00\x00\x00slow")
	release, started := make(chan struct{}), make(chan struct{}, 32)
	var probed sync.Once
	runner.Handle(module, func(app.RunInput) (app.RunResult, error) {
		first := false
		probed.Do(func() { first = true })
		if !first {
			started <- struct{}{}
			<-release
		}
		return app.RunResult{Stdout: []byte("Content-Type: text/plain\n\nok")}, nil
	})
	runtime := newRuntime(t, runner, localstatetest.NewMemory())
	if err := runtime.Deploy(ctx, hosted("dep-1", module)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range app.MaxConcurrentRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := runtime.Serve(ctx, "dep-1", get("/")); err != nil {
				t.Error(err)
			}
		}()
	}
	for range app.MaxConcurrentRequests {
		<-started
	}
	if _, err := runtime.Serve(ctx, "dep-1", get("/")); !errors.Is(err, app.ErrAgentBusy) {
		t.Fatalf("17th request: %v", err)
	}
	close(release)
	wg.Wait()
	drainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := runtime.Drain(drainCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Serve(ctx, "dep-1", get("/")); !errors.Is(err, app.ErrAgentPaused) {
		t.Fatalf("serve after drain: %v", err)
	}
}

func TestAgentRuntimeKeepsTheNewestReports(t *testing.T) {
	runtime := newRuntime(t, cgiRunner(), localstatetest.NewMemory())
	if err := runtime.Deploy(ctx, hosted("dep-1", cgiModule)); err != nil {
		t.Fatal(err)
	}
	const runs = 1005
	for i := range runs {
		request := get("/")
		request.Body = []byte(fmt.Sprint(i))
		if _, err := runtime.Serve(ctx, "dep-1", request); err != nil {
			t.Fatal(err)
		}
	}
	// The probe and the first runs were dropped; the last report is the last run.
	var last app.RunReport
	count := 0
	for len(runtime.Reports()) > 0 {
		last = <-runtime.Reports()
		count++
	}
	if count != 1000 || last.NetworkBytesIn != uint64(len(fmt.Sprint(runs-1))) {
		t.Fatalf("got %d reports, last %+v", count, last)
	}
}

// wireBytes is the size of a response as Usage counts it: each header as
// "Name: value" and a line break, then the body.
func wireBytes(response app.ServeResponse) uint64 {
	n := len(response.Body)
	for name, values := range response.Header {
		for _, value := range values {
			n += len(name) + 2 + len(value) + 2
		}
	}
	return uint64(n)
}
