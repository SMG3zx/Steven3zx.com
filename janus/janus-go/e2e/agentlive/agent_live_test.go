//go:build live

package agentlive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"janus/internal/adapters/agentgrpc"
	"janus/internal/adapters/audittest"
	"janus/internal/adapters/headscale"
	"janus/internal/adapters/httpapi"
	"janus/internal/adapters/sqlstore"
	"janus/internal/adapters/tailnode"
	"janus/internal/app"
	"janus/internal/domain/artifact"
)

const headscaleImage = "headscale/headscale:v0.29.5"

// moduleRoot is the janus-go module, two levels up from this package.
var moduleRoot, _ = filepath.Abs(filepath.Join("..", ".."))

func TestTheRealAgentServesPausesResumesAndSurvivesARestart(t *testing.T) {
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Fatalf("the live test needs Docker: %v", err)
	}
	work := t.TempDir()
	controlURL, tailnet := startHeadscale(t, work)
	agentBinary := build(t, work)
	module := buildModule(t, work)

	// The control plane: real Store, real Headscale, its own tag:ctrl node, and
	// the gRPC link, behind the real HTTP adapter.
	ctx := context.Background()
	store, err := sqlstore.Open(filepath.Join(work, "janus.db"))
	must(t, err)
	t.Cleanup(func() { store.Close() })
	audit := audittest.NewRecorder()
	agents := app.NewAgents(store, tailnet, audit, controlURL, time.Now)
	key, err := agents.ControlPlaneKey(ctx)
	must(t, err)
	upCtx, cancelUp := context.WithTimeout(ctx, 60*time.Second)
	control, err := tailnode.Start(upCtx, tailnode.Config{Dir: filepath.Join(work, "control"), Hostname: "janus-control", ControlURL: controlURL, AuthKey: key.Key})
	cancelUp()
	must(t, err)
	t.Cleanup(func() { control.Close() })
	link := agentgrpc.NewLink(control.Dial, agentgrpc.WithMaxRetryDelay(time.Second))
	t.Cleanup(func() { link.Close() })
	fleet := app.NewFleet(store, tailnet, link, audit)
	fleetCtx, stopFleet := context.WithCancel(ctx)
	fleetDone := make(chan struct{})
	go func() { defer close(fleetDone); fleet.Run(fleetCtx, time.Second) }()
	t.Cleanup(func() { stopFleet(); <-fleetDone })
	policy, err := app.NewPolicy(ctx)
	must(t, err)
	server := httptest.NewServer(httpapi.New(httpapi.Config{
		Audit: audit, Identity: app.NewIdentity(store), Projects: app.NewProjects(store), Accounts: app.NewAccounts(store),
		Authentication: app.NewAuthentication([]byte(strings.Repeat("k", 32)), "janus-live", 30*time.Minute), Authorization: policy,
		Agents: agents, Fleet: fleet,
	}).Handler())
	t.Cleanup(server.Close)
	api := &client{t: t, base: server.URL}
	api.register("alice@example.com")

	// The Owner's machine: a host folder, with a file outside it that must
	// stay out of reach.
	parent := filepath.Join(work, "owner")
	folder := filepath.Join(parent, "folder")
	must(t, os.MkdirAll(folder, 0o755))
	must(t, os.WriteFile(filepath.Join(folder, "inside.txt"), []byte("inside-content"), 0o644))
	outside := filepath.Join(parent, "outside.txt")
	must(t, os.WriteFile(outside, []byte("outside-secret"), 0o644))
	state := filepath.Join(work, "agent-state")

	// Enroll with the real binary and a code from the API.
	var issued struct{ Code string }
	api.do(http.MethodPost, "/api/agents/enrollment-codes", nil, http.StatusCreated, &issued)
	runCommand(t, agentBinary, "enroll", "-state", state, "-server", server.URL, "-folder", folder, "-name", "laptop", issued.Code)
	agent := startAgent(t, agentBinary, state, work)
	agentID := waitConnected(t, api)
	assertNoTCPListener(t, agent.Process.Pid)

	// Deploy a completed build of the CGI app.
	buildID := seedBuild(t, store, module)
	deployCtx, cancelDeploy := context.WithTimeout(ctx, 90*time.Second)
	must(t, fleet.Deploy(deployCtx, aliceID(t, store), agentID, "dep-live", buildID))
	cancelDeploy()

	// Reach it the way the edge will: from the control plane's node over the
	// tailnet, naming the Deployment in the header.
	address := agentAddress(t, tailnet, store, agentID)
	edge := &edgeClient{t: t, http: control.HTTPClient(), base: "http://" + net.JoinHostPort(address, strconv.Itoa(app.AgentServePort))}
	edge.expect(http.MethodGet, "/?x=1", nil, 200, "hello from GET x=1")
	edge.expect(http.MethodGet, "/read?name=inside.txt", nil, 200, "inside-content")
	edge.expect(http.MethodPost, "/write?name=written.txt", []byte("from the module"), 201, "wrote 15 bytes")
	if data, err := os.ReadFile(filepath.Join(folder, "written.txt")); err != nil || string(data) != "from the module" {
		t.Fatalf("the module's write in the folder = %q, %v", data, err)
	}
	edge.expect(http.MethodGet, "/escape?absolute="+filepathToQuery(outside), nil, 403, "every path outside the folder was refused")
	edge.expect(http.MethodGet, "/spin", nil, 500, "")

	// Usage per run reaches the control plane.
	var runs []struct {
		Status          string `json:"status"`
		Probe           bool   `json:"probe"`
		Fuel            uint64 `json:"fuel"`
		PeakMemoryBytes uint64 `json:"peak_memory_bytes"`
		DurationMS      int64  `json:"duration_ms"`
		NetworkBytesIn  uint64 `json:"network_bytes_in"`
		NetworkBytesOut uint64 `json:"network_bytes_out"`
	}
	eventually(t, "the runs to be recorded", 30*time.Second, func() bool {
		api.do(http.MethodGet, "/api/agents/"+agentID+"/runs", nil, http.StatusOK, &runs)
		return len(runs) >= 6
	})
	statuses := map[string]int{}
	sawRequestBytes := false
	for _, run := range runs {
		statuses[run.Status]++
		if run.Fuel == 0 || run.PeakMemoryBytes == 0 {
			t.Errorf("a run without fuel or memory: %+v", run)
		}
		if run.NetworkBytesOut == 0 {
			t.Errorf("a run that answered nothing: %+v", run)
		}
		if run.NetworkBytesIn > 0 {
			sawRequestBytes = true
		}
	}
	if !sawRequestBytes {
		t.Error("no run counted the bytes of its request")
	}
	if statuses["ok"] != 5 || statuses["out_of_fuel"] != 1 {
		t.Fatalf("run statuses = %v, want the probe and four requests ok and one out of fuel", statuses)
	}
	t.Logf("runs: %+v", runs)

	// Pause and resume from the web API, then from the command line.
	api.do(http.MethodPost, "/api/agents/"+agentID+"/pause", nil, http.StatusOK, nil)
	edge.expect(http.MethodGet, "/", nil, 503, "")
	api.do(http.MethodPost, "/api/agents/"+agentID+"/resume", nil, http.StatusOK, nil)
	edge.expect(http.MethodGet, "/", nil, 200, "hello from GET")
	runCommand(t, agentBinary, "pause", "-state", state)
	edge.eventually(http.MethodGet, "/", 503)
	runCommand(t, agentBinary, "resume", "-state", state)
	edge.eventually(http.MethodGet, "/", 200)

	// A restart resumes serving the Deployment with no new Deploy.
	stopAgent(t, agent)
	startAgent(t, agentBinary, state, work)
	edge.eventually(http.MethodGet, "/?after=restart", 200)
	waitConnected(t, api)

	// Removing the Agent revokes its node.
	api.do(http.MethodDelete, "/api/agents/"+agentID, nil, http.StatusNoContent, nil)
	edge.eventually(http.MethodGet, "/", 0)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, within time.Duration, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// startHeadscale runs Headscale in Docker on a free port and returns its URL
