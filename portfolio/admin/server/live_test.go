package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const testAgentToken = "0123456789abcdef0123456789abcdef"

// fakeAgent answers the two agent endpoints the live socket uses. Snapshot
// echoes the topics it was asked for, so the test can check the forwarding.
func fakeAgent(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+testAgentToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /snapshot", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		out := map[string]any{
			"collectedAt": "2026-10-22T10:00:00Z",
			"topics":      r.URL.Query().Get("topics"),
		}
		for t := range strings.SplitSeq(r.URL.Query().Get("topics"), ",") {
			out[t] = map[string]string{"topic": t}
		}
		json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /logs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		enc := json.NewEncoder(w)
		enc.Encode(logLine{Time: "2026-10-22T10:00:00Z", Stream: "stdout", Text: "GET /admin 200"})
		enc.Encode(logLine{Time: "2026-10-22T10:00:01Z", Stream: "stderr", Text: "upstream failed"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func readMessage(t *testing.T, ctx context.Context, c *websocket.Conn) map[string]any {
	t.Helper()
	var msg map[string]any
	if err := wsjson.Read(ctx, c, &msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	return msg
}

func TestLiveSendsOnlyRequestedTopicsAndLogs(t *testing.T) {
	agent := fakeAgent(t)
	srv, token := newJobsTestServer(t)
	srv.agentURL = agent.URL
	srv.agentToken = testAgentToken
	srv.agent = &http.Client{Timeout: 5 * time.Second}
	srv.agentStream = &http.Client{}

	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(ts.URL, "http", "ws", 1)+"/admin/api/live", &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": {cookieName + "=" + token}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	if err := wsjson.Write(ctx, conn, subscription{Topics: []string{"cpu", "network"}, Logs: "steven3zx-edge-traefik-1"}); err != nil {
		t.Fatal(err)
	}

	// The first message clears the log buffer, then a frame arrives with only the topics asked for.
	reset := readMessage(t, ctx, conn)
	if reset["type"] != "log" || reset["reset"] != true || reset["container"] != "steven3zx-edge-traefik-1" {
		t.Fatalf("expected log reset, got %v", reset)
	}
	frame := readMessage(t, ctx, conn)
	if frame["type"] != "frame" {
		t.Fatalf("expected frame, got %v", frame)
	}
	data := frame["data"].(map[string]any)
	if data["cpu"] == nil || data["network"] == nil {
		t.Fatalf("frame lacks requested topics: %v", data)
	}
	if _, ok := data["topics"]; ok {
		t.Fatalf("frame carried an unrequested key: %v", data)
	}

	// Log lines arrive batched, then the agent ends the stream.
	var lines []any
	var ended string
	for ended == "" {
		msg := readMessage(t, ctx, conn)
		switch msg["type"] {
		case "log":
			lines = append(lines, msg["lines"].([]any)...)
		case "error":
			ended = msg["message"].(string)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2", len(lines))
	}
	if ended != "log stream ended" {
		t.Fatalf("stream end message = %q", ended)
	}

	// The fake agent's topic values never change, so the next ticks must send no frame.
	quiet, stop := context.WithTimeout(ctx, frameInterval+time.Second)
	defer stop()
	for {
		var msg map[string]any
		if err := wsjson.Read(quiet, conn, &msg); err != nil {
			break // the window passed with nothing new
		}
		if msg["type"] == "frame" {
			t.Fatalf("unchanged topics were sent again: %v", msg)
		}
	}
}

func TestChangedTopicsSendsOnlyDifferences(t *testing.T) {
	sent := map[string]json.RawMessage{}
	first := map[string]json.RawMessage{
		"collectedAt": json.RawMessage(`"t1"`),
		"cpu":         json.RawMessage(`{"totalPercent":10}`),
		"memory":      json.RawMessage(`{"used":1}`),
	}
	if got := changedTopics(sent, first, []string{"cpu", "memory"}); len(got) != 2 {
		t.Fatalf("first frame sent %d topics, want 2", len(got))
	}

	second := map[string]json.RawMessage{
		"collectedAt": json.RawMessage(`"t2"`),
		"cpu":         json.RawMessage(`{"totalPercent":12}`),
		"memory":      json.RawMessage(`{"used":1}`),
	}
	got := changedTopics(sent, second, []string{"cpu", "memory"})
	if len(got) != 1 || string(got["cpu"]) != `{"totalPercent":12}` {
		t.Fatalf("second frame = %v, want only cpu", got)
	}

	// Topics the browser already has are not sent again, and absent topics are skipped.
	if got := changedTopics(sent, second, []string{"memory", "connections"}); len(got) != 0 {
		t.Fatalf("unchanged memory and absent connections were sent: %v", got)
	}
}

func TestLiveRejectsSignedOutBrowser(t *testing.T) {
	srv, _ := newJobsTestServer(t)
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	_, resp, err := websocket.Dial(context.Background(), strings.Replace(ts.URL, "http", "ws", 1)+"/admin/api/live", nil)
	if err == nil {
		t.Fatal("dial without a session succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", resp)
	}
}
