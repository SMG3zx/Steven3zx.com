package fleet_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/contract/account"
	contractfleet "janus/internal/contract/fleet"
)

// TestDeletingAnAccountStopsItsDeploymentsAndRemovesItsAgents plays identity's
// announcement of an Account's deletion. The fleet service stops the Account's
// Deployments, takes its Agents off the tailnet, deletes its records, and confirms.
func TestDeletingAnAccountStopsItsDeploymentsAndRemovesItsAgents(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	s.deploy(s.build("b1", "passed", pageModule))

	cleared := make(chan *nats.Msg, 1)
	subscription, err := s.Conn().Subscribe(account.ClearedSubject(contractfleet.ServiceName), func(msg *nats.Msg) {
		cleared <- msg
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = subscription.Unsubscribe() }()

	js, err := jetstream.New(s.Conn())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(account.Deleting{UserID: s.owner})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.Publish(ctx, account.DeletingSubject, data); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-cleared:
		var confirmation account.Cleared
		if err := json.Unmarshal(msg.Data, &confirmation); err != nil {
			t.Fatal(err)
		}
		if confirmation.OwnerID != s.owner || confirmation.Service != contractfleet.ServiceName {
			t.Fatalf("confirmation = %+v, want %s cleared by fleet", confirmation, s.owner)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("fleet did not confirm the Account's deletion")
	}

	if agents, err := s.Fleet.ListAgents(ctx, s.owner); err != nil || len(agents) != 0 {
		t.Fatalf("agents after the deletion = %+v, %v", agents, err)
	}
	if nodes, err := s.Tailnet.Nodes(ctx); err != nil || len(nodes) != 0 {
		t.Fatalf("tailnet nodes after the deletion = %+v, %v", nodes, err)
	}
	if _, found, err := s.Fleet.FindDeployment(ctx, s.owner, "b1"); err != nil || found {
		t.Fatalf("deployment after the deletion: found %v, %v", found, err)
	}
}
