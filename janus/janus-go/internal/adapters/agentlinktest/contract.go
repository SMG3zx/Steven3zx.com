package agentlinktest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"janus/internal/adapters/localstatetest"
	"janus/internal/adapters/runnertest"
	"janus/internal/app"
	"janus/internal/domain/cgi"
)

// Subject is an adapter under test, with what the port cannot do itself: run
// an Agent where the link can reach it.
type Subject struct {
	Link app.AgentLink
	// Start serves runtime as an Agent and returns the address to Connect to.
	// When address is not empty the Agent must listen there, as a restarted
	// Agent does. stop takes the Agent away as a crash would.
	Start func(t *testing.T, runtime *app.AgentRuntime, address string) (listening string, stop func())
}

// Run runs the AgentLink contract against subjects made by newSubject.
func Run(t *testing.T, newSubject func(t *testing.T) Subject) {
	t.Helper()
	for _, c := range []struct {
		name string
		test func(t *testing.T, s Subject)
	}{
		{"TheFirstEventIsTheAgentsState", testFirstEventIsState},
		{"PauseAndResumeReachTheAgent", testPauseResume},
		{"DeployCarriesTheModuleAndUsageComesBack", testDeployAndUsage},
		{"ARefusalIsAnAgentError", testRefusal},
		{"AnAgentWithNoConnectionIsUnreachable", testUnreachable},
		{"AConnectionComesBackWhenTheAgentReturns", testReconnect},
		{"DisconnectStopsTheEvents", testDisconnect},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSubject(t)
			t.Cleanup(func() { s.Link.Close() })
			c.test(t, s)
		})
	}
}

// module is answered by the runtime's runner as a small CGI program.
var module = []byte("\x00asm\x01\x00\x00\x00agentlinktest")

func newRuntime(state app.LocalState) *app.AgentRuntime {
	runner := runnertest.NewFake()
	runner.Handle(module, func(app.RunInput) (app.RunResult, error) {
		return app.RunResult{Stdout: []byte("Content-Type: text/plain\n\nhello")}, nil
	})
	return app.NewAgentRuntime(runner, state, "", time.Now)
}

func deployment(id string) app.HostedDeployment {
	sum := sha256.Sum256(module)
	return app.HostedDeployment{ID: id, SHA256: hex.EncodeToString(sum[:]), Module: module,
		Limits: app.RunLimits{Fuel: 1_000_000, MemoryBytes: 16 << 20}}
}

// events collects what a link delivers for one Agent.
type events chan app.AgentEvent

func (e events) deliver(event app.AgentEvent) { e <- event }

// next waits for an event that matches want, skipping others.
func (e events) next(t *testing.T, what string, want func(app.AgentEvent) bool) app.AgentEvent {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case event := <-e:
			if want(event) {
				return event
			}
		case <-deadline:
			t.Fatalf("no event: %s", what)
			return app.AgentEvent{}
		}
	}
}

func isState(paused bool) func(app.AgentEvent) bool {
	return func(event app.AgentEvent) bool { return event.State != nil && event.State.Paused == paused }
}

// connected starts an Agent, connects the link to it and waits for the first
// State, so the connection is up.
func connected(t *testing.T, s Subject, runtime *app.AgentRuntime) (events, string, func()) {
	t.Helper()
	address, stop := s.Start(t, runtime, "")
	got := make(events, 64)
	s.Link.Connect("agent-1", address, got.deliver)
	got.next(t, "the first State", func(event app.AgentEvent) bool { return event.State != nil })
	return got, address, stop
}

