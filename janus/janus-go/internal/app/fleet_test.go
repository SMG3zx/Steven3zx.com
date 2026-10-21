package app_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"janus/internal/adapters/agentlinktest"
	"janus/internal/adapters/localstatetest"
	"janus/internal/app"
)

// fleetEnv is a control plane with one enrolled Agent of alice's, joined to the
// tailnet and running in process behind a loopback link.
type fleetEnv struct {
	*agentEnv
	link    *agentlinktest.Loopback
	fleet   *app.Fleet
	runtime *app.AgentRuntime
	agentID string
}

func newFleetEnv(t *testing.T) *fleetEnv {
	t.Helper()
	env := &fleetEnv{agentEnv: newAgentEnv(t, "alice", "bob"), link: agentlinktest.NewLoopback()}
	t.Cleanup(func() { env.link.Close() })
	// completedBuild puts alice's builds in this Project.
	_, err := env.store.CreateProject(context.Background(), app.CreateProject{ID: "alice-project", OwnerID: "alice", Name: "p"})
	must(t, err)
	enrollment := env.enroll(t, "alice", "laptop")
	env.agentID = enrollment.AgentID
	env.runtime = app.NewAgentRuntime(cgiRunner(), localstatetest.NewMemory(), "/home/alice/janus", time.Now)
	env.link.Serve(env.addressOf(t, enrollment.AgentID), env.runtime)
	env.fleet = app.NewFleet(env.store, env.tailnet, env.link, env.audit)
	must(t, env.fleet.Reconcile(context.Background()))
	return env
}

// addressOf is where the Fleet will look for the Agent: its node's address and
// the Agent's connect port.
func (e *fleetEnv) addressOf(t *testing.T, agentID string) string {
	t.Helper()
	record, found, err := e.store.FindAgent(context.Background(), agentID, "alice")
	must(t, err)
	if !found {
		t.Fatalf("no Agent %s", agentID)
	}
	nodes, err := e.tailnet.Nodes(context.Background())
	must(t, err)
	for _, node := range nodes {
		if node.KeyID == record.KeyID {
			return net.JoinHostPort(node.Addresses[0], strconv.Itoa(app.AgentConnectPort))
		}
	}
	t.Fatalf("Agent %s has no node", agentID)
	return ""
}

