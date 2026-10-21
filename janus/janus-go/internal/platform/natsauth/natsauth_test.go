package natsauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// denyWait bounds how long a test waits for the server to refuse an operation.
const denyWait = 5 * time.Second

// fixture is a hub with the operator's trust and the JANUS and AGENTS
// accounts, the way fleet and the control plane set them up.
type fixture struct {
	hub      *server.Server
	leafPort int
	resolver *server.MemAccResolver
	sys      *Account
	op       *Operator
	agents   *Account
	janus    *Account
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	op, err := NewOperator("janus")
	if err != nil {
		t.Fatal(err)
	}
	agents, err := NewAccount(op, "AGENTS")
	if err != nil {
		t.Fatal(err)
	}
	janus, err := NewAccount(op, "JANUS")
	if err != nil {
		t.Fatal(err)
	}
	sys, err := NewAccount(op, "SYS")
	if err != nil {
		t.Fatal(err)
	}
	if err := op.SetSystemAccount(sys); err != nil {
		t.Fatal(err)
	}
	// AGENTS exports janus.agent.> as a service; JANUS imports it.
	if err := agents.Export("agent commands", "janus.agent.>"); err != nil {
		t.Fatal(err)
	}
	if err := janus.Import(agents, "agent commands", "janus.agent.>"); err != nil {
		t.Fatal(err)
	}
	if err := agents.EnableJetStream(); err != nil {
		t.Fatal(err)
	}

	leafPort := freePort(t)
	resolver := &server.MemAccResolver{}
	for _, a := range []*Account{agents, janus, sys} {
		if err := resolver.Store(a.Pub, a.JWT); err != nil {
			t.Fatal(err)
		}
	}
	hub, err := server.NewServer(&server.Options{
		ServerName:       "janus-hub",
		Host:             "127.0.0.1",
		Port:             -1,
		TrustedOperators: []*jwt.OperatorClaims{op.Claims},
		AccountResolver:  resolver,
		SystemAccount:    sys.Pub,
		JetStream:        true,
		JetStreamDomain:  "hub",
		StoreDir:         t.TempDir(),
		LeafNode:         server.LeafNodeOpts{Host: "127.0.0.1", Port: leafPort},
		NoLog:            true,
		NoSigs:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	hub.Start()
	if !hub.ReadyForConnections(10 * time.Second) {
		t.Fatal("hub not ready")
	}
	t.Cleanup(func() {
		hub.Shutdown()
		hub.WaitForShutdown()
	})
	return &fixture{hub: hub, leafPort: leafPort, resolver: resolver, sys: sys, op: op, agents: agents, janus: janus}
}

// connect dials the hub as creds and collects the permission errors the
// server reports asynchronously.
func (f *fixture) connect(t *testing.T, creds Credentials, extra ...nats.Option) (*nats.Conn, *violations) {
	t.Helper()
	v := newViolations()
	opts := append(creds.Options(), nats.CustomInboxPrefix("_INBOX"), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
		v.add(err)
	}), nats.NoReconnect())
	opts = append(opts, extra...)
	nc, err := nats.Connect(f.hub.ClientURL(), opts...)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc, v
}

// violations queues the permission errors a client was given. nats.go hands
// them to the error handler on its own goroutine, so a test waits for them
// rather than checking straight after a flush.
type violations struct {
	errs chan string
}

func newViolations() *violations {
	return &violations{errs: make(chan string, 64)}
}

func (v *violations) add(err error) {
	select {
	case v.errs <- err.Error():
	default:
	}
}

// all returns the refusals queued so far, for a failure message.
func (v *violations) all() []string {
	var out []string
	for {
		select {
		case e := <-v.errs:
			out = append(out, e)
		default:
			return out
		}
	}
}

// denied reports whether the server refused an operation on subject within
// the given time. An empty subject matches any refusal.
func (v *violations) denied(subject string, within time.Duration) bool {
	timeout := time.After(within)
	for {
		select {
		case e := <-v.errs:
			if strings.Contains(e, "Permissions Violation") && strings.Contains(e, subject) {
				return true
			}
		case <-timeout:
			return false
		}
	}
}

