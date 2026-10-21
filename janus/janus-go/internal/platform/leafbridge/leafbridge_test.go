package leafbridge

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Topology, all on loopback:
//
//	hub NATS (domain hub, leaf port L)
//	   ^ dial L
//	hub bridge (listens H)            <- stands in for the hub's tsnet listener
//	   ^ dial H
//	agent bridge (listens A)          <- stands in for the Agent's tsnet dial
//	   ^ dial A
//	agent NATS (domain agent-1, leaf remote -> A)
func TestLeafBridgePrototype(t *testing.T) {
	hubLeafPort := freePort(t)
	hub := startServer(t, &server.Options{
		ServerName:      "janus-hub",
		Host:            "127.0.0.1",
		Port:            -1,
		JetStream:       true,
		JetStreamDomain: "hub",
		StoreDir:        t.TempDir(),
		LeafNode:        server.LeafNodeOpts{Host: "127.0.0.1", Port: hubLeafPort},
	})

	hubBridgeLn := listen(t)
	hubBridge := NewBridge(hubBridgeLn, func() (net.Conn, error) {
		return net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", hubLeafPort))
	})
	t.Cleanup(hubBridge.Close)

	agentBridgeLn := listen(t)
	agentBridge := NewBridge(agentBridgeLn, func() (net.Conn, error) {
		return net.Dial("tcp", hubBridgeLn.Addr().String())
	})
	t.Cleanup(agentBridge.Close)

	leafURL, err := url.Parse("nats-leaf://" + agentBridgeLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	agent := startServer(t, &server.Options{
		ServerName:      "janus-agent-1",
		Host:            "127.0.0.1",
		Port:            -1,
		JetStream:       true,
		JetStreamDomain: "agent-1",
		StoreDir:        t.TempDir(),
		LeafNode: server.LeafNodeOpts{Remotes: []*server.RemoteLeafOpts{{
			URLs: []*url.URL{leafURL},
		}}},
	})

	hubConn := connect(t, hub.ClientURL())
	agentConn := connect(t, agent.ClientURL())

	waitFor(t, "leaf connected", func() bool {
		return hub.NumLeafNodes() == 1 && agent.NumLeafNodes() == 1
	})

	// The Agent answers commands on its own subject; the hub's request crosses the leaf.
	if _, err := agentConn.Subscribe("janus.agent.1.command", func(m *nats.Msg) {
		m.Respond([]byte("ok"))
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "command reply from the Agent", func() bool {
		reply, err := hubConn.Request("janus.agent.1.command", []byte("pause"), 500*time.Millisecond)
		return err == nil && string(reply.Data) == "ok"
	})

	// The Agent's Usage goes to its local USAGE stream.
	agentJS, err := jetstream.New(agentConn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := agentJS.CreateStream(ctx, jetstream.StreamConfig{
		Name:     "USAGE",
		Subjects: []string{"janus.usage.1.>"},
	}); err != nil {
		t.Fatal(err)
	}
	publishUsage(t, ctx, agentJS, 1, 2)

	// fleet reads the Agent's stream through the Agent's domain from the hub.
	fleetJS, err := jetstream.NewWithDomain(hubConn, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := fleetJS.Stream(ctx, "USAGE")
	if err != nil {
		t.Fatalf("hub reads USAGE through agent-1: %v", err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:   "fleet-agent-1",
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := drain(t, consumer, 2); len(got) != 2 {
		t.Fatalf("online: got %d reports, want 2", len(got))
	}

	// Cut the link. The Agent keeps recording while the hub cannot reach it.
	hubBridge.SetUp(false)
	agentBridge.SetUp(false)
	waitFor(t, "leaf gone", func() bool {
		return hub.NumLeafNodes() == 0
	})
	if _, err := hubConn.Request("janus.agent.1.command", []byte("pause"), 300*time.Millisecond); err == nil {
		t.Fatal("command reached the Agent while the link was down")
	}
	publishUsage(t, ctx, agentJS, 3, 5)

	// Restore the link. The leaf reconnects on its own and the backlog arrives.
	hubBridge.SetUp(true)
	agentBridge.SetUp(true)
	waitFor(t, "leaf reconnected", func() bool {
		return hub.NumLeafNodes() == 1 && agent.NumLeafNodes() == 1
	})
	got := drain(t, consumer, 3)
	if len(got) != 3 {
		t.Fatalf("after reconnect: got %d reports, want 3 (the backlog)", len(got))
	}
	for i, m := range got {
		if want := fmt.Sprintf("report-%d", i+3); string(m.Data()) != want {
			t.Fatalf("backlog message %d = %q, want %q", i, m.Data(), want)
		}
	}
}

func publishUsage(t *testing.T, ctx context.Context, js jetstream.JetStream, from, to int) {
	t.Helper()
	for i := from; i <= to; i++ {
		if _, err := js.Publish(ctx, "janus.usage.1.run", fmt.Appendf(nil, "report-%d", i)); err != nil {
			t.Fatalf("publish usage %d: %v", i, err)
		}
	}
}

// drain fetches up to n messages and acks each one after it is read.
func drain(t *testing.T, c jetstream.Consumer, n int) []jetstream.Msg {
	t.Helper()
	batch, err := c.Fetch(n, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	var out []jetstream.Msg
	for m := range batch.Messages() {
		if err := m.Ack(); err != nil {
			t.Fatalf("ack: %v", err)
		}
		out = append(out, m)
	}
	if err := batch.Error(); err != nil {
		t.Fatalf("fetch batch: %v", err)
	}
	return out
}

func startServer(t *testing.T, opts *server.Options) *server.Server {
	t.Helper()
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv
}

func connect(t *testing.T, u string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(u)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func freePort(t *testing.T) int {
	t.Helper()
	ln := listen(t)
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// waitFor polls cond until it holds. The prototype is a probe, so it polls;
// the real service tests wait on replies and events instead.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