// eventually waits for an event the link delivers on its own goroutine.
func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// send retries while the link's connection is still coming up.
func send(t *testing.T, call func() error) error {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := call()
		if !errors.Is(err, app.ErrAgentUnreachable) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFleetPausesAndResumesAnAgent(t *testing.T) {
	env := newFleetEnv(t)
	ctx := context.Background()
	must(t, send(t, func() error { return env.fleet.Pause(ctx, "alice", env.agentID) }))
	if !env.runtime.State().Paused {
		t.Fatal("the Agent is not paused")
	}
	eventually(t, "the paused state to be reported", func() bool {
		status, err := env.fleet.Status(ctx, "alice")
		must(t, err)
		return status[env.agentID].Connected && status[env.agentID].Paused
	})
	must(t, env.fleet.Resume(ctx, "alice", env.agentID))
	if env.runtime.State().Paused {
		t.Fatal("the Agent is still paused")
	}
	for _, name := range []string{"agent.paused", "agent.resumed"} {
		if !audited(env.audit.Events(), name) {
			t.Errorf("no %s Audit Event", name)
		}
	}
}

func TestFleetOnlyCommandsTheOwnersAgents(t *testing.T) {
	env := newFleetEnv(t)
	ctx := context.Background()
	if err := env.fleet.Pause(ctx, "bob", env.agentID); !errors.Is(err, app.ErrAgentNotFound) {
		t.Fatalf("bob pausing alice's Agent = %v, want ErrAgentNotFound", err)
	}
	if env.runtime.State().Paused {
		t.Fatal("bob paused alice's Agent")
	}
	status, err := env.fleet.Status(ctx, "bob")
	must(t, err)
	if _, seen := status[env.agentID]; seen {
		t.Fatal("bob sees alice's Agent's status")
	}
}

func TestFleetDeploysABuildAndRecordsItsUsageUnderTheOwner(t *testing.T) {
	env := newFleetEnv(t)
	ctx := context.Background()
	build := completedBuild(t, env.store, "alice", "build-1", cgiModule)
	must(t, send(t, func() error { return env.fleet.Deploy(ctx, "alice", env.agentID, "dep-1", build) }))
	response, err := env.runtime.Serve(ctx, "dep-1", get("/hi"))
	must(t, err)
	if string(response.Body) != "GET /hi? body=" {
		t.Fatalf("served %q", response.Body)
	}
	var records []app.RunUsageRecord
	eventually(t, "both runs to be recorded", func() bool {
		records, err = env.fleet.Usage(ctx, app.RunUsageQuery{OwnerID: "alice", DeploymentID: "dep-1"})
		must(t, err)
		return len(records) == 2
	})
	for _, record := range records {
		if record.OwnerID != "alice" || record.AgentID != env.agentID || record.Usage.Fuel != 42 {
			t.Fatalf("record = %+v", record)
		}
	}
	if bobs, _ := env.fleet.Usage(ctx, app.RunUsageQuery{OwnerID: "bob"}); len(bobs) != 0 {
		t.Fatalf("bob sees runs: %+v", bobs)
	}
	if !audited(env.audit.Events(), "agent.deployment.started") {
		t.Fatal("no Audit Event for the Deployment")
	}
}

func TestFleetRefusesABuildThatCannotBeDeployed(t *testing.T) {
	env := newFleetEnv(t)
	ctx := context.Background()
	build := completedBuild(t, env.store, "alice", "build-1", cgiModule)
	if err := env.fleet.Deploy(ctx, "bob", env.agentID, "dep-1", build); !errors.Is(err, app.ErrAgentNotFound) && err == nil {
		t.Fatal("bob deployed alice's build")
	}
	if err := env.fleet.Deploy(ctx, "alice", env.agentID, "dep-1", "no-such-build"); err == nil {
		t.Fatal("deployed a build that does not exist")
	}
	if got := env.runtime.State().Deployments; len(got) != 0 {
		t.Fatalf("the Agent serves %+v", got)
	}
}

func TestFleetReportsAFailedProbeAsAnAgentError(t *testing.T) {
	env := newFleetEnv(t)
	ctx := context.Background()
	build := completedBuild(t, env.store, "alice", "build-1", []byte("\x00asm\x01\x00\x00\x00unknown to the runner"))
	err := send(t, func() error { return env.fleet.Deploy(ctx, "alice", env.agentID, "dep-1", build) })
	var refused *app.AgentError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want an AgentError", err)
	}
}

func TestFleetCannotReachAnAgentThatIsNotRunning(t *testing.T) {
	env := newFleetEnv(t)
	env.link.Unserve(env.addressOf(t, env.agentID))
	if err := env.fleet.Pause(context.Background(), "alice", env.agentID); !errors.Is(err, app.ErrAgentUnreachable) {
		t.Fatalf("err = %v, want ErrAgentUnreachable", err)
	}
}

func TestFleetDisconnectsARemovedAgent(t *testing.T) {
	env := newFleetEnv(t)
	ctx := context.Background()
	must(t, send(t, func() error { return env.link.Send(ctx, env.agentID, app.AgentCommand{Resume: true}) }))
	removed, err := env.agents.Remove(ctx, "alice", env.agentID)
	must(t, err)
	if !removed {
		t.Fatal("not removed")
	}
	must(t, env.fleet.Reconcile(ctx))
	if err := env.link.Send(ctx, env.agentID, app.AgentCommand{Pause: true}); !errors.Is(err, app.ErrAgentUnreachable) {
		t.Fatalf("a removed Agent is still connected: %v", err)
	}
}

func audited(events []app.AuditEvent, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}