func TestAgentCanUseItsOwnSubjects(t *testing.T) {
	f := newFixture(t)
	creds, err := f.agents.IssueAgentUser("1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	nc, v := f.connect(t, creds)

	if _, err := nc.Subscribe("janus.agent.1.command", func(m *nats.Msg) { m.Respond([]byte("ok")) }); err != nil {
		t.Fatal(err)
	}
	if err := nc.Publish("$KV.agent_state.1", []byte("paused")); err != nil {
		t.Fatal(err)
	}
	if err := nc.Publish("$JS.API.DIRECT.GET.OBJ_modules-owner-a", nil); err != nil {
		t.Fatal(err)
	}
	if v.denied("", 500*time.Millisecond) {
		t.Fatal("own subjects refused")
	}
}

func TestAgentCannotUseAnotherAgentsSubjects(t *testing.T) {
	f := newFixture(t)
	agent1, err := f.agents.IssueAgentUser("1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	agent2, err := f.agents.IssueAgentUser("2", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	nc1, _ := f.connect(t, agent1)
	nc2, v2 := f.connect(t, agent2)

	// Agent 2 tries to take Agent 1's commands and to write Agent 1's state.
	if _, err := nc2.Subscribe("janus.agent.1.command", func(*nats.Msg) {}); err != nil {
		t.Fatal(err)
	}
	if err := nc2.Publish("janus.agent.1.command", []byte("pause")); err != nil {
		t.Fatal(err)
	}
	if err := nc2.Publish("$KV.agent_state.1", []byte("paused")); err != nil {
		t.Fatal(err)
	}
	if !v2.denied("janus.agent.1.command", denyWait) {
		t.Fatal("agent 2 subscribed to agent 1's commands")
	}
	if !v2.denied("$KV.agent_state.1", denyWait) {
		t.Fatal("agent 2 wrote agent 1's state")
	}

	// Agent 1's commands are still Agent 1's: a request from JANUS reaches it.
	if _, err := nc1.Subscribe("janus.agent.1.command", func(m *nats.Msg) { m.Respond([]byte("agent-1")) }); err != nil {
		t.Fatal(err)
	}
}

func TestAgentCannotReadAnotherOwnersModules(t *testing.T) {
	f := newFixture(t)
	ownerA, err := f.agents.IssueAgentUser("1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	ownerB, err := f.agents.IssueAgentUser("3", "owner-b")
	if err != nil {
		t.Fatal(err)
	}
	ncB, vB := f.connect(t, ownerB)
	if err := ncB.Publish("$JS.API.DIRECT.GET.OBJ_modules-owner-a", nil); err != nil {
		t.Fatal(err)
	}
	if !vB.denied("$JS.API.DIRECT.GET.OBJ_modules-owner-a", denyWait) {
		t.Fatal("owner B's Agent read owner A's modules")
	}

	ncA, vA := f.connect(t, ownerA)
	if err := ncA.Publish("$JS.API.DIRECT.GET.OBJ_modules-owner-a", nil); err != nil {
		t.Fatal(err)
	}
	if vA.denied("$JS.API.DIRECT.GET.OBJ_modules-owner-a", 500*time.Millisecond) {
		t.Fatal("owner A's Agent was refused its own modules")
	}
}

func TestAgentReadsOnlyItsOwnersModules(t *testing.T) {
	f := newFixture(t)
	// fleet writes the modules; it is trusted, so it may do anything in AGENTS.
	fleet, err := f.agents.IssueUser("fleet", []string{">"}, []string{">"}, true)
	if err != nil {
		t.Fatal(err)
	}
	fleetNC, _ := f.connect(t, fleet)
	fleetJS, err := jetstream.New(fleetNC)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	modules, err := fleetJS.CreateObjectStore(ctx, jetstream.ObjectStoreConfig{Bucket: "modules-owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modules.PutBytes(ctx, "module-1", []byte("wasm")); err != nil {
		t.Fatal(err)
	}

	ownerA, err := f.agents.IssueAgentUser("1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	ncA, vA := f.connect(t, ownerA, nats.CustomInboxPrefix("_INBOX.1"))
	jsA, err := jetstream.New(ncA)
	if err != nil {
		t.Fatal(err)
	}
	storeA, err := jsA.ObjectStore(ctx, "modules-owner-a")
	if err != nil {
		t.Fatalf("owner A's Agent cannot open its modules: %v (refused: %v)", err, vA.all())
	}
	got, err := storeA.GetBytes(ctx, "module-1")
	if err != nil {
		t.Fatalf("owner A's Agent cannot read its module: %v", err)
	}
	if string(got) != "wasm" {
		t.Fatalf("module = %q, want wasm", got)
	}

	ownerB, err := f.agents.IssueAgentUser("3", "owner-b")
	if err != nil {
		t.Fatal(err)
	}
	ncB, vB := f.connect(t, ownerB, nats.CustomInboxPrefix("_INBOX.3"))
	jsB, err := jetstream.New(ncB)
	if err != nil {
		t.Fatal(err)
	}
	// A refused request gets no reply, so the read waits out its deadline; keep
	// that short.
	refused, cancelRefused := context.WithTimeout(ctx, 2*time.Second)
	defer cancelRefused()
	if storeB, err := jsB.ObjectStore(refused, "modules-owner-a"); err == nil {
		if _, err := storeB.GetBytes(refused, "module-1"); err == nil {
			t.Fatal("owner B's Agent read owner A's module")
		}
	}
	if !vB.denied("OBJ_modules-owner-a", denyWait) {
		t.Fatal("owner B's Agent was not refused owner A's modules")
	}
}

func TestJanusReachesAgentThroughImport(t *testing.T) {
	f := newFixture(t)
	agent, err := f.agents.IssueAgentUser("1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	nc1, _ := f.connect(t, agent)
	if _, err := nc1.Subscribe("janus.agent.1.command", func(m *nats.Msg) { m.Respond([]byte("paused")) }); err != nil {
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

func TestRevokedAgentIsDisconnectedAndCannotReconnect(t *testing.T) {
	f := newFixture(t)
	agent, err := f.agents.IssueAgentUser("1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	f.connect(t, agent, nats.ClosedHandler(func(*nats.Conn) { close(closed) }))

	if err := f.agents.Revoke(agent.UserPub, time.Now()); err != nil {
		t.Fatal(err)
	}
	acc, err := f.hub.LookupAccount(f.agents.Pub)
	if err != nil {
		t.Fatal(err)
	}
	f.hub.UpdateAccountClaims(acc, f.agents.Claims)

	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("revoked Agent stayed connected")
	}

	_, err = nats.Connect(f.hub.ClientURL(), agent.Options()...)
	if err == nil {
		t.Fatal("revoked Agent reconnected")
	}
	if !errors.Is(err, nats.ErrAuthorization) && !strings.Contains(err.Error(), "uthorization") {
		t.Fatalf("reconnect failed for another reason: %v", err)
	}
}

// waitForReply retries a request until the import has propagated, then checks
// the reply.
func waitForReply(t *testing.T, nc *nats.Conn, subject, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		reply, err := nc.Request(subject, []byte("pause"), 300*time.Millisecond)
		if err == nil {
			if got := string(reply.Data); got != want {
				t.Fatalf("reply = %q, want %q", got, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reply on %s: %v", subject, err)
		}
	}
}
