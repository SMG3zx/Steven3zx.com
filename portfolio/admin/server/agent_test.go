package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSupervisor answers the restart endpoint the way the host supervisor does
// and records how many restarts it was asked for.
func fakeSupervisor(t *testing.T, status int, restarts *int) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/restart" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+testAgentToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		*restarts++
		w.WriteHeader(status)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func postRestart(t *testing.T, base, token, contentType string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/admin/api/agent/restart", strings.NewReader("{}"))
	req.Header.Set("Cookie", cookieName+"="+token)
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestRestartAgentAsksSupervisor(t *testing.T) {
	restarts := 0
	sup := fakeSupervisor(t, http.StatusAccepted, &restarts)
	srv, token := newJobsTestServer(t)
	srv.agentControlURL = sup.URL
	srv.agentToken = testAgentToken
	srv.agent = &http.Client{Timeout: 5 * time.Second}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	if code := postRestart(t, ts.URL, token, "application/json"); code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}
	if restarts != 1 {
		t.Fatalf("supervisor got %d restarts, want 1", restarts)
	}
}

func TestRestartAgentRefusesCrossSiteAndSignedOut(t *testing.T) {
	restarts := 0
	sup := fakeSupervisor(t, http.StatusAccepted, &restarts)
	srv, token := newJobsTestServer(t)
	srv.agentControlURL = sup.URL
	srv.agentToken = testAgentToken
	srv.agent = &http.Client{Timeout: 5 * time.Second}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	if code := postRestart(t, ts.URL, token, "text/plain"); code != http.StatusForbidden {
		t.Fatalf("form post: status %d, want 403", code)
	}
	if code := postRestart(t, ts.URL, "", "application/json"); code != http.StatusUnauthorized {
		t.Fatalf("signed out: status %d, want 401", code)
	}
	if restarts != 0 {
		t.Fatalf("supervisor got %d restarts, want 0", restarts)
	}
}

func TestRestartAgentReportsSupervisorFailure(t *testing.T) {
	restarts := 0
	sup := fakeSupervisor(t, http.StatusInternalServerError, &restarts)
	srv, token := newJobsTestServer(t)
	srv.agentControlURL = sup.URL
	srv.agentToken = testAgentToken
	srv.agent = &http.Client{Timeout: 5 * time.Second}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	if code := postRestart(t, ts.URL, token, "application/json"); code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", code)
	}
}
