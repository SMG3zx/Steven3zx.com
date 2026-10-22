package projects

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/contract/account"
	contractprojects "janus/internal/contract/projects"
)

// TestDeletingAnAccountClearsItsProjectsAndConfirms plays identity's announcement
// of an Account's deletion. The projects service removes that Owner's Projects,
// leaves another Owner's Projects alone, and confirms with its own event.
func TestDeletingAnAccountClearsItsProjectsAndConfirms(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	h.project("bob", "b1")

	js, err := jetstream.New(h.conn)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(account.Deleting{UserID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.Publish(context.Background(), account.DeletingSubject, data); err != nil {
		t.Fatal(err)
	}

	var confirmation account.Cleared
	if err := json.Unmarshal(h.event(account.ClearedSubject(contractprojects.ServiceName)).Data, &confirmation); err != nil {
		t.Fatal(err)
	}
	if confirmation.OwnerID != "alice" || confirmation.Service != contractprojects.ServiceName {
		t.Fatalf("confirmation = %+v, want alice cleared by projects", confirmation)
	}
	if found := h.getProject("alice", "p1"); found.Found {
		t.Fatal("alice's Project survived her Account's deletion")
	}
	if found := h.getProject("bob", "b1"); !found.Found {
		t.Fatal("bob's Project was removed with alice's Account")
	}
}
