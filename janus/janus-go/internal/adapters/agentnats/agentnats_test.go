package agentnats

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/adapters/agentlinktest"
	"janus/internal/adapters/localstatetest"
	"janus/internal/app"
	contractfleet "janus/internal/contract/fleet"
	"janus/internal/platform/natstest"
)

// The tests run each Agent on its own embedded server, which joins the hub as a
// leaf over loopback. They need no Docker and no tailnet.

// agentID is the Agent the suite runs.
const agentID = "agent-1"

// ownerID is the Owner whose modules the Agent reads.
const ownerID = "owner-1"

// agentServer starts an embedded server for the Agent, joined to the hub as a
// leaf at leafAddress, and returns it. It is shut down when the test ends.
func agentServer(t *testing.T, leafAddress string) *server.Server {
	t.Helper()
	leafURL, err := url.Parse("nats-leaf://" + leafAddress)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.NewServer(&server.Options{
		ServerName:      "janus-agent-test",
		Host:            "127.0.0.1",
		Port:            -1,
		JetStream:       true,
		JetStreamDomain: contractfleet.AgentDomain(agentID),
		StoreDir:        t.TempDir(),
		NoLog:           true,
		NoSigs:          true,
		LeafNode:        server.LeafNodeOpts{Remotes: []*server.RemoteLeafOpts{{URLs: []*url.URL{leafURL}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("the Agent's server did not start")
	}
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv
}

// startAgent runs the Agent with runtime on its own server, joined to the hub at
// leafAddress. The returned function stops it, as a crash would.
func startAgent(t *testing.T, leafAddress string, runtime *app.AgentRuntime) func() {
	t.Helper()
	srv := agentServer(t, leafAddress)
	conn, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{Conn: conn, AgentID: agentID, OwnerID: ownerID, Runtime: runtime, Heartbeat: 100 * time.Millisecond})
	}()
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve the Agent: %v", err)
		}
		conn.Close()
		srv.Shutdown()
		srv.WaitForShutdown()
	}
}

// stagedRunner runs every module as a CGI program that answers ok.
type stagedRunner struct{}

func (stagedRunner) Run(context.Context, []byte, app.RunInput) (app.RunResult, error) {
	return app.RunResult{Stdout: []byte("Content-Type: text/plain\n\nok")}, nil
}

func TestTheAgentLinkPassesTheContractOverNATS(t *testing.T) {
	agentlinktest.Run(t, func(t *testing.T) agentlinktest.Subject {
		hub, leafAddress := natstest.StartWithLeaf(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		js, err := jetstream.New(hub.Conn())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: contractfleet.AgentStateBucket}); err != nil {
			t.Fatal(err)
		}
		link, err := NewLink(hub.Conn())
		if err != nil {
			t.Fatal(err)
		}
		return agentlinktest.Subject{Link: link, Start: func(t *testing.T, runtime *app.AgentRuntime) func() {
			return startAgent(t, leafAddress, runtime)
		}}
	})
}

func TestAModuleStagedOnTheHubIsReadByTheAgent(t *testing.T) {
	module := []byte("\x00asm\x01\x00\x00\x00staged")
	sum := sha256.Sum256(module)
	sha := hex.EncodeToString(sum[:])

	hub, leafAddress := natstest.StartWithLeaf(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	js, err := jetstream.New(hub.Conn())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: contractfleet.AgentStateBucket}); err != nil {
		t.Fatal(err)
	}
	modules, err := js.CreateObjectStore(ctx, jetstream.ObjectStoreConfig{Bucket: contractfleet.ModulesBucket(ownerID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modules.PutBytes(ctx, sha, module); err != nil {
		t.Fatal(err)
	}
	link, err := NewLink(hub.Conn())
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()

	runtime := app.NewAgentRuntime(stagedRunner{}, localstatetest.NewMemory(), "", time.Now)
	stop := startAgent(t, leafAddress, runtime)
	defer stop()
	link.Connect(agentID, func(app.AgentEvent) error { return nil })

	deadline := time.Now().Add(30 * time.Second)
	for {
		err := link.Send(ctx, agentID, contractfleet.AgentCommand{Deploy: &contractfleet.HostedDeployment{
			ID: "dep-1", SHA256: sha, Limits: app.RunLimits{Fuel: 1_000_000, MemoryBytes: 16 << 20}}})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deploy without the module inline: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := runtime.State().Deployments; len(got) != 1 || got[0].ID != "dep-1" || got[0].SHA256 != sha {
		t.Fatalf("the Agent's deployments = %+v, want dep-1 with the staged module", got)
	}
}

func TestAStagedModuleThatDoesNotMatchItsHashIsRefused(t *testing.T) {
	hub, leafAddress := natstest.StartWithLeaf(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	js, err := jetstream.New(hub.Conn())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: contractfleet.AgentStateBucket}); err != nil {
		t.Fatal(err)
	}
	modules, err := js.CreateObjectStore(ctx, jetstream.ObjectStoreConfig{Bucket: contractfleet.ModulesBucket(ownerID)})
	if err != nil {
		t.Fatal(err)
	}
	wrongSHA := hex.EncodeToString(make([]byte, sha256.Size))
	if _, err := modules.PutBytes(ctx, wrongSHA, []byte("not the module the hash names")); err != nil {
		t.Fatal(err)
	}
	link, err := NewLink(hub.Conn())
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	runtime := app.NewAgentRuntime(stagedRunner{}, localstatetest.NewMemory(), "", time.Now)
	stop := startAgent(t, leafAddress, runtime)
	defer stop()
	link.Connect(agentID, func(app.AgentEvent) error { return nil })

	var sendErr error
	deadline := time.Now().Add(30 * time.Second)
	for {
		sendErr = link.Send(ctx, agentID, contractfleet.AgentCommand{Deploy: &contractfleet.HostedDeployment{
			ID: "dep-1", SHA256: wrongSHA, Limits: app.RunLimits{Fuel: 1, MemoryBytes: 1}}})
		if sendErr == nil || time.Now().After(deadline) || !errors.Is(sendErr, app.ErrAgentUnreachable) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if sendErr == nil {
		t.Fatal("a module that does not match its hash was deployed")
	}
	if len(runtime.State().Deployments) != 0 {
		t.Fatal("the Agent kept a Deployment with a module that does not match its hash")
	}
}
