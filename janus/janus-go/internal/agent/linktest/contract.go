package linktest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"janus/internal/domain/agent"
	"sort"
	"sync"
	"testing"
	"time"

	"janus/internal/domain/cgi"
	domainfleet "janus/internal/domain/fleet"
)

// Link is the Agent link the contract tests: the control plane's end of a
// connection to an Agent, as the fleet service holds it.
type Link interface {
	Connect(agentID string, events func(domainfleet.AgentEvent) error)
	Disconnect(agentID string)
	Send(ctx context.Context, agentID string, command domainfleet.AgentCommand) error
	Close() error
}

// Subject is an adapter under test, with what the port cannot do itself: run
// the Agent "agent-1" where the link can reach it. Start serves runtime as that
// Agent and returns stop, which takes the Agent away as a crash would. A
// restarted Agent is served again by another Start.
type Subject struct {
	Link  Link
	Start func(t *testing.T, runtime *agent.Runtime) (stop func())
}

// Run runs the Agent link contract against subjects made by newSubject.
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

// module is what the suite deploys; helloRunner answers it as a CGI program.
var module = []byte("\x00asm\x01\x00\x00\x00agentlinktest")

// helloRunner is the suite's own agent.Runner: the suite tests the link, not
// running modules, so every run answers "hello".
type helloRunner struct{}

func (helloRunner) Run(context.Context, []byte, agent.RunInput) (agent.RunResult, error) {
	return agent.RunResult{Stdout: []byte("Content-Type: text/plain\n\nhello")}, nil
}

// keptState is the suite's own agent.LocalState, shared by the Agents it starts
// at one address, as a restarted Agent shares its directory.
type keptState struct {
	mu          sync.Mutex
	paused      bool
	deployments map[string]domainfleet.HostedDeployment
}

func newKeptState() *keptState {
	return &keptState{deployments: map[string]domainfleet.HostedDeployment{}}
}

func (k *keptState) SaveDeployment(_ context.Context, d domainfleet.HostedDeployment) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.deployments[d.ID] = d
	return nil
}

func (k *keptState) DeleteDeployment(_ context.Context, id string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.deployments, id)
	return nil
}

func (k *keptState) Deployments(context.Context) ([]domainfleet.HostedDeployment, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]domainfleet.HostedDeployment, 0, len(k.deployments))
	for _, d := range k.deployments {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (k *keptState) SetPaused(_ context.Context, paused bool) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.paused = paused
	return nil
}

func (k *keptState) Paused(context.Context) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.paused, nil
}

func newRuntime(state agent.LocalState) *agent.Runtime {
	return agent.NewRuntime(helloRunner{}, state, "", time.Now)
}

func deployment(id string) domainfleet.HostedDeployment {
	sum := sha256.Sum256(module)
	return domainfleet.HostedDeployment{ID: id, SHA256: hex.EncodeToString(sum[:]), Module: module,
		Limits: agent.RunLimits{Fuel: 1_000_000, MemoryBytes: 16 << 20}}
}

// events collects what a link delivers for one Agent.
type events chan domainfleet.AgentEvent

func (e events) deliver(event domainfleet.AgentEvent) error {
	e <- event
	return nil
}

// next waits for an event that matches want, skipping others.
func (e events) next(t *testing.T, what string, want func(domainfleet.AgentEvent) bool) domainfleet.AgentEvent {
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
			return domainfleet.AgentEvent{}
		}
	}
}

func isState(paused bool) func(domainfleet.AgentEvent) bool {
	return func(event domainfleet.AgentEvent) bool { return event.State != nil && event.State.Paused == paused }
}

// connected starts an Agent, connects the link to it and waits for the first
// State, so the connection is up.
func connected(t *testing.T, s Subject, runtime *agent.Runtime) (events, func()) {
	t.Helper()
	stop := s.Start(t, runtime)
	got := make(events, 64)
	s.Link.Connect("agent-1", got.deliver)
	got.next(t, "the first State", func(event domainfleet.AgentEvent) bool { return event.State != nil })
	return got, stop
}