// and a prepared adapter.
func startHeadscale(t *testing.T, work string) (string, *headscale.Client) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	config, err := os.ReadFile(filepath.Join(moduleRoot, "internal", "adapters", "headscale", "testdata", "config.yaml"))
	must(t, err)
	config = []byte(strings.Replace(string(config), "server_url: http://127.0.0.1:8080", "server_url: "+url, 1))
	dir := filepath.Join(work, "headscale")
	for _, sub := range []string{"config", "data"} {
		must(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
	}
	must(t, os.WriteFile(filepath.Join(dir, "config", "config.yaml"), config, 0o644))
	name := fmt.Sprintf("janus-agent-live-%d", os.Getpid())
	out, err := exec.Command("docker", "run", "-d", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:8080", port),
		"-v", filepath.ToSlash(filepath.Join(dir, "config"))+":/etc/headscale",
		"-v", filepath.ToSlash(filepath.Join(dir, "data"))+":/var/lib/headscale",
		headscaleImage, "serve").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	eventually(t, "Headscale to be healthy", 90*time.Second, func() bool {
		response, err := http.Get(url + "/health")
		if err != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == http.StatusOK
	})
	keyOut, err := exec.Command("docker", "exec", name, "headscale", "apikeys", "create", "--expiration", "24h").Output()
	must(t, err)
	apiKey := strings.TrimSpace(regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(string(keyOut), ""))
	client := headscale.New(headscale.Config{URL: url, APIKey: apiKey})
	must(t, client.Prepare(context.Background()))
	return url, client
}

