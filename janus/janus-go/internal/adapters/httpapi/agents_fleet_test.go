package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"janus/internal/domain/agent"
	"net/http"
	"strings"
	"testing"
	"time"

	"janus/internal/adapters/agentlinktest"
	"janus/internal/adapters/localstatetest"
	"janus/internal/adapters/runnertest"
	"janus/internal/domain/cgi"
	domainfleet "janus/internal/domain/fleet"
)

// fleetApp is the HTTP adapter with the fleet service running in process, and an
// Agent running in process behind a loopback link.
type fleetApp struct {
	*agentApp
	link  *agentlinktest.Loopback
	fleet Fleet
}

func newFleetApp(t *testing.T) *fleetApp {
	t.Helper()
	a := newAgentApp(t, true)
	return &fleetApp{agentApp: a, link: a.fixture.Link, fleet: a.fixture.Fleet}
}

var pageModule = []byte("\x00asm\x01\x00\x00\x00httpapi")

// runAgent starts the enrolled Agent in process where the Fleet will look for
// it, connects the Fleet, and waits until the Agent has reported its state.
func (f *fleetApp) runAgent(t *testing.T, owner *http.Cookie, enrolled enrolledBody) *agent.Runtime {
	t.Helper()
	runner := runnertest.NewFake()
	runner.Handle(pageModule, func(agent.RunInput) (agent.RunResult, error) {
		return agent.RunResult{Stdout: []byte("Content-Type: text/plain\n\nok")}, nil
	})
	runtime := agent.NewRuntime(runner, localstatetest.NewMemory(), "", time.Now)
	f.link.Serve(enrolled.AgentID, runtime)
	f.settled(t, enrolled.AgentID, "report from the Agent", func() bool {
		agents := decode[[]agentBody](t, perform(f.handler, http.MethodGet, "/api/agents", "", owner))
		return len(agents) == 1 && agents[0].Connected
	})
	return runtime
}

// settled waits until the link has delivered every event the Agent has
// produced, then checks done.
func (f *fleetApp) settled(t *testing.T, agentID, what string, done func() bool) {
	t.Helper()
	f.fixture.Eventually("the link to deliver "+what, func() bool {
		f.link.Settle(agentID)
		return done()
	})
}

func TestTheOwnerPausesAndResumesAnAgentFromTheWebUI(t *testing.T) {
	f := newFleetApp(t)
	alice, bob := f.register(t, "alice@example.com"), f.register(t, "bob@example.com")
	enrolled := f.enrollAgent(t, alice, "laptop")
	runtime := f.runAgent(t, alice, enrolled)
	path := "/api/agents/" + enrolled.AgentID

	if denied := perform(f.handler, http.MethodPost, path+"/pause", "", nil); denied.Code != http.StatusUnauthorized {
		t.Fatalf("pausing without a session = %d, want 401", denied.Code)
	}
	if foreign := perform(f.handler, http.MethodPost, path+"/pause", "", bob); foreign.Code != http.StatusNotFound {
		t.Fatalf("bob pausing alice's Agent = %d, want 404", foreign.Code)
	}
	if runtime.State().Paused {
		t.Fatal("bob paused alice's Agent")
	}
	if paused := perform(f.handler, http.MethodPost, path+"/pause", "", alice); paused.Code != http.StatusOK {
		t.Fatalf("pause = %d: %s", paused.Code, paused.Body.String())
	}
	if !runtime.State().Paused {
		t.Fatal("the Agent did not pause")
	}
	f.settled(t, enrolled.AgentID, "pause in the list", func() bool {
		return decode[[]agentBody](t, perform(f.handler, http.MethodGet, "/api/agents", "", alice))[0].Paused
	})
	if resumed := perform(f.handler, http.MethodPost, path+"/resume", "", alice); resumed.Code != http.StatusOK {
		t.Fatalf("resume = %d: %s", resumed.Code, resumed.Body.String())
	}
	if runtime.State().Paused {
		t.Fatal("the Agent did not resume")
	}
}

func TestPausingAnOfflineAgentSaysSo(t *testing.T) {
	f := newFleetApp(t)
	alice := f.register(t, "alice@example.com")
	enrolled := f.enrollAgent(t, alice, "laptop")
	response := perform(f.handler, http.MethodPost, "/api/agents/"+enrolled.AgentID+"/pause", "", alice)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "offline") {
		t.Fatalf("pausing an Agent that is not running = %d: %s", response.Code, response.Body.String())
	}
}

func TestAnOwnerReadsTheirAgentsRuns(t *testing.T) {
	f := newFleetApp(t)
	alice, bob := f.register(t, "alice@example.com"), f.register(t, "bob@example.com")
	enrolled := f.enrollAgent(t, alice, "laptop")
	runtime := f.runAgent(t, alice, enrolled)
	sum := sha256.Sum256(pageModule)
	if err := runtime.Deploy(context.Background(), domainfleet.HostedDeployment{ID: "dep-1", SHA256: hex.EncodeToString(sum[:]), Module: pageModule}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Serve(context.Background(), "dep-1", agent.ServeRequest{Request: cgi.Request{Method: "GET", Path: "/"}, Body: []byte("abc")}); err != nil {
		t.Fatal(err)
	}
	path := "/api/agents/" + enrolled.AgentID + "/runs"
	var runs []runView
	f.settled(t, enrolled.AgentID, "record of both runs", func() bool {
		runs = decode[[]runView](t, perform(f.handler, http.MethodGet, path, "", alice))
		return len(runs) == 2
	})
	// Runs that start in the same clock tick have no fixed order.
	request, probe := runs[0], runs[1]
	if request.Probe {
		request, probe = probe, request
	}
	wantOut := uint64(len("Content-Type: text/plain") + 2 + len("ok"))
	if request.Probe || request.NetworkBytesIn != 3 || request.NetworkBytesOut != wantOut || request.Status != "ok" || !probe.Probe {
		t.Fatalf("runs = %+v, want the request and the probe", runs)
	}
	if foreign := perform(f.handler, http.MethodGet, path, "", bob); foreign.Code != http.StatusNotFound {
		t.Fatalf("bob reading alice's runs = %d, want 404", foreign.Code)
	}
}

func TestTheAgentsPageNeedsASession(t *testing.T) {
	f := newFleetApp(t)
	if anonymous := perform(f.handler, http.MethodGet, "/agents", "", nil); anonymous.Code != http.StatusSeeOther {
		t.Fatalf("the page without a session = %d, want a redirect", anonymous.Code)
	}
	alice := f.register(t, "alice@example.com")
	page := perform(f.handler, http.MethodGet, "/agents", "", alice)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Your Agents") || !strings.Contains(page.Body.String(), "Pause") {
		t.Fatalf("the page = %d", page.Code)
	}
}
