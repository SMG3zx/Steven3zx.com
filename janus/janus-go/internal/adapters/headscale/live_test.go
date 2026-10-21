//go:build live

// Live tests run the Headscale adapter against a real Headscale server in
// Docker and real tsnet nodes, the way an Agent joins. Run them with
//
//	go test -tags live ./internal/adapters/headscale
//
// They need Docker and internet access (Headscale fetches the public DERP map).
// They start one container, remove it when they finish, and leave nothing behind.
package headscale

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"tailscale.com/tsnet"

	"janus/internal/adapters/audittest"
	"janus/internal/adapters/storetest"
	"janus/internal/adapters/tailnettest"
	"janus/internal/app"
)

const headscaleImage = "headscale/headscale:v0.29.5"

var (
	live    *Client
	liveURL string
)

func TestMain(m *testing.M) {
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		fmt.Fprintln(os.Stderr, "live tests need Docker:", err)
		os.Exit(1)
	}
	stop, err := startHeadscale()
	if err != nil {
		fmt.Fprintln(os.Stderr, "start Headscale:", err)
		os.Exit(1)
	}
	code := m.Run()
	stop()
	os.Exit(code)
}

// startHeadscale runs a Headscale container on a free port and prepares the
// package's live client against it.
func startHeadscale() (stop func(), err error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	liveURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	dir, err := os.MkdirTemp("", "janus-headscale-live-")
	if err != nil {
		return nil, err
	}
	config, err := os.ReadFile(filepath.Join("testdata", "config.yaml"))
	if err != nil {
		return nil, err
	}
	config = []byte(strings.Replace(string(config), "server_url: http://127.0.0.1:8080", "server_url: "+liveURL, 1))
	for _, sub := range []string{"config", "data"} {
		if err = os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	if err = os.WriteFile(filepath.Join(dir, "config", "config.yaml"), config, 0o644); err != nil {
		return nil, err
	}

	name := fmt.Sprintf("janus-headscale-live-%d", os.Getpid())
	stop = func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		_ = os.RemoveAll(dir)
	}
	out, err := exec.Command("docker", "run", "-d", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:8080", port),
		"-v", filepath.ToSlash(filepath.Join(dir, "config"))+":/etc/headscale",
		"-v", filepath.ToSlash(filepath.Join(dir, "data"))+":/var/lib/headscale",
		headscaleImage, "serve").CombinedOutput()
	if err != nil {
		stop()
		return nil, fmt.Errorf("docker run: %v: %s", err, out)
	}

	deadline := time.Now().Add(90 * time.Second)
	for {
		response, getErr := http.Get(liveURL + "/health")
		if getErr == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", "--tail", "20", name).CombinedOutput()
			stop()
			return nil, fmt.Errorf("Headscale did not become healthy: %s", logs)
		}
		time.Sleep(500 * time.Millisecond)
	}

	keyOut, err := exec.Command("docker", "exec", name, "headscale", "apikeys", "create", "--expiration", "24h").Output()
	if err != nil {
		stop()
		return nil, fmt.Errorf("create an API key: %w", err)
	}
	apiKey := strings.TrimSpace(regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(string(keyOut), ""))
	live = New(Config{URL: liveURL, APIKey: apiKey})
	if err = live.Prepare(context.Background()); err != nil {
		stop()
		return nil, err
	}
	return stop, nil
}

// join starts a tsnet node that joins the live tailnet with authKey, as an Agent
// does. The node runs until the test ends. It returns the running server.
func join(t *testing.T, authKey, hostname string) (*tsnet.Server, error) {
	t.Helper()
	server := &tsnet.Server{Dir: t.TempDir(), Hostname: hostname, AuthKey: authKey, ControlURL: liveURL,
		Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if _, err := server.Up(ctx); err != nil {
		server.Close()
		return nil, err
	}
	t.Cleanup(func() { server.Close() })
	return server, nil
}

// serve answers HTTP on the node's tailnet port with the node's own name.
func serve(t *testing.T, server *tsnet.Server, port, name string) {
	t.Helper()
	listener, err := server.Listen("tcp", ":"+port)
	if err != nil {
		t.Fatal(err)
	}
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, name) }))
}

// reaches reports whether from can fetch http://address:port within a few seconds.
func reaches(from *tsnet.Server, address, port string) bool {
	client := from.HTTPClient()
	client.Timeout = 5 * time.Second
	response, err := client.Get("http://" + net.JoinHostPort(address, port))
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// addressOf is the IPv4 tailnet address of the node named name.
func addressOf(t *testing.T, name string) string {
	t.Helper()
	nodes, err := live.Nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if node.Name == name {
			for _, address := range node.Addresses {
				if ip := net.ParseIP(address); ip != nil && ip.To4() != nil {
					return address
				}
			}
		}
	}
	t.Fatalf("no node named %s with an IPv4 address in %#v", name, nodes)
	return ""
}

