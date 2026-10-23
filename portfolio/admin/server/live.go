package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	// frameInterval is how often a connected dashboard receives its topics.
	frameInterval = 3 * time.Second
	// logFlushInterval and logFlushSize batch log lines so a busy container
	// sends a handful of messages a second rather than one per line.
	logFlushInterval = 250 * time.Millisecond
	logFlushSize     = 200
	// writeTimeout bounds one message to the browser. A stalled client is dropped.
	writeTimeout = 10 * time.Second
)

// subscription is what the browser is drawing right now. Topics name parts of
// the agent snapshot. Logs is the container whose output is open, or "".
type subscription struct {
	Topics []string `json:"topics"`
	Logs   string   `json:"logs"`
}

// logLine mirrors the agent's NDJSON line.
type logLine struct {
	Time   string `json:"time,omitempty"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// logEvent is one line from an agent log stream, or the stream's end.
type logEvent struct {
	gen  int
	line *logLine
	err  error
}

type frameMessage struct {
	Type string                     `json:"type"`
	Data map[string]json.RawMessage `json:"data"`
}

// changedTopics returns the subscribed topics whose value differs from sent,
// and records them there. Values are compared as the agent wrote them, so an
// unchanged topic compares equal byte for byte.
func changedTopics(sent map[string]json.RawMessage, snap map[string]json.RawMessage, topics []string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, t := range topics {
		v, ok := snap[t]
		if !ok {
			continue
		}
		if prev, seen := sent[t]; seen && bytes.Equal(prev, v) {
			continue
		}
		sent[t] = v
		out[t] = v
	}
	return out
}

type logMessage struct {
	Type      string    `json:"type"`
	Container string    `json:"container"`
	Reset     bool      `json:"reset,omitempty"`
	Lines     []logLine `json:"lines,omitempty"`
}

// errorMessage reports a problem. Scope says which view it belongs to: "frame"
// for the status panels, "logs" for the open log stream.
type errorMessage struct {
	Type    string `json:"type"`
	Scope   string `json:"scope"`
	Message string `json:"message"`
}

// live upgrades a signed-in browser to a WebSocket. The browser sends a
// subscription (which topics and which container's logs it needs). The server
// answers with frames holding only those topics, and with batched log lines.
// Nothing is sent for topics the open views do not draw.
func (s *server) live(w http.ResponseWriter, r *http.Request) {
	// The server's read and write timeouts exist for plain HTTP. A socket stays
	// open for as long as the page does, so clear them before the upgrade.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has already answered the request.
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 10)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// The reader goroutine is the only sender on subs. It replaces an unread
	// subscription with the newest one, so the writer always sees the latest.
	subs := make(chan subscription, 1)
	go func() {
		defer cancel()
		for {
			var next subscription
			if err := wsjson.Read(ctx, conn, &next); err != nil {
				return
			}
			select {
			case <-subs:
			default:
			}
			subs <- next
		}
	}()

	sendJSON := func(v any) error {
		wctx, done := context.WithTimeout(ctx, writeTimeout)
		defer done()
		return wsjson.Write(wctx, conn, v)
	}

	var (
		sub      subscription
		logGen   int
		stopLogs = func() {}
		pending  []logLine
	)
	logCh := make(chan logEvent, 64)
	defer func() { stopLogs() }()

	frames := time.NewTicker(frameInterval)
	defer frames.Stop()
	flush := time.NewTicker(logFlushInterval)
	defer flush.Stop()

	// sent holds the last value of each topic this browser has been sent. The
	// browser keeps every topic it has received, so a topic that matches sent
	// is already up to date there and is left out of the frame.
	sent := map[string]json.RawMessage{}
	sendFrame := func() error {
		if len(sub.Topics) == 0 {
			return nil
		}
		snap, err := s.agentSnapshot(ctx, sub.Topics)
		if err != nil {
			log.Printf("live frame: %v", err)
			return sendJSON(errorMessage{Type: "error", Scope: "frame", Message: "host agent unreachable"})
		}
		data := changedTopics(sent, snap, sub.Topics)
		if len(data) == 0 {
			return nil
		}
		// collectedAt rides along with any frame so the browser's clock stays current.
		data["collectedAt"] = snap["collectedAt"]
		return sendJSON(frameMessage{Type: "frame", Data: data})
	}
	sendLogs := func() error {
		if len(pending) == 0 {
			return nil
		}
		msg := logMessage{Type: "log", Container: sub.Logs, Lines: pending}
		pending = nil
		return sendJSON(msg)
	}

	for {
		select {
		case <-ctx.Done():
			return

		case next := <-subs:
			if next.Logs != sub.Logs {
				stopLogs()
				pending = nil
				logGen++
				stopLogs = func() {}
				if next.Logs != "" {
					stopLogs = s.startLogs(ctx, logGen, next.Logs, logCh)
				}
				// The reset tells the browser to drop lines from the previous container.
				if err := sendJSON(logMessage{Type: "log", Container: next.Logs, Reset: true}); err != nil {
					return
				}
			}
			sub = next
			if err := sendFrame(); err != nil {
				return
			}

		case <-frames.C:
			if err := sendFrame(); err != nil {
				return
			}

		case ev := <-logCh:
			if ev.gen != logGen {
				continue // Left over from a container the browser no longer shows.
			}
			if ev.line == nil {
				// Lines still waiting for the batch flush go out before the end notice.
				if err := sendLogs(); err != nil {
					return
				}
				msg := "log stream ended"
				if ev.err != nil {
					msg = "log stream: " + ev.err.Error()
				}
				if err := sendJSON(errorMessage{Type: "error", Scope: "logs", Message: msg}); err != nil {
					return
				}
				continue
			}
			pending = append(pending, *ev.line)
			if len(pending) >= logFlushSize {
				if err := sendLogs(); err != nil {
					return
				}
			}

		case <-flush.C:
			if err := sendLogs(); err != nil {
				return
			}
		}
	}
}

// agentSnapshot fetches only the requested topics from the host agent. It
// returns each topic's raw JSON, so values are forwarded without re-encoding.
func (s *server) agentSnapshot(ctx context.Context, topics []string) (map[string]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	q := url.Values{"topics": {strings.Join(topics, ",")}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.agentURL+"/snapshot?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.agentToken)
	resp, err := s.agent.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	var snap map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// startLogs runs streamLogs in the background. The returned func stops it.
func (s *server) startLogs(ctx context.Context, gen int, container string, out chan<- logEvent) context.CancelFunc {
	logCtx, cancel := context.WithCancel(ctx)
	go s.streamLogs(logCtx, gen, container, out)
	return cancel
}

// streamLogs reads one container's log stream from the agent and sends each
// line on out, tagged with gen so the caller can drop lines from an old stream.
// It exits when ctx ends, the agent closes the stream, or the stream fails.
func (s *server) streamLogs(ctx context.Context, gen int, container string, out chan<- logEvent) {
	send := func(ev logEvent) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.agentURL+"/logs/"+url.PathEscape(container), nil)
	if err != nil {
		send(logEvent{gen: gen, err: err})
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.agentToken)
	resp, err := s.agentStream.Do(req)
	if err != nil {
		send(logEvent{gen: gen, err: err})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		send(logEvent{gen: gen, err: errors.New(resp.Status)})
		return
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var l logLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue
		}
		if !send(logEvent{gen: gen, line: &l}) {
			return
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		send(logEvent{gen: gen, err: err})
		return
	}
	send(logEvent{gen: gen})
}
