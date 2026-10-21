package natsauth

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// startAgent starts an Agent's embedded server as a leaf of the fixture's hub.
// With creds nil the leaf joins without credentials, which the hub must refuse.
func (f *fixture) startAgent(t *testing.T, agentID string, creds *Credentials) *server.Server {
	t.Helper()
	leafURL, err := url.Parse(fmt.Sprintf("nats-leaf://127.0.0.1:%d", f.leafPort))
	if err != nil {
		t.Fatal(err)
	}
	remote := &server.RemoteLeafOpts{URLs: []*url.URL{leafURL}}
	opts := &server.Options{
		ServerName:      "janus-agent-" + agentID,
		Host:            "127.0.0.1",
		Port:            -1,
		JetStream:       true,
		JetStreamDomain: "agent-" + agentID,
		StoreDir:        t.TempDir(),
		NoLog:           true,
		NoSigs:          true,
	}
	if creds != nil {
		body, err := creds.CredsFile()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "leaf.creds")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		remote.Credentials = path
		// The Agent's server knows AGENTS, so the leaf's traffic lands in
		// AGENTS on this side as it does on the hub.
		remote.LocalAccount = f.agents.Pub
		opts.TrustedOperators = []*jwt.OperatorClaims{f.op.Claims}
		opts.AccountResolver = f.resolver
		opts.SystemAccount = f.sys.Pub
	}
	opts.LeafNode = server.LeafNodeOpts{Remotes: []*server.RemoteLeafOpts{remote}}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("agent not ready")
	}
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	return srv
}

// freePort returns a loopback port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// connectLocal connects a client to the Agent's own server, as the Agent's
// process does. It holds an AGENTS user, since the leaf carries AGENTS there.
func (f *fixture) connectLocal(t *testing.T, agent *server.Server) *nats.Conn {
	t.Helper()
	local, err := f.agents.IssueUser("agent-local", []string{">"}, []string{">"}, true)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(agent.ClientURL(), local.Options()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// waitForLeaves waits until the hub has n leaf connections.
func waitForLeaves(t *testing.T, f *fixture, n int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for f.hub.NumLeafNodes() != n {
		if time.Now().After(deadline) {
			t.Fatalf("hub has %d leaf connections, want %d", f.hub.NumLeafNodes(), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAgentLeafJoinsWithItsOwnCredentials(t *testing.T) {
	f := newFixture(t)
	leaf, err := f.agents.IssueLeafUser("1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	agent := f.startAgent(t, "1", &leaf)
	waitForLeaves(t, f, 1)

	local := f.connectLocal(t, agent)
	if _, err := local.Subscribe("janus.agent.1.command", func(m *nats.Msg) { m.Respond([]byte("paused")) }); err != nil {
		t.Fatal(err)
	}

	control, err := f.janus.IssueUser("control-plane",
		[]string{"janus.agent.>", "_INBOX.>"}, []string{"_INBOX.>"}, false)
	if err != nil {
		t.Fatal(err)
	}
	ncJ, _ := f.connect(t, control)
	waitForReply(t, ncJ, "janus.agent.1.command", "paused")
}

func TestLeafWithoutCredentialsIsRefused(t *testing.T) {
	f := newFixture(t)
	f.startAgent(t, "1", nil)

	time.Sleep(2 * time.Second)
	if n := f.hub.NumLeafNodes(); n != 0 {
		t.Fatalf("hub accepted a leaf without credentials: %d connections", n)
	}
}

func TestLeafCannotCarryAnotherAgentsSubjects(t *testing.T) {
	f := newFixture(t)
	leaf, err := f.agents.IssueLeafUser("1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	agent := f.startAgent(t, "1", &leaf)
	waitForLeaves(t, f, 1)

	local := f.connectLocal(t, agent)
	for _, id := range []string{"1", "2"} {
		if _, err := local.Subscribe("janus.agent."+id+".command", func(m *nats.Msg) { m.Respond([]byte("paused")) }); err != nil {
			t.Fatal(err)
		}
	}

	control, err := f.janus.IssueUser("control-plane",
		[]string{"janus.agent.>", "_INBOX.>"}, []string{"_INBOX.>"}, false)
	if err != nil {
		t.Fatal(err)
	}
	ncJ, _ := f.connect(t, control)
	waitForReply(t, ncJ, "janus.agent.1.command", "paused")

	// Agent 1's leaf may not announce interest in agent 2's subject, so the
	// hub has no responder for it.
	if _, err := ncJ.Request("janus.agent.2.command", []byte("pause"), time.Second); err == nil {
		t.Fatal("hub reached agent 2 through agent 1's leaf")
	}
}

func TestRevokedLeafIsDisconnectedAndCannotRejoin(t *testing.T) {
	f := newFixture(t)
	leaf, err := f.agents.IssueLeafUser("1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	f.startAgent(t, "1", &leaf)
	waitForLeaves(t, f, 1)

	if err := f.agents.Revoke(leaf.UserPub, time.Now()); err != nil {
		t.Fatal(err)
	}
	acc, err := f.hub.LookupAccount(f.agents.Pub)
	if err != nil {
		t.Fatal(err)
	}
	f.hub.UpdateAccountClaims(acc, f.agents.Claims)
	waitForLeaves(t, f, 0)

	f.startAgent(t, "1", &leaf)
	time.Sleep(2 * time.Second)
	if n := f.hub.NumLeafNodes(); n != 0 {
		t.Fatalf("revoked leaf rejoined: %d connections", n)
	}
}