// build compiles the real janus-agent binary.
func build(t *testing.T, work string) string {
	t.Helper()
	binary := filepath.Join(work, "janus-agent")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	command := exec.Command("go", "build", "-tags", "ts_omit_peerapiserver", "-o", binary, "./cmd/janus-agent")
	command.Dir = moduleRoot
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build janus-agent: %v\n%s", err, out)
	}
	return binary
}

// buildModule compiles the CGI test app for WASI.
func buildModule(t *testing.T, work string) []byte {
	t.Helper()
	out := filepath.Join(work, "cgiapp.wasm")
	command := exec.Command("go", "build", "-o", out, "./e2e/agentlive/testdata/cgiapp")
	command.Dir = moduleRoot
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build the test module: %v\n%s", err, output)
	}
	data, err := os.ReadFile(out)
	must(t, err)
	return data
}

func runCommand(t *testing.T, binary string, args ...string) {
	t.Helper()
	command := exec.Command(binary, args...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("janus-agent %s: %v\n%s", args[0], err, out)
	}
	t.Logf("janus-agent %s: %s", args[0], strings.TrimSpace(string(out)))
}

// startAgent runs `janus-agent run` as its own process until the test ends.
func startAgent(t *testing.T, binary, state, work string) *exec.Cmd {
	t.Helper()
	logFile, err := os.CreateTemp(work, "agent-*.log")
	must(t, err)
	command := exec.Command(binary, "run", "-state", state)
	command.Stdout, command.Stderr = logFile, logFile
	must(t, command.Start())
	t.Cleanup(func() {
		stopAgent(t, command)
		logFile.Close()
		if t.Failed() {
			data, _ := os.ReadFile(logFile.Name())
			t.Logf("agent log:\n%s", data)
		}
	})
	return command
}

func stopAgent(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if command.ProcessState != nil {
		return
	}
	_ = command.Process.Kill()
	_ = command.Wait()
}

// waitConnected waits until the API shows the Owner's one Agent connected,
// and returns its ID.
func waitConnected(t *testing.T, api *client) string {
	t.Helper()
	var agentID string
	eventually(t, "the Agent to connect", 90*time.Second, func() bool {
		var agents []struct {
			ID        string `json:"id"`
			Connected bool   `json:"connected"`
		}
		api.do(http.MethodGet, "/api/agents", nil, http.StatusOK, &agents)
		if len(agents) == 1 && agents[0].Connected {
			agentID = agents[0].ID
			return true
		}
		return false
	})
	return agentID
}

// assertNoTCPListener checks that the Agent process opened no TCP listening
// socket on the host: everything it serves is on the tailnet's own network
// stack. Its WireGuard UDP socket is how tsnet reaches the tailnet at all.
func assertNoTCPListener(t *testing.T, pid int) {
	t.Helper()
	var out []byte
	var err error
	switch runtime.GOOS {
	case "windows":
		out, err = exec.Command("netstat", "-ano", "-p", "TCP").Output()
	case "linux":
		out, err = exec.Command("ss", "-ltnp").Output()
	default:
		t.Logf("not checking listeners on %s", runtime.GOOS)
		return
	}
	must(t, err)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		listening := strings.Contains(line, "LISTEN")
		owned := (runtime.GOOS == "windows" && len(fields) > 0 && fields[len(fields)-1] == strconv.Itoa(pid)) ||
			strings.Contains(line, "pid="+strconv.Itoa(pid)+",")
		if listening && owned {
			t.Fatalf("the Agent listens on the host: %s", strings.TrimSpace(line))
		}
	}
	udp, _ := exec.Command("netstat", "-ano", "-p", "UDP").Output()
	for _, line := range strings.Split(string(udp), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[len(fields)-1] == strconv.Itoa(pid) {
			t.Logf("Agent UDP socket (WireGuard): %s", strings.TrimSpace(line))
		}
	}
}

func aliceID(t *testing.T, store *sqlstore.Store) string {
	t.Helper()
	found, err := store.FindLocalUser(context.Background(), "alice@example.com")
	must(t, err)
	return found.ID
}

