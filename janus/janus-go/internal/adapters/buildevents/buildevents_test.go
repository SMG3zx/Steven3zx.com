package buildevents

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/app"
	contractbuilds "janus/internal/contract/builds"
)

// refs records what the handler applies.
type refs struct {
	mu      sync.Mutex
	builds  []app.WASMBuildChange
	checks  []app.CheckChange
	failing bool
}

func (r *refs) ApplyWASMBuild(_ context.Context, build app.WASMBuildChange) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failing {
		return errors.New("the database is down")
	}
	r.builds = append(r.builds, build)
	return nil
}

func (r *refs) ApplyCheck(_ context.Context, check app.CheckChange) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failing {
		return errors.New("the database is down")
	}
	r.checks = append(r.checks, check)
	return nil
}

// message is the jetstream.Msg the handler sees, and how it was acknowledged.
type message struct {
	subject, data string
	outcome       string
}

func (m *message) Metadata() (*jetstream.MsgMetadata, error) { return nil, nil }
func (m *message) Data() []byte                              { return []byte(m.data) }
func (m *message) Headers() nats.Header                      { return nil }
func (m *message) Subject() string                           { return m.subject }
func (m *message) Reply() string                             { return "" }
func (m *message) Ack() error                                { m.outcome = "ack"; return nil }
func (m *message) DoubleAck(context.Context) error           { m.outcome = "ack"; return nil }
func (m *message) Nak() error                                { m.outcome = "nak"; return nil }
func (m *message) NakWithDelay(time.Duration) error          { m.outcome = "nak"; return nil }
func (m *message) InProgress() error                         { return nil }
func (m *message) Term() error                               { m.outcome = "term"; return nil }
func (m *message) TermWithReason(string) error               { m.outcome = "term"; return nil }

func TestBuildAndCheckEventsAreApplied(t *testing.T) {
	store := &refs{}
	events := New(store)

	build := &message{subject: contractbuilds.SubjectBuildChanged,
		data: `{"build_id":"b1","owner_id":"alice","project_id":"p1","snapshot_id":"s1","target":"wasip1-wasm","status":"complete"}`}
	events.handle(build)
	check := &message{subject: contractbuilds.SubjectCheckChanged,
		data: `{"build_id":"b1","owner_id":"alice","status":"failed","error":"container crashed","log":"log","no_tests":true}`}
	events.handle(check)

	if build.outcome != "ack" || check.outcome != "ack" {
		t.Fatalf("outcomes = %q, %q; want both acknowledged", build.outcome, check.outcome)
	}
	want := app.WASMBuildChange{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: "s1", Target: "wasip1-wasm", Status: "complete"}
	if len(store.builds) != 1 || store.builds[0] != want {
		t.Fatalf("applied builds = %+v", store.builds)
	}
	wantCheck := app.CheckChange{BuildID: "b1", OwnerID: "alice", Status: "failed", Error: "container crashed", Log: "log", NoTests: true}
	if len(store.checks) != 1 || store.checks[0] != wantCheck {
		t.Fatalf("applied checks = %+v", store.checks)
	}
}

func TestAFailedApplyIsDeliveredAgain(t *testing.T) {
	store := &refs{failing: true}
	msg := &message{subject: contractbuilds.SubjectBuildChanged, data: `{"build_id":"b1","status":"queued"}`}
	New(store).handle(msg)
	if msg.outcome != "nak" {
		t.Fatalf("outcome = %q, want the event delivered again", msg.outcome)
	}
}

func TestAnUnreadableEventIsDropped(t *testing.T) {
	msg := &message{subject: contractbuilds.SubjectBuildChanged, data: `{not json`}
	New(&refs{}).handle(msg)
	if msg.outcome != "term" {
		t.Fatalf("outcome = %q, want the event terminated", msg.outcome)
	}
}