// sendEventually retries while the connection comes up: over a network it
// opens on the link's own goroutine, after Connect has returned.
func sendEventually(t *testing.T, s Subject, command domainfleet.AgentCommand) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := s.Link.Send(context.Background(), "agent-1", command)
		if err == nil {
			return
		}
		if !errors.Is(err, domainfleet.ErrAgentUnreachable) || time.Now().After(deadline) {
			t.Fatalf("send %+v: %v", command, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func testFirstEventIsState(t *testing.T, s Subject) {
	state := newKeptState()
	if err := state.SetPaused(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	runtime := newRuntime(state)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Start(t, runtime)
	got := make(events, 64)
	s.Link.Connect("agent-1", got.deliver)
	first := got.next(t, "the first event", func(domainfleet.AgentEvent) bool { return true })
	if first.State == nil || !first.State.Paused {
		t.Fatalf("first event = %+v, want the paused State", first)
	}
}

func testPauseResume(t *testing.T, s Subject) {
	runtime := newRuntime(newKeptState())
	got, _ := connected(t, s, runtime)
	sendEventually(t, s, domainfleet.AgentCommand{Pause: true})
	if !runtime.State().Paused {
		t.Fatal("the Agent is not paused")
	}
	got.next(t, "a paused State", isState(true))
	sendEventually(t, s, domainfleet.AgentCommand{Resume: true})
	if runtime.State().Paused {
		t.Fatal("the Agent is still paused")
	}
	got.next(t, "a resumed State", isState(false))
}

func testDeployAndUsage(t *testing.T, s Subject) {
	runtime := newRuntime(newKeptState())
	got, _ := connected(t, s, runtime)
	sendEventually(t, s, domainfleet.AgentCommand{Deploy: ptr(deployment("dep-1"))})
	// Usage and State are separate streams, so their order is not fixed.
	var sawState, sawProbe bool
	got.next(t, "a State with the Deployment and the probe's usage", func(event domainfleet.AgentEvent) bool {
		if event.State != nil && len(event.State.Deployments) == 1 && event.State.Deployments[0] == (domainfleet.DeploymentHash{ID: "dep-1", SHA256: deployment("dep-1").SHA256}) {
			sawState = true
		}
		if event.Usage != nil && event.Usage.Probe {
			sawProbe = true
		}
		return sawState && sawProbe
	})
	response, err := runtime.Serve(context.Background(), "dep-1", agent.ServeRequest{Request: cgi.Request{Method: "GET", Path: "/"}, Body: []byte("abc")})
	if err != nil || string(response.Body) != "hello" {
		t.Fatalf("serve: %q, %v", response.Body, err)
	}
	usage := got.next(t, "the request's usage", func(event domainfleet.AgentEvent) bool { return event.Usage != nil && !event.Usage.Probe })
	// The response is the Content-Type header and its line break, then the body.
	wantOut := uint64(len("Content-Type: text/plain") + 2 + len("hello"))
	if usage.Usage.DeploymentID != "dep-1" || usage.Usage.RunID == "" || usage.Usage.NetworkBytesIn != 3 || usage.Usage.NetworkBytesOut != wantOut || usage.Usage.Status != "ok" {
		t.Fatalf("usage = %+v", usage.Usage)
	}
	sendEventually(t, s, domainfleet.AgentCommand{Stop: "dep-1"})
	if len(runtime.State().Deployments) != 0 {
		t.Fatal("the Deployment was not stopped")
	}
}

func testRefusal(t *testing.T, s Subject) {
	runtime := newRuntime(newKeptState())
	connected(t, s, runtime)
	bad := deployment("dep-1")
	bad.SHA256 = "0000"
	err := s.Link.Send(context.Background(), "agent-1", domainfleet.AgentCommand{Deploy: &bad})
	var refused *domainfleet.AgentError
	if !errors.As(err, &refused) || refused.Message != agent.ErrModuleHash.Error() {
		t.Fatalf("err = %v, want an AgentError saying %q", err, agent.ErrModuleHash)
	}
}

func testUnreachable(t *testing.T, s Subject) {
	if err := s.Link.Send(context.Background(), "never-connected", domainfleet.AgentCommand{Pause: true}); !errors.Is(err, domainfleet.ErrAgentUnreachable) {
		t.Fatalf("err = %v", err)
	}
}

func testReconnect(t *testing.T, s Subject) {
	state := newKeptState()
	got, stop := connected(t, s, newRuntime(state))
	stop()
	deadline := time.Now().Add(30 * time.Second)
	for !errors.Is(s.Link.Send(context.Background(), "agent-1", domainfleet.AgentCommand{Pause: true}), domainfleet.ErrAgentUnreachable) {
		if time.Now().After(deadline) {
			t.Fatal("a stopped Agent was still reachable")
		}
		time.Sleep(50 * time.Millisecond)
	}
	restarted := newRuntime(state)
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Start(t, restarted)
	got.next(t, "the State of the restarted Agent", func(event domainfleet.AgentEvent) bool { return event.State != nil })
	sendEventually(t, s, domainfleet.AgentCommand{Pause: true})
	if !restarted.State().Paused {
		t.Fatal("the restarted Agent did not get the command")
	}
}

func testDisconnect(t *testing.T, s Subject) {
	runtime := newRuntime(newKeptState())
	got, _ := connected(t, s, runtime)
	s.Link.Disconnect("agent-1")
	if err := s.Link.Send(context.Background(), "agent-1", domainfleet.AgentCommand{Pause: true}); !errors.Is(err, domainfleet.ErrAgentUnreachable) {
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
