package buildsnats

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"janus/internal/contract"
	contractbuilds "janus/internal/contract/builds"
)

// TestTheGatewayRequestsReadsAndRestoresBuildsInTheContractsTypes runs a Build
// through the Gateway, which speaks the contract's types, and reads it back.
func TestTheGatewayRequestsReadsAndRestoresBuildsInTheContractsTypes(t *testing.T) {
	conn, events := startBuilds(t)
	ctx := context.Background()
	gateway, err := NewGateway(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}

	requested, err := gateway.Request(ctx, contractbuilds.RequestBuild{ID: "g1", OwnerID: "alice", ProjectID: "p1", Target: "wasm32-wasip1"})
	if err != nil || requested.ID != "g1" {
		t.Fatalf("Request = %+v, %v", requested, err)
	}
	waitCompleted(t, events, "g1")

	found, ok, err := gateway.Find(ctx, "alice", "g1")
	if err != nil || !ok || found.Status != "complete" {
		t.Fatalf("Find = %+v, %v, %v", found, ok, err)
	}
	if _, ok, _ := gateway.Find(ctx, "bob", "g1"); ok {
		t.Fatal("bob found alice's Build")
	}

	artifact, data, err := gateway.Artifact(ctx, "alice", "g1")
	if err != nil || !artifact.Found || !bytes.Equal(data, artifactBytes) {
		t.Fatalf("Artifact = %+v, %q, %v", artifact, data, err)
	}

	if _, err := gateway.Retry(ctx, "alice", "g1"); !errors.Is(err, contract.ErrInvalid) {
		t.Fatalf("Retry of a complete Build = %v, want invalid", err)
	}
}
