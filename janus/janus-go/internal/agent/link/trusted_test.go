package link

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"janus/internal/domain/agent"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/agent/linktest"
	"janus/internal/agent/statetest"
	contractfleet "janus/internal/contract/fleet"
	domainfleet "janus/internal/domain/fleet"
	"janus/internal/platform/natsauth"
	"janus/internal/platform/natstrust"
	"janus/internal/platform/natsx"
)

// The trusted tests run the link as production does. The hub trusts its operator
// and accounts, the fleet reaches the Agents as a user of AGENTS, and each
// Agent's server joins the hub as a leaf of AGENTS with the credentials its
// enrollment gave it, and runs its own clients as AGENTS too.

// trustSecret keys the test keystore.
const trustSecret = "0123456789abcdef0123456789abcdef"

// trustedHub starts a hub that trusts keys from a fresh keystore, and returns it
// with the trust, the fleet's connection, and the leaf address its Agents join.
// The buckets the control plane makes at start are made here too.
func trustedHub(t *testing.T) (*natsx.Hub, natstrust.Trust, *nats.Conn, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	trust, err := natstrust.Open(ctx, filepath.Join(t.TempDir(), "keys.db"), trustSecret, nil)
	if err != nil {
		t.Fatal(err)
	}
	leafAddress := freeAddress(t)
	hub, err := natsx.Start(ctx, natsx.Config{Address: "127.0.0.1:0", StoreDir: t.TempDir(), Name: t.Name(),
		Trust: trust.Server, ClientOptions: trust.Client, LeafAddress: leafAddress})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = hub.Close(closeCtx)
	})
	fleet, err := nats.Connect(hub.URL(), trust.Fleet...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fleet.Close)
	js, err := jetstream.New(fleet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: contractfleet.AgentStateBucket}); err != nil {
		t.Fatal(err)
	}
	return hub, trust, fleet, leafAddress
}

// trustedAgent starts the Agent agentID with runtime as janus-agent does: its
// server trusts the enrollment's trust, joins the hub through its leaf
// credentials and serves its clients with the local ones.
func trustedAgent(t *testing.T, trust natstrust.Trust, leafAddress string, runtime *agent.Runtime) func() {
	t.Helper()
	dir, err := os.MkdirTemp("", "agent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	leaf, err := trust.Agents.IssueLeafUser(agentID, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	local, err := trust.Agents.IssueUser("local-"+agentID, []string{">"}, []string{">"}, true)
	if err != nil {
		t.Fatal(err)
	}
	leafFile := writeCreds(t, dir, "leaf.creds", leaf)
	localFile := writeCreds(t, dir, "local.creds", local)

	operator, err := jwt.DecodeOperatorClaims(trust.Public.OperatorJWT)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &server.MemAccResolver{}
	if err := resolver.Store(trust.Public.AgentsAccount, trust.Public.AgentsJWT); err != nil {
		t.Fatal(err)
	}
	if err := resolver.Store(trust.Public.SystemAccount, trust.Public.SystemJWT); err != nil {
		t.Fatal(err)
	}
	leafURL, err := url.Parse("nats-leaf://" + leafAddress)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.NewServer(&server.Options{
		ServerName:       "janus-agent-test",
		Host:             "127.0.0.1",
		Port:             -1,
		JetStream:        true,
		JetStreamDomain:  contractfleet.AgentDomain(agentID),
		StoreDir:         filepath.Join(dir, "nats"),
		TrustedOperators: []*jwt.OperatorClaims{operator},
		AccountResolver:  resolver,
		SystemAccount:    trust.Public.SystemAccount,
		NoLog:            true,
		NoSigs:           true,
		LeafNode: server.LeafNodeOpts{Remotes: []*server.RemoteLeafOpts{{
			URLs:         []*url.URL{leafURL},
			Credentials:  leafFile,
			LocalAccount: trust.Public.AgentsAccount,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("the Agent's server did not start")
	}
	conn, err := nats.Connect(srv.ClientURL(), nats.UserCredentials(localFile), nats.CustomInboxPrefix("_INBOX."+agentID))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{Conn: conn, AgentID: agentID, OwnerID: ownerID, Runtime: runtime, Heartbeat: 100 * time.Millisecond})
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("serve the Agent: %v", err)
			}
			conn.Close()
			srv.Shutdown()
			srv.WaitForShutdown()
		})
	}
	// The server's files stay open until it stops, so it stops before the
	// test's directory is removed.
	t.Cleanup(stop)
	return stop
}

func writeCreds(t *testing.T, dir, name string, creds natsauth.Credentials) string {
	t.Helper()
	body, err := creds.CredsFile()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// freeAddress returns a loopback address that was free a moment ago.
func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
}

func TestTheAgentLinkPassesTheContractOverATrustedHub(t *testing.T) {
	linktest.Run(t, func(t *testing.T) linktest.Subject {
		_, trust, fleet, leafAddress := trustedHub(t)
		link, err := NewLink(fleet)
		if err != nil {
			t.Fatal(err)
		}
		return linktest.Subject{Link: link, Start: func(t *testing.T, runtime *agent.Runtime) func() {
			return trustedAgent(t, trust, leafAddress, runtime)
		}}
	})
}

func TestAModuleStagedForATrustedOwnerIsReadByTheAgent(t *testing.T) {
	module := []byte("\x00asm\x01\x00\x00\x00trusted")
	sum := sha256.Sum256(module)
	sha := hex.EncodeToString(sum[:])

	_, trust, fleet, leafAddress := trustedHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	js, err := jetstream.New(fleet)
	if err != nil {
		t.Fatal(err)
	}
	modules, err := js.CreateObjectStore(ctx, jetstream.ObjectStoreConfig{Bucket: contractfleet.ModulesBucket(ownerID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modules.PutBytes(ctx, sha, module); err != nil {
		t.Fatal(err)
	}
	link, err := NewLink(fleet)
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	runtime := agent.NewRuntime(stagedRunner{}, statetest.NewMemory(), "", time.Now)
	stop := trustedAgent(t, trust, leafAddress, runtime)
	defer stop()
	link.Connect(agentID, func(domainfleet.AgentEvent) error { return nil })

	deadline := time.Now().Add(30 * time.Second)
	for {
		err := link.Send(ctx, agentID, contractfleet.AgentCommand{Deploy: &contractfleet.HostedDeployment{
			ID: "dep-1", SHA256: sha, Limits: agent.RunLimits{Fuel: 1_000_000, MemoryBytes: 16 << 20}}})
		if err == nil {
			break
		}
		if !errors.Is(err, domainfleet.ErrAgentUnreachable) || time.Now().After(deadline) {
			t.Fatalf("deploy with the module staged on the hub: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := runtime.State().Deployments; len(got) != 1 || got[0].SHA256 != sha {
		t.Fatalf("the Agent's deployments = %+v, want the staged module", got)
	}
}
