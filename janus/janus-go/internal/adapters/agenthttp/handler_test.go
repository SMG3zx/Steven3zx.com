package agenthttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"janus/internal/domain/agent"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"janus/internal/adapters/localstatetest"
	"janus/internal/adapters/runnertest"
	"janus/internal/app"
)

var module = []byte("\x00asm\x01\x00\x00\x00agenthttp")

func env(input app.RunInput, name string) string {
	for _, entry := range input.Env {
		if key, value, ok := strings.Cut(entry, "="); ok && key == name {
			return value
		}
	}
	return ""
}

func serving(t *testing.T, authorize func(context.Context, string) error) (*agent.Runtime, *httptest.Server) {
	t.Helper()
	runner := runnertest.NewFake()
	runner.Handle(module, func(input app.RunInput) (app.RunResult, error) {
		out := fmt.Sprintf("Status: 201 Created\nContent-Type: text/plain\nX-Seen-Deployment: %s\n\n%s %s from %s: %s",
			env(input, "HTTP_JANUS_DEPLOYMENT"), env(input, "REQUEST_METHOD"), env(input, "REQUEST_URI"), env(input, "REMOTE_ADDR"), input.Stdin)
		return app.RunResult{Stdout: []byte(out)}, nil
	})
	runtime := agent.NewRuntime(runner, localstatetest.NewMemory(), "", time.Now)
	sum := sha256.Sum256(module)
	if err := runtime.Deploy(context.Background(), app.HostedDeployment{ID: "dep-1", SHA256: hex.EncodeToString(sum[:]), Module: module}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler(runtime, authorize))
	t.Cleanup(server.Close)
	return runtime, server
}

func allowAll(context.Context, string) error { return nil }

func call(t *testing.T, server *httptest.Server, deployment, path string, body []byte) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if deployment != "" {
		request.Header.Set(agent.DeploymentHeader, deployment)
	}
	request.Header.Set("X-Forwarded-For", "198.51.100.7")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response, string(data)
}

func TestHandlerServesADeployment(t *testing.T) {
	_, server := serving(t, allowAll)
	response, body := call(t, server, "dep-1", "/a/b?c=d", []byte("payload"))
	if response.StatusCode != 201 || body != "POST /a/b?c=d from 198.51.100.7: payload" {
		t.Fatalf("got %d %q", response.StatusCode, body)
	}
	if response.Header.Get("X-Seen-Deployment") != "" {
		t.Fatal("the module saw the Deployment header")
	}
}

func TestHandlerRefusals(t *testing.T) {
	runtime, server := serving(t, allowAll)
	if response, _ := call(t, server, "", "/", nil); response.StatusCode != http.StatusBadRequest {
		t.Errorf("no Deployment header: %d", response.StatusCode)
	}
	if response, _ := call(t, server, "other", "/", nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("unknown Deployment: %d", response.StatusCode)
	}
	if response, _ := call(t, server, "dep-1", "/", make([]byte, agent.MaxBodyBytes+1)); response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("large body: %d", response.StatusCode)
	}
	if err := runtime.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	if response, _ := call(t, server, "dep-1", "/", nil); response.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("paused: %d", response.StatusCode)
	}
}

func TestHandlerRefusesACallerItDoesNotAuthorize(t *testing.T) {
	_, server := serving(t, func(context.Context, string) error { return errors.New("no") })
	if response, body := call(t, server, "dep-1", "/", nil); response.StatusCode != http.StatusForbidden || strings.Contains(body, "POST") {
		t.Fatalf("got %d %q", response.StatusCode, body)
	}
}
