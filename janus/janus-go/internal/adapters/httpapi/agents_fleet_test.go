package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"janus/internal/adapters/agentlinktest"
	"janus/internal/adapters/audittest"
	"janus/internal/adapters/localstatetest"
	"janus/internal/adapters/runnertest"
	"janus/internal/adapters/storetest"
	"janus/internal/adapters/tailnettest"
	"janus/internal/app"
	"janus/internal/domain/cgi"
)

// fleetApp is the HTTP adapter with the Agents and Fleet use cases over test
// adapters, and an Agent running in process behind a loopback link.
type fleetApp struct {
	*agentApp
	link  *agentlinktest.Loopback
	fleet *app.Fleet
}

func newFleetApp(t *testing.T) *fleetApp {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	store, audit, tailnet, link := storetest.NewMemory(), audittest.NewRecorder(), tailnettest.NewFake(), agentlinktest.NewLoopback()
	t.Cleanup(func() { link.Close() })
	authorization, err := app.NewPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fleet := app.NewFleet(store, tailnet, link, audit)
	config := Config{
		Audit: audit, Identity: app.NewIdentity(store), Projects: app.NewProjects(store), Accounts: app.NewAccounts(store),
		Authentication: app.NewAuthentication([]byte(strings.Repeat("k", 32)), "janus-test", 30*time.Minute), Authorization: authorization,
		Agents: app.NewAgents(store, tailnet, audit, "https://tailnet.example.test", time.Now), Fleet: fleet,
	}
	return &fleetApp{agentApp: &agentApp{handler: New(config).Handler(), tailnet: tailnet, audit: audit}, link: link, fleet: fleet}
}

var pageModule = []byte("\x00asm\x01\x00\x00\x00httpapi")

// runAgent starts the enrolled Agent in process where the Fleet will look for
// it, connects the Fleet, and waits until the Agent has reported its state.
func (f *fleetApp) runAgent(t *testing.T, owner *http.Cookie, enrolled enrolledBody) *app.AgentRuntime {
	t.Helper()
	runner := runnertest.NewFake()
	runner.Handle(pageModule, func(app.RunInput) (app.RunResult, error) {
		return app.RunResult{Stdout: []byte("Content-Type: text/plain\n\nok")}, nil
	})
	runtime := app.NewAgentRuntime(runner, localstatetest.NewMemory(), "", time.Now)
	nodes, err := f.tailnet.Nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if node.Name == "laptop" {
			f.link.Serve(net.JoinHostPort(node.Addresses[0], strconv.Itoa(app.AgentConnectPort)), runtime)
		}
	}
	if err = f.fleet.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
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
	f.link.Settle(agentID)
	if !done() {
		t.Fatalf("after the link settled, still no %s", what)
	}
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
	if err := f.fleet.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
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
	if err := runtime.Deploy(context.Background(), app.HostedDeployment{ID: "dep-1", SHA256: hex.EncodeToString(sum[:]), Module: pageModule}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Serve(context.Background(), "dep-1", app.ServeRequest{Request: cgi.Request{Method: "GET", Path: "/"}, Body: []byte("abc")}); err != nil {
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
