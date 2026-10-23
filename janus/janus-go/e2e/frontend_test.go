package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// freeAddress returns a loopback address nothing listens on yet, so a server
// started on it can be given to the gateway before it starts.
func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

// startFrontend builds the Qwik web frontend (janus/web) and runs it under Bun
// on address. Its pages call the gateway at apiURL, and visitors reach it at
// publicURL. The bundle is built on every run, so the test runs the code as it
// is. The test is skipped when Bun is not installed.
func startFrontend(t *testing.T, address, apiURL, publicURL string) {
	t.Helper()
	bun, err := exec.LookPath("bun")
	if err != nil {
		if testingOnlyE2E() {
			t.Fatalf("Bun is required for the web frontend: %v", err)
		}
		t.Skip("Bun is not installed; the web frontend cannot run")
	}
	web, err := filepath.Abs(filepath.Join("..", "..", "web"))
	if err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{"build.client", "build.server"} {
		build := exec.Command(bun, "run", script)
		build.Dir = web
		if output, buildErr := build.CombinedOutput(); buildErr != nil {
			t.Fatalf("bun run %s: %v\n%s", script, buildErr, output)
		}
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	server := exec.Command(bun, "server/entry.bun.js")
	server.Dir = web
	server.Env = append(os.Environ(),
		"PORT="+port,
		"JANUS_API_URL="+apiURL,
		"JANUS_WEB_URL="+publicURL,
	)
	server.Stdout = os.Stdout
	server.Stderr = os.Stderr
	if err := server.Start(); err != nil {
		t.Fatalf("start the web frontend: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Process.Kill()
		_, _ = server.Process.Wait()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/healthz", address), nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("the web frontend did not become healthy on %s", address)
		case <-time.After(100 * time.Millisecond):
		}
	}
}