func sendEventually(t *testing.T, s Subject, command app.AgentCommand) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := s.Link.Send(context.Background(), "agent-1", command)
		if err == nil {
			return
		}
		if !errors.Is(err, app.ErrAgentUnreachable) || time.Now().After(deadline) {
			t.Fatalf("send %+v: %v", command, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func testFirstEventIsState(t *testing.T, s Subject) {
	state := localstatetest.NewMemory()
	if err := state.SetPaused(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(state)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	address, _ := s.Start(t, runtime, "")
	got := make(events, 64)
	s.Link.Connect("agent-1", address, got.deliver)
	first := got.next(t, "the first event", func(app.AgentEvent) bool { return true })
	if first.State == nil || !first.State.Paused {
		t.Fatalf("first event = %+v, want the paused State", first)
	}
}

func testPauseResume(t *testing.T, s Subject) {
	runtime := newRuntime(localstatetest.NewMemory())
	got, _, _ := connected(t, s, runtime)
	sendEventually(t, s, app.AgentCommand{Pause: true})
	if !runtime.State().Paused {
		t.Fatal("the Agent is not paused")
	}
	got.next(t, "a paused State", isState(true))
	sendEventually(t, s, app.AgentCommand{Resume: true})
	if runtime.State().Paused {
		t.Fatal("the Agent is still paused")
	}
	got.next(t, "a resumed State", isState(false))
}

func testDeployAndUsage(t *testing.T, s Subject) {
	runtime := newRuntime(localstatetest.NewMemory())
	got, _, _ := connected(t, s, runtime)
	sendEventually(t, s, app.AgentCommand{Deploy: ptr(deployment("dep-1"))})
	// Usage and State are separate streams, so their order is not fixed.
	var sawState, sawProbe bool
	got.next(t, "a State with the Deployment and the probe's usage", func(event app.AgentEvent) bool {
		if event.State != nil && len(event.State.Deployments) == 1 && event.State.Deployments[0] == (app.DeploymentHash{ID: "dep-1", SHA256: deployment("dep-1").SHA256}) {
			sawState = true
		}
		if event.Usage != nil && event.Usage.Probe {
			sawProbe = true
		}
		return sawState && sawProbe
	})
	response, err := runtime.Serve(context.Background(), "dep-1", app.ServeRequest{Request: cgi.Request{Method: "GET", Path: "/"}, Body: []byte("abc")})
	if err != nil || string(response.Body) != "hello" {
		t.Fatalf("serve: %q, %v", response.Body, err)
	}
	usage := got.next(t, "the request's usage", func(event app.AgentEvent) bool { return event.Usage != nil && !event.Usage.Probe })
	if usage.Usage.DeploymentID != "dep-1" || usage.Usage.RunID == "" || usage.Usage.NetworkBytesIn != 3 || usage.Usage.NetworkBytesOut != 5 || usage.Usage.Status != "ok" {
		t.Fatalf("usage = %+v", usage.Usage)
	}
	sendEventually(t, s, app.AgentCommand{Stop: "dep-1"})
	if len(runtime.State().Deployments) != 0 {
		t.Fatal("the Deployment was not stopped")
	}
}

func testRefusal(t *testing.T, s Subject) {
	runtime := newRuntime(localstatetest.NewMemory())
	connected(t, s, runtime)
	bad := deployment("dep-1")
	bad.SHA256 = "0000"
	err := s.Link.Send(context.Background(), "agent-1", app.AgentCommand{Deploy: &bad})
	var refused *app.AgentError
	if !errors.As(err, &refused) || refused.Message != app.ErrModuleHash.Error() {
		t.Fatalf("err = %v, want an AgentError saying %q", err, app.ErrModuleHash)
	}
}

func testUnreachable(t *testing.T, s Subject) {
	if err := s.Link.Send(context.Background(), "never-connected", app.AgentCommand{Pause: true}); !errors.Is(err, app.ErrAgentUnreachable) {
		t.Fatalf("err = %v", err)
	}
}

func testReconnect(t *testing.T, s Subject) {
	state := localstatetest.NewMemory()
	got, address, stop := connected(t, s, newRuntime(state))
	stop()
	deadline := time.Now().Add(30 * time.Second)
	for !errors.Is(s.Link.Send(context.Background(), "agent-1", app.AgentCommand{Pause: true}), app.ErrAgentUnreachable) {
		if time.Now().After(deadline) {
			t.Fatal("a stopped Agent was still reachable")
		}
		time.Sleep(50 * time.Millisecond)
	}
	restarted := newRuntime(state.Reopen())
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Start(t, restarted, address)
	got.next(t, "the State of the restarted Agent", func(event app.AgentEvent) bool { return event.State != nil })
	sendEventually(t, s, app.AgentCommand{Pause: true})
	if !restarted.State().Paused {
		t.Fatal("the restarted Agent did not get the command")
	}
}

func testDisconnect(t *testing.T, s Subject) {
	runtime := newRuntime(localstatetest.NewMemory())
	got, _, _ := connected(t, s, runtime)
	s.Link.Disconnect("agent-1")
	if err := s.Link.Send(context.Background(), "agent-1", app.AgentCommand{Pause: true}); !errors.Is(err, app.ErrAgentUnreachable) {
		t.Fatalf("send after disconnect: %v", err)
	}
	if err := runtime.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-got:
		if event.State != nil && event.State.Paused {
			t.Fatalf("an event arrived after Disconnect: %+v", event)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func ptr[T any](v T) *T { return &v }
