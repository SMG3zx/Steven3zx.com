package projectsnats

import (
	"context"
	"errors"
	"testing"

	"janus/internal/app"
	"janus/internal/contract"
	contractprojects "janus/internal/contract/projects"
)

// TestTheGatewayWritesWhatItReads changes Projects through the Gateway's contract
// types and checks the service answers each write with the contract's errors.
func TestTheGatewayWritesWhatItReads(t *testing.T) {
	f := startProjects(t)
	ctx := context.Background()
	f.Connect("alice")
	gateway := NewGateway(f.Catalog.(*Client).conn)
	if _, err := gateway.CreateProject(ctx, contractprojects.CreateProject{ID: "p2", OwnerID: "alice", Name: "Blank"}); err != nil {
		t.Fatal(err)
	}
	renamed, err := gateway.UpdateProject(ctx, contractprojects.UpdateProject{ID: "p2", OwnerID: "alice", Name: "Renamed", Description: "Kept"})
	if err != nil || renamed.Name != "Renamed" || renamed.Description != "Kept" {
		t.Fatalf("UpdateProject = %+v, %v", renamed, err)
	}
	if _, err := gateway.UpdateProject(ctx, contractprojects.UpdateProject{ID: "p2", OwnerID: "bob", Name: "Stolen"}); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("UpdateProject of another Owner's Project = %v, want not found", err)
	}
	if deleted, err := gateway.DeleteProject(ctx, "p2", "bob"); err != nil || deleted {
		t.Fatalf("DeleteProject of another Owner's Project = %v, %v, want false", deleted, err)
	}
	if deleted, err := gateway.DeleteProject(ctx, "p2", "alice"); err != nil || !deleted {
		t.Fatalf("DeleteProject = %v, %v, want true", deleted, err)
	}
	if _, found, err := gateway.FindImportJob(ctx, "missing", "alice"); err != nil || found {
		t.Fatalf("FindImportJob of a missing Import = %v, %v, want absent", found, err)
	}
}

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
