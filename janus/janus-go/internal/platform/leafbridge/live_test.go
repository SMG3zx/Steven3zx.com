//go:build live

package leafbridge_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractfleet "janus/internal/contract/fleet"
	"janus/internal/platform/leafbridge"
	"janus/internal/platform/tailnode"
	"janus/internal/services/fleet/headscale"
)

// Live tests run the leaf over real tsnet nodes joined to a real Headscale in
// Docker. Run them with
//
//	go test -tags live -count=1 -v ./internal/platform/leafbridge
//
// They need Docker and internet access (Headscale fetches the public DERP map).
// They remove the container when they finish.

const headscaleImage = "headscale/headscale:v0.29.5"

// liveTimeout bounds each wait on live state: Headscale starting, a tailnet
// peer appearing, a leaf joining.
const liveTimeout = 90 * time.Second

func TestAnAgentsLeafReachesTheHubOverTheTailnet(t *testing.T) {
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Fatalf("the live test needs Docker: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*liveTimeout)
	defer cancel()
	controlURL, client := startHeadscale(t)
	if err := client.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	hubKey, err := client.CreatePreAuthKey(ctx, contractfleet.ControlTag, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	agentKey, err := client.CreatePreAuthKey(ctx, "tag:acct-live", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	// The hub: a leaf listener on loopback, and the bridge that carries the
	// tailnet's connections to it, as cmd/janus starts them.
	hubLeafPort := freePort(t)
	hubNATS := startNATS(t, &server.Options{
		ServerName:      "janus-hub",
		Host:            "127.0.0.1",
		Port:            -1,
		JetStream:       true,
		JetStreamDomain: "hub",
		StoreDir:        t.TempDir(),
		LeafNode:        server.LeafNodeOpts{Host: "127.0.0.1", Port: hubLeafPort},
	})
	hubNode, err := tailnode.Start(ctx, tailnode.Config{Dir: t.TempDir(), Hostname: "janus-control", ControlURL: controlURL, AuthKey: hubKey.Key})
	if err != nil {
		t.Fatalf("the hub joins the tailnet: %v", err)
	}
	t.Cleanup(func() { _ = hubNode.Close() })
	hubListener, err := hubNode.Listen(contractfleet.AgentLeafPort)
	if err != nil {
		t.Fatal(err)
	}
	hubBridge := leafbridge.ServeHub(hubListener, fmt.Sprintf("127.0.0.1:%d", hubLeafPort))
	t.Cleanup(hubBridge.Close)

	// The Agent: its own tailnet node, a bridge that dials the hub over the
	// tailnet, and an embedded server that joins the hub through that bridge.
	agentNode, err := tailnode.Start(ctx, tailnode.Config{Dir: t.TempDir(), Hostname: "agent-live", ControlURL: controlURL, AuthKey: agentKey.Key})
	if err != nil {
		t.Fatalf("the Agent joins the tailnet: %v", err)
	}
	t.Cleanup(func() { _ = agentNode.Close() })
	hubIP := eventually(t, "the hub's tailnet address", func() (string, bool) {
		address, err := agentNode.PeerAddress(ctx, "janus-control")
		return address, err == nil
	})
	hubAddress := fmt.Sprintf("%s:%d", hubIP, contractfleet.AgentLeafPort)
	agentBridge, leafAddress, err := leafbridge.DialHub(func() (net.Conn, error) {
		dialCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		return agentNode.Dial(dialCtx, hubAddress)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(agentBridge.Close)
	leafURL, err := url.Parse("nats-leaf://" + leafAddress)
	if err != nil {
		t.Fatal(err)
	}
	agentNATS := startNATS(t, &server.Options{
		ServerName:      "janus-agent-1",
		Host:            "127.0.0.1",
		Port:            -1,
		JetStream:       true,
		JetStreamDomain: "agent-1",
		StoreDir:        t.TempDir(),
		LeafNode:        server.LeafNodeOpts{Remotes: []*server.RemoteLeafOpts{{URLs: []*url.URL{leafURL}}}},
	})

	eventually(t, "the leaf to join the hub", func() (struct{}, bool) {
		return struct{}{}, hubNATS.NumLeafNodes() == 1 && agentNATS.NumLeafNodes() == 1
	})

	// A command from the hub reaches the Agent across the tailnet.
	agentConn, err := nats.Connect(agentNATS.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(agentConn.Close)
	if _, err := agentConn.Subscribe("janus.agent.1.command", func(m *nats.Msg) { m.Respond([]byte("ok")) }); err != nil {
		t.Fatal(err)
	}
	hubConn, err := nats.Connect(hubNATS.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hubConn.Close)
	eventually(t, "a command reply from the Agent", func() (struct{}, bool) {
		reply, err := hubConn.Request("janus.agent.1.command", []byte("pause"), 500*time.Millisecond)
		return struct{}{}, err == nil && string(reply.Data) == "ok"
	})

	// Usage recorded while the tailnet is cut reaches the hub after it returns.
	agentJS, err := jetstream.New(agentConn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentJS.CreateStream(ctx, jetstream.StreamConfig{Name: "USAGE", Subjects: []string{"janus.usage.1.>"}}); err != nil {
		t.Fatal(err)
	}
	publishUsage(t, ctx, agentJS, 1, 2)
	fleetJS, err := jetstream.NewWithDomain(hubConn, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := fleetJS.Stream(ctx, "USAGE")
	if err != nil {
		t.Fatalf("the hub reads USAGE through agent-1: %v", err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: "fleet-agent-1", AckPolicy: jetstream.AckExplicitPolicy})
	if err != nil {
		t.Fatal(err)
	}
	if got := drain(t, consumer, 2); len(got) != 2 {
		t.Fatalf("online: got %d reports, want 2", len(got))
	}

	agentBridge.SetUp(false)
	hubBridge.SetUp(false)
	eventually(t, "the leaf to drop", func() (struct{}, bool) {
		return struct{}{}, hubNATS.NumLeafNodes() == 0
	})
	publishUsage(t, ctx, agentJS, 3, 5)
	agentBridge.SetUp(true)
	hubBridge.SetUp(true)
	eventually(t, "the leaf to rejoin", func() (struct{}, bool) {
		return struct{}{}, hubNATS.NumLeafNodes() == 1 && agentNATS.NumLeafNodes() == 1
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

// startHeadscale runs Headscale in Docker with the test configuration and returns
// its URL and a client that holds its API key.
func startHeadscale(t *testing.T) (string, *headscale.Client) {
	t.Helper()
	port := freePort(t)
	controlURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	config, err := os.ReadFile(filepath.Join("..", "..", "services", "fleet", "headscale", "testdata", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	config = []byte(strings.Replace(string(config), "server_url: http://127.0.0.1:8080", "server_url: "+controlURL, 1))
	dir := t.TempDir()
	for _, sub := range []string{"config", "data"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "config.yaml"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("janus-leaf-live-%d", os.Getpid())
	out, err := exec.Command("docker", "run", "-d", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:8080", port),
		"-v", filepath.ToSlash(filepath.Join(dir, "config"))+":/etc/headscale",
		"-v", filepath.ToSlash(filepath.Join(dir, "data"))+":/var/lib/headscale",
		headscaleImage, "serve").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	eventually(t, "Headscale to be healthy", func() (struct{}, bool) {
		response, err := http.Get(controlURL + "/health")
		if err != nil {
			return struct{}{}, false
		}
		response.Body.Close()
		return struct{}{}, response.StatusCode == http.StatusOK
	})
	keyOut, err := exec.Command("docker", "exec", name, "headscale", "apikeys", "create", "--expiration", "24h").Output()
	if err != nil {
		t.Fatal(err)
	}
	apiKey := strings.TrimSpace(regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(string(keyOut), ""))
	client := headscale.New(headscale.Config{URL: controlURL, APIKey: apiKey})
	return controlURL, client
}

// startNATS starts an embedded server and stops it when the test ends.
func startNATS(t *testing.T, opts *server.Options) *server.Server {
	t.Helper()
	opts.NoLog = true
	opts.NoSigs = true
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
	batch, err := c.Fetch(n, jetstream.FetchMaxWait(liveTimeout))
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

// eventually retries try until it reports success. The live state it waits on
// settles on its own, so it polls; the service tests wait on replies and events.
func eventually[T any](t *testing.T, what string, try func() (T, bool)) T {
	t.Helper()
	deadline := time.Now().Add(liveTimeout)
	for {
		if value, ok := try(); ok {
			return value
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
