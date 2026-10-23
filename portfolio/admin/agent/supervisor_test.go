package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const testSupervisorToken = "0123456789abcdef0123456789abcdef"

// The supervisor tests run this test binary as the worker. TestHelperWorker
// does nothing unless the test sets HOSTAGENT_HELPER_WORKER, and then it
// records its pid and either exits or waits to be killed.
func TestHelperWorker(t *testing.T) {
	if os.Getenv("HOSTAGENT_HELPER_WORKER") != "1" {
		return
	}
	f, err := os.OpenFile(os.Getenv("HOSTAGENT_HELPER_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(3)
	}
	fmt.Fprintln(f, os.Getpid())
	f.Close()
	if os.Getenv("HOSTAGENT_HELPER_EXIT") == "1" {
		os.Exit(0)
	}
	time.Sleep(time.Minute)
}

func helperSupervisor(t *testing.T) (*supervisor, string) {
	t.Helper()
	logPath := t.TempDir() + "/pids.log"
	t.Setenv("HOSTAGENT_HELPER_WORKER", "1")
	t.Setenv("HOSTAGENT_HELPER_LOG", logPath)
	s := newSupervisor(os.Args[0], "", testSupervisorToken)
	s.args = []string{"-test.run=^TestHelperWorker$"}
	s.respawnDelay = 10 * time.Millisecond
	return s, logPath
}

func startWorkers(t *testing.T, s *supervisor) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// waitForPids waits until the helper has recorded n starts and returns them.
func waitForPids(t *testing.T, path string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(path)
		pids := strings.Fields(string(data))
		if len(pids) >= n {
			return pids
		}
		if time.Now().After(deadline) {
			t.Fatalf("want %d worker starts, have %d", n, len(pids))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSupervisorRestartsWorkerOnRequest(t *testing.T) {
	s, logPath := helperSupervisor(t)
	startWorkers(t, s)

	first := waitForPids(t, logPath, 1)
	s.requestRestart()
	second := waitForPids(t, logPath, 2)
	if first[0] == second[1] {
		t.Fatalf("restart reused pid %s", first[0])
	}
}

func TestSupervisorRespawnsWorkerThatExits(t *testing.T) {
	s, logPath := helperSupervisor(t)
	t.Setenv("HOSTAGENT_HELPER_EXIT", "1")
	startWorkers(t, s)

	waitForPids(t, logPath, 3)
}

func TestControlRestartNeedsToken(t *testing.T) {
	s := newSupervisor("unused", "", testSupervisorToken)
	ts := httptest.NewServer(s.controlHandler())
	t.Cleanup(ts.Close)

	post := func(auth string) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/restart", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	if code := post(""); code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", code)
	}
	if code := post("Bearer wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d, want 401", code)
	}
	if code := post("Bearer " + testSupervisorToken); code != http.StatusAccepted {
		t.Fatalf("right token: got %d, want 202", code)
	}
	select {
	case <-s.restart:
	default:
		t.Fatal("restart was not requested")
	}
}
