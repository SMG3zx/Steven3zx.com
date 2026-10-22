package builds

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/contract/account"
	contractbuilds "janus/internal/contract/builds"
)

// TestDeletingAnAccountClearsItsBuildsAndConfirms plays identity's announcement of
// an Account's deletion. The builds service removes that Owner's Builds and their
// Artifacts, and confirms with its own event.
func TestDeletingAnAccountClearsItsBuildsAndConfirms(t *testing.T) {
	h := start(t)
	id := h.request("b1")
	h.waitFor(id, "project.wasm.build.completed")

	js, err := jetstream.New(h.conn)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(account.Deleting{UserID: testOwner})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.Publish(context.Background(), account.DeletingSubject, data); err != nil {
		t.Fatal(err)
	}

	var confirmation account.Cleared
	if err := json.Unmarshal(h.waitEvent(account.ClearedSubject(contractbuilds.ServiceName)).Data, &confirmation); err != nil {
		t.Fatal(err)
	}
	if confirmation.OwnerID != testOwner || confirmation.Service != contractbuilds.ServiceName {
		t.Fatalf("confirmation = %+v, want %s cleared by builds", confirmation, testOwner)
	}
	if found := h.find(id); found.Found {
		t.Fatalf("build survived its Account's deletion: %+v", found)
	}
	if _, err := h.objectStore().GetBytes(context.Background(), id); !errors.Is(err, jetstream.ErrObjectNotFound) {
		t.Fatalf("artifact survived its Account's deletion: %v", err)
	}
}
