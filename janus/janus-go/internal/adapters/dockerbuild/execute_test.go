package dockerbuild

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDocker replaces the Docker CLI with this test binary. "run" blocks until
// it is killed or the file named by FAKE_DOCKER_RELEASE appears, and "rm"
// records the container name it was asked to remove.
func fakeDocker(t *testing.T) (removed, started string) {
	t.Helper()
	dir := t.TempDir()
	removed, started = filepath.Join(dir, "removed"), filepath.Join(dir, "started")
	t.Setenv("FAKE_DOCKER_REMOVED", removed)
	t.Setenv("FAKE_DOCKER_STARTED", started)
	dockerCommand = func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=TestFakeDockerProcess", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "FAKE_DOCKER_PROCESS=1")
		return cmd
	}
	t.Cleanup(func() {
		dockerCommand = func(ctx context.Context, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "docker", args...)
		}
	})
	return removed, started
}

func TestFakeDockerProcess(t *testing.T) {
	if os.Getenv("FAKE_DOCKER_PROCESS") != "1" {
		t.Skip("helper process")
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	switch args[0] {
	case "run":
		_ = os.WriteFile(os.Getenv("FAKE_DOCKER_STARTED"), []byte("started"), 0o600)
		os.Stdout.WriteString("tar bytes")
		os.Stderr.WriteString("build log")
		if os.Getenv("FAKE_DOCKER_HANG") == "1" {
			time.Sleep(time.Minute)
		}
	case "rm":
		_ = os.WriteFile(os.Getenv("FAKE_DOCKER_REMOVED"), []byte(args[len(args)-1]), 0o600)
	}
	os.Exit(0)
}

func TestExecuteReturnsOutputAndLog(t *testing.T) {
	fakeDocker(t)
	got := Execute(context.Background(), Run{Image: "builder:local", Workspace: t.TempDir(), Command: "true"})
	if got.Err != nil || string(got.Stdout) != "tar bytes" || string(got.Stderr) != "build log" {
		t.Fatalf("Execute = %q %q %v", got.Stdout, got.Stderr, got.Err)
	}
}

func TestExecuteRefusesUntrustedImages(t *testing.T) {
	if got := Execute(context.Background(), Run{Image: "builder; rm -rf /", Workspace: "w", Command: "true"}); got.Err == nil {
		t.Fatal("accepted an image name with shell characters")
	}
}

func TestExecuteStopsTheContainerWhenItsContextEnds(t *testing.T) {
	removed, started := fakeDocker(t)
	t.Setenv("FAKE_DOCKER_HANG", "1")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Ran, 1)
	go func() {
		done <- Execute(ctx, Run{Image: "builder:local", Workspace: t.TempDir(), Command: "sleep 600"})
	}()
	waitForFile(t, started)
	cancel()
	select {
	case got := <-done:
		if got.Err == nil {
			t.Fatal("a cancelled run reported success")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a cancelled run did not return")
	}
	name, err := os.ReadFile(removed)
	if err != nil || !strings.HasPrefix(string(name), "janus-run-") {
		t.Fatalf("container removal = %q, %v; want the run's container removed", name, err)
	}
}

func TestRunArgumentsKeepTheIsolationFlags(t *testing.T) {
	args := strings.Join(runArgs("janus-run-x", Run{Image: "builder:local", Workspace: "/ws", Command: "make"}), " ")
	for _, flag := range []string{"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128", "--memory=2g", "--cpus=2", "type=bind,source=/ws,target=/src,readonly"} {
		if !strings.Contains(args, flag) {
			t.Errorf("docker run arguments lack %s: %s", flag, args)
		}
	}
}

func waitForFile(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(name); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", name)
}