// seedBuild records a completed WASI build of module for alice, as the WASM
// Build pipeline would; building it is not what this test is about.
func seedBuild(t *testing.T, store *sqlstore.Store, module []byte) string {
	t.Helper()
	ctx := context.Background()
	owner := aliceID(t, store)
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "live-project", OwnerID: owner, Name: "live"})
	must(t, err)
	commit := strings.Repeat("a", 40)
	must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: "live-job", OwnerID: owner, ProjectID: "live-project", Repository: "o/r", Branch: "main", Commit: commit}))
	must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: "live-snapshot", JobID: "live-job", OwnerID: owner, ProjectID: "live-project",
		Repository: "o/r", Branch: "main", Commit: commit, MetadataJSON: "{}", Language: "Go"}))
	for _, step := range [][2]string{{"queued", "cloning"}, {"cloning", "scanning"}, {"scanning", "storing"}} {
		must(t, store.TransitionImportJob(ctx, app.JobTransition{ID: "live-job", From: step[0], To: step[1]}))
	}
	must(t, store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: "live-snapshot", JobID: "live-job"}))
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "live-build", OwnerID: owner, ProjectID: "live-project", SnapshotID: "live-snapshot", Target: "wasip1-wasm"}))
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("main.wasm")
	must(t, err)
	_, err = file.Write(module)
	must(t, err)
	must(t, writer.Close())
	compressed, err := artifact.Compress(ctx, archive.Bytes())
	must(t, err)
	must(t, store.CompleteWASMBuild(ctx, app.CompleteWASMBuild{ID: "live-build", ArtifactName: "wasm-artifacts.zip" + artifact.ZstdExtension, Artifact: compressed.Data, Log: []byte("ok")}))
	return "live-build"
}

// agentAddress is the IPv4 tailnet address of the Agent's node.
func agentAddress(t *testing.T, tailnet *headscale.Client, store *sqlstore.Store, agentID string) string {
	t.Helper()
	record, found, err := store.FindAgent(context.Background(), agentID, aliceID(t, store))
	must(t, err)
	if !found {
		t.Fatal("no Agent record")
	}
	nodes, err := tailnet.Nodes(context.Background())
	must(t, err)
	for _, node := range nodes {
		if node.KeyID != record.KeyID {
			continue
		}
		for _, address := range node.Addresses {
			if ip := net.ParseIP(address); ip != nil && ip.To4() != nil {
				return address
			}
		}
	}
	t.Fatal("the Agent's node has no IPv4 address")
	return ""
}

func filepathToQuery(path string) string {
	return strings.ReplaceAll(filepath.ToSlash(path), " ", "%20")
}

// client calls the control plane's HTTP API with alice's session.
type client struct {
	t       *testing.T
	base    string
	session *http.Cookie
}

func (c *client) register(email string) {
	c.t.Helper()
	body := `{"email":"` + email + `","password":"correct horse battery staple"}`
	response, err := http.Post(c.base+"/auth/register", "application/json", strings.NewReader(body))
	must(c.t, err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated || len(response.Cookies()) == 0 {
		c.t.Fatalf("register = %d", response.StatusCode)
	}
	c.session = response.Cookies()[0]
}

func (c *client) do(method, path string, body []byte, want int, out any) {
	c.t.Helper()
	request, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	must(c.t, err)
	request.AddCookie(c.session)
	response, err := http.DefaultClient.Do(request)
	must(c.t, err)
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		c.t.Fatalf("%s %s = %d, want %d: %s", method, path, response.StatusCode, want, data)
	}
	if out != nil {
		must(c.t, json.Unmarshal(data, out))
	}
}

// edgeClient calls the Agent over the tailnet from the control plane's node.
type edgeClient struct {
	t    *testing.T
	http *http.Client
	base string
}

func (e *edgeClient) call(method, path string, body []byte) (int, string, error) {
	request, err := http.NewRequest(method, e.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	request.Header.Set(app.DeploymentHeader, "dep-live")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	response, err := e.http.Do(request.WithContext(ctx))
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(data), nil
}

func (e *edgeClient) expect(method, path string, body []byte, status int, contains string) {
	e.t.Helper()
	got, text, err := e.call(method, path, body)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	if got != status || !strings.Contains(text, contains) {
		e.t.Fatalf("%s %s = %d %q, want %d containing %q", method, path, got, text, status, contains)
	}
}

// eventually waits until a request answers status; status 0 means the Agent
// cannot be reached at all.
func (e *edgeClient) eventually(method, path string, status int) {
	e.t.Helper()
	short := *e.http
	short.Timeout = 5 * time.Second
	edge := &edgeClient{t: e.t, http: &short, base: e.base}
	eventually(e.t, fmt.Sprintf("%s %s to answer %d", method, path, status), 90*time.Second, func() bool {
		got, _, err := edge.call(method, path, nil)
		if status == 0 {
			return err != nil
		}
		return err == nil && got == status
	})
}
