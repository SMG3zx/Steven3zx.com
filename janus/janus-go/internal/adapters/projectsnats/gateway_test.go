package projectsnats

import (
	"context"
	"errors"
	"testing"

	"janus/internal/app"
	"janus/internal/contract"
)

// TestTheGatewayReadsWhatTheClientWrites creates a Project through the Client and
// reads it back through the Gateway, which speaks the contract's types.
func TestTheGatewayReadsWhatTheClientWrites(t *testing.T) {
	f := startProjects(t)
	ctx := context.Background()
	client := f.Catalog.(*Client)
	f.Connect("alice")
	if _, err := client.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "Demo"}); err != nil {
		t.Fatal(err)
	}
	gateway := NewGateway(client.conn)
	list, err := gateway.ListProjects(ctx, "alice")
	if err != nil || len(list) != 1 || list[0].ID != "p1" {
		t.Fatalf("ListProjects = %+v, %v", list, err)
	}
	project, found, err := gateway.GetProject(ctx, "p1", "alice")
	if err != nil || !found || project.Name != "Demo" {
		t.Fatalf("GetProject = %+v, %v, %v", project, found, err)
	}
	if _, found, _ := gateway.GetProject(ctx, "p1", "bob"); found {
		t.Fatal("bob read alice's Project")
	}
	if _, err := gateway.ListSnapshots(ctx, "alice", "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.ListSnapshots(ctx, "alice", "missing"); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("snapshots of a missing Project = %v, want not found", err)
	}
	repositories, err := gateway.Repositories(ctx, "alice")
	if err != nil || !repositories.Connected {
		t.Fatalf("Repositories = %+v, %v", repositories, err)
	}
}
