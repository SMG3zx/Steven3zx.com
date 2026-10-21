package projectevents

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
)

// recorder remembers what it was told to forget, and fails the first time it
// is told about a project named "flaky".
type recorder struct {
	mu        sync.Mutex
	forgotten chan string
	failed    bool
}

func (r *recorder) ForgetProject(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == "flaky" && !r.failed {
		r.failed = true
		return errors.New("database busy")
	}
	r.forgotten <- "project:" + id
	return nil
}

func (r *recorder) ForgetSnapshot(_ context.Context, id string) error {
	r.forgotten <- "snapshot:" + id
	return nil
}

func publish(t *testing.T, js jetstream.JetStream, subject string, event any) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.Publish(context.Background(), subject, data); err != nil {
		t.Fatal(err)
	}
}

func (r *recorder) next(t *testing.T) string {
	t.Helper()
	select {
	case got := <-r.forgotten:
		return got
	case <-time.After(15 * time.Second):
		t.Fatal("nothing was forgotten")
		return ""
	}
}

func TestEventsPublishedBeforeTheStartAreApplied(t *testing.T) {
	hub := natstest.Start(t)
	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.DeclareEvents(context.Background(), js); err != nil {
		t.Fatal(err)
	}
	publish(t, js, contractprojects.SubjectProjectDeleted, contractprojects.ProjectDeletedEvent{ProjectID: "p1", OwnerID: "alice"})
	publish(t, js, contractprojects.SubjectSnapshotDeleted, contractprojects.SnapshotDeletedEvent{SnapshotID: "s1", ProjectID: "p2", OwnerID: "alice"})

	r := &recorder{forgotten: make(chan string, 10)}
	events := New(r)
	if err := events.Start(context.Background(), hub.Conn()); err != nil {
		t.Fatal(err)
	}
	defer events.Stop(context.Background())
	if got := r.next(t); got != "project:p1" {
		t.Fatalf("first = %q", got)
	}
	if got := r.next(t); got != "snapshot:s1" {
		t.Fatalf("second = %q", got)
	}
}

func TestAnEventThatFailsIsAppliedAgain(t *testing.T) {
	hub := natstest.Start(t)
	r := &recorder{forgotten: make(chan string, 10)}
	events := New(r)
	if err := events.Start(context.Background(), hub.Conn()); err != nil {
		t.Fatal(err)
	}
	defer events.Stop(context.Background())
	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	publish(t, js, contractprojects.SubjectProjectDeleted, contractprojects.ProjectDeletedEvent{ProjectID: "flaky", OwnerID: "alice"})
	if got := r.next(t); got != "project:flaky" {
		t.Fatalf("after a retry = %q", got)
	}
}