func TestLiveTailnetContract(t *testing.T) {
	tailnettest.Run(t, func(t *testing.T) tailnettest.Subject {
		return tailnettest.Subject{
			Tailnet: live,
			Join: func(t *testing.T, key app.TailnetKey, name string) error {
				_, err := join(t, key.Key, name)
				return err
			},
		}
	})
}

// A tagged node must not expire, or an Agent would drop off the tailnet on its
// own. Headscale reports an unset expiry as null.
func TestLiveEnrolledNodesDoNotExpire(t *testing.T) {
	ctx := context.Background()
	key, err := live.CreatePreAuthKey(ctx, "tag:acct-live-expiry", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = join(t, key.Key, "live-expiry"); err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Nodes []struct {
			Name   string  `json:"name"`
			Expiry *string `json:"expiry"`
		} `json:"nodes"`
	}
	if err = live.do(ctx, http.MethodGet, "/api/v1/node", nil, &listed); err != nil {
		t.Fatal(err)
	}
	for _, node := range listed.Nodes {
		if node.Name == "live-expiry" {
			if node.Expiry != nil {
				t.Fatalf("node %s expires at %s, want no expiry", node.Name, *node.Expiry)
			}
			return
		}
	}
	t.Fatalf("no node named live-expiry in %#v", listed.Nodes)
}

func TestLiveDefaultDenyPolicy(t *testing.T) {
	ctx := context.Background()
	newNode := func(tag, name string) *tsnet.Server {
		key, err := live.CreatePreAuthKey(ctx, tag, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		server, err := join(t, key.Key, name)
		if err != nil {
			t.Fatal(err)
		}
		serve(t, server, "9000", name)
		return server
	}
	control := newNode(app.ControlTag, "policy-control")
	agentA := newNode("tag:acct-policy-a", "policy-agent-a")
	agentB := newNode("tag:acct-policy-b", "policy-agent-b")
	controlIP, aIP, bIP := addressOf(t, "policy-control"), addressOf(t, "policy-agent-a"), addressOf(t, "policy-agent-b")

	eventually(t, "the control plane to reach its Agents", func() bool {
		return reaches(control, aIP, "9000") && reaches(control, bIP, "9000")
	})
	if reaches(agentA, controlIP, "9000") || reaches(agentB, controlIP, "9000") {
		t.Fatal("an Agent reached the control plane")
	}
	if reaches(agentA, bIP, "9000") || reaches(agentB, aIP, "9000") {
		t.Fatal("an Agent reached another Account's Agent")
	}
}

func TestLiveEnrollmentEndToEnd(t *testing.T) {
	ctx := context.Background()
	store, audit := storetest.NewMemory(), audittest.NewRecorder()
	if err := store.AddOwner(ctx, "live-alice"); err != nil {
		t.Fatal(err)
	}
	agents := app.NewAgents(store, live, audit, liveURL, time.Now)

	controlKey, err := live.CreatePreAuthKey(ctx, app.ControlTag, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	control, err := join(t, controlKey.Key, "e2e-control")
	if err != nil {
		t.Fatal(err)
	}

	code, err := agents.IssueCode(ctx, "live-alice")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := agents.Enroll(ctx, code.Code, "e2e-agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = agents.Enroll(ctx, code.Code, "replay"); err == nil {
		t.Fatal("the Enrollment Code worked twice")
	}
	agent, err := join(t, enrollment.AuthKey, "e2e-agent")
	if err != nil {
		t.Fatalf("the Agent could not join with its key: %v", err)
	}
	serve(t, agent, "9000", "e2e-agent")
	if _, err = join(t, enrollment.AuthKey, "e2e-intruder"); err == nil {
		t.Fatal("a second machine joined with the Agent's single-use key")
	}

	listed := waitStatus(t, agents, "live-alice", app.AgentOnline)
	if listed.Tag != "tag:acct-live-alice" {
		t.Fatalf("listed Agent = %#v, want the owner's tag", listed)
	}
	agentIP := addressOf(t, "e2e-agent")
	eventually(t, "the control plane to reach the enrolled Agent", func() bool { return reaches(control, agentIP, "9000") })

	removed, err := agents.Remove(ctx, "live-alice", enrollment.AgentID)
	if err != nil || !removed {
		t.Fatalf("removing the Agent = %v, %v", removed, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for reaches(control, agentIP, "9000") {
		if time.Now().After(deadline) {
			t.Fatal("the control plane can still reach a removed Agent")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// eventually calls check every second until it returns true, for up to 45 s. A node
// that has just joined needs a moment before the others learn of it.
func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Second)
	}
}

// waitStatus polls List until the owner's only Agent has the wanted status.
func waitStatus(t *testing.T, agents *app.Agents, owner, want string) app.AgentStatus {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		listed, err := agents.List(context.Background(), owner)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) == 1 && listed[0].Status == want {
			return listed[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Agent never became %s: %#v", want, listed)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
