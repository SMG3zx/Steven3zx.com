package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"janus/internal/platform/natstest"
)

// TestEachServiceRunsInItsOwnProcess starts the audit, identity, projects and
// runs services and the gateway as five processes against one NATS server, and
// drives the gateway over HTTP: every answer it gives crosses NATS to a service in
// another process. The builds and fleet services need Docker and Headscale, so
// they are left out; the flows that need them are tested with their services.
func TestEachServiceRunsInItsOwnProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the janus binary and runs five processes")
	}
	hub := natstest.Start(t)
	binary := buildJanus(t)
	work := t.TempDir()
	address := freeAddress(t)
	environment := append(os.Environ(),
		"JANUS_NATS_URL="+hub.URL(),
		"JANUS_JWT_SECRET="+strings.Repeat("s", 32),
		"JANUS_HTTP_ADDRESS="+address,
		"JANUS_IDENTITY_DATABASE_PATH="+filepath.Join(work, "identity.db"),
		"JANUS_PROJECTS_DATABASE_PATH="+filepath.Join(work, "projects.db"),
		"JANUS_BUILDS_DATABASE_PATH="+filepath.Join(work, "builds.db"),
		"JANUS_FLEET_DATABASE_PATH="+filepath.Join(work, "fleet.db"),
		"OTEL_SDK_DISABLED=true",
		"PYROSCOPE_SERVER_ADDRESS=",
	)
	for _, service := range []string{"audit", "identity", "projects", "runs", "gateway"} {
		startProcess(t, binary, work, environment, service)
	}
	base := "http://" + address

	var session *http.Cookie
	eventually(t, "the gateway to register a User through the identity process", func() bool {
		response, err := http.Post(base+"/auth/register", "application/json", strings.NewReader(`{"email":"alice@example.com","password":"correct horse battery staple"}`))
		if err != nil {
			return false
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		for _, cookie := range response.Cookies() {
			if cookie.Name == "janus_session" {
				session = cookie
			}
		}
		return response.StatusCode == http.StatusCreated && session != nil
	})

	created := call(t, http.MethodPost, base+"/api/projects", `{"name":"Across processes","description":""}`, session, http.StatusCreated)
	var project struct {
		ID, Name string
	}
	if err := json.Unmarshal(created, &project); err != nil || project.ID == "" || project.Name != "Across processes" {
		t.Fatalf("created project = %s, %v", created, err)
	}
	listed := call(t, http.MethodGet, base+"/api/projects", "", session, http.StatusOK)
	if !bytes.Contains(listed, []byte(project.ID)) {
		t.Fatalf("projects = %s, want the one just created", listed)
	}
	if profile := call(t, http.MethodGet, base+"/api/me", "", session, http.StatusOK); !bytes.Contains(profile, []byte("alice@example.com")) {
		t.Fatalf("profile = %s", profile)
	}
	// The runs process takes the request and asks for the Build, which the builds
	// service would answer. It is not running, so the refusal names its subject.
	if refused := call(t, http.MethodPost, base+"/api/wasm-builds/no-such-build/runs", `{}`, session, http.StatusBadRequest); !bytes.Contains(refused, []byte("janus.builds.build.find")) {
		t.Fatalf("run refusal = %s, want it to name the builds subject", refused)
	}
}

// TestAServiceNeedsNATSWhenItDoesNotRunEverything refuses to start a subset of
// the services with no NATS server to find the others on.
func TestAServiceNeedsNATSWhenItDoesNotRunEverything(t *testing.T) {
	t.Setenv("JANUS_JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("JANUS_NATS_URL", "")
	t.Chdir(t.TempDir())
	err := newServing(nil, nil, []string{"gateway"}).load()
	if err == nil || !strings.Contains(err.Error(), "JANUS_NATS_URL") {
		t.Fatalf("load for the gateway alone with no NATS URL = %v, want a refusal naming JANUS_NATS_URL", err)
	}
	if err := newApplication(nil, nil).load(); err != nil {
		t.Fatalf("load for every service = %v, want none: it embeds NATS", err)
	}
}

// buildJanus compiles the janus binary into a temporary directory.
func buildJanus(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "janus")
	if os.PathSeparator == '\\' {
		binary += ".exe"
	}
	command := exec.Command("go", "build", "-o", binary, ".")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build janus: %v\n%s", err, output)
	}
	return binary
}

// freeAddress returns a loopback address with a free port.
func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

// lockedBuffer is a process's output, read while the process still writes it.
type lockedBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

// startProcess runs `janus serve service` in dir until the test ends, and shows
// its output when the test fails.
func startProcess(t *testing.T, binary, dir string, environment []string, service string) {
	t.Helper()
	output := &lockedBuffer{}
	command := exec.Command(binary, "serve", service)
	command.Dir, command.Env, command.Stdout, command.Stderr = dir, environment, output, output
	if err := command.Start(); err != nil {
		t.Fatalf("start janus serve %s: %v", service, err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		if t.Failed() {
			t.Logf("janus serve %s:\n%s", service, output.String())
		}
	})
}

// call sends one request with the session and returns the body of an answer with
// the wanted status.
func call(t *testing.T, method, url, body string, session *http.Cookie, want int) []byte {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(session)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		t.Fatalf("%s %s = %d %s, want %d", method, url, response.StatusCode, data, want)
	}
	return data
}

// eventually waits up to 90 seconds for done, trying again every 200 ms.
func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal(fmt.Sprintf("timed out waiting for %s", what))
		}
		time.Sleep(200 * time.Millisecond)
	}
}
