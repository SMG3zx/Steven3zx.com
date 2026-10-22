package projectsnats

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/contract"
	contractaudit "janus/internal/contract/audit"
	contractid "janus/internal/contract/identity"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
	"janus/internal/services/audit"
	"janus/internal/services/projects"
	"janus/internal/services/projects/githubtest"
)

// fixture is the projects service with a fake identity service and a fake GitHub.
type fixture struct {
	conn    *nats.Conn
	connect func(owner string)
	publish func(t *testing.T, repository string, files map[string]string) string
	wait    func(t *testing.T, jobID string)
}

// startProjects runs the projects service with a fake identity service and a
// fake GitHub.
func startProjects(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	hub := natstest.Start(t)
	conn := hub.Conn()
	github := githubtest.New(t)

	var mu sync.Mutex
	connected := map[string]bool{}
	identity, err := micro.AddService(conn, micro.Config{Name: "identity-fake", Version: "0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = identity.Stop() })
	if err := natsx.AddEndpoint(identity, "token", contractid.SubjectGitHubToken, func(_ context.Context, request contractid.GitHubToken) (contractid.GitHubTokenFound, error) {
		mu.Lock()
		defer mu.Unlock()
		return contractid.GitHubTokenFound{Connected: connected[request.UserID], Token: githubtest.Token}, nil
	}); err != nil {
		t.Fatal(err)
	}

	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.DeclareStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	finished := make(chan string, 100)
	if _, err := conn.Subscribe(contractaudit.Subject(contractprojects.ServiceName), func(msg *nats.Msg) {
		var event contractaudit.Event
		if json.Unmarshal(msg.Data, &event) == nil && (event.Name == "project.import.completed" || event.Name == "project.import.failed") {
			finished <- event.Fields["job_id"]
		}
	}); err != nil {
		t.Fatal(err)
	}

	service := projects.New(projects.Config{DatabasePath: filepath.Join(t.TempDir(), "projects.db"), GitHubAPI: github.APIURL(), GitBase: github.GitBase()})
	if err := service.Start(ctx, conn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Stop(context.Background()) })

	return fixture{
		conn: conn,
		connect: func(owner string) {
			mu.Lock()
			defer mu.Unlock()
			connected[owner] = true
		},
		publish: func(t *testing.T, repository string, files map[string]string) string {
			return github.Commit(t, repository, files)
		},
		wait: func(t *testing.T, jobID string) {
			t.Helper()
			for {
				select {
				case done := <-finished:
					if done == jobID {
						return
					}
				case <-time.After(30 * time.Second):
					t.Fatalf("import %s did not finish", jobID)
				}
			}
		},
	}
}

// TestTheGatewayWritesWhatItReads changes Projects through the Gateway's contract
// types and checks the service answers each write with the contract's errors.
func TestTheGatewayWritesWhatItReads(t *testing.T) {
	f := startProjects(t)
	ctx := context.Background()
	f.connect("alice")
	gateway := NewGateway(f.conn)
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

// TestTheGatewayReadsWhatItWrites creates a Project through the Gateway and reads it
// back, with the contract's types.
func TestTheGatewayReadsWhatItWrites(t *testing.T) {
	f := startProjects(t)
	ctx := context.Background()
	f.connect("alice")
	gateway := NewGateway(f.conn)
	if _, err := gateway.CreateProject(ctx, contractprojects.CreateProject{ID: "p1", OwnerID: "alice", Name: "Demo"}); err != nil {
		t.Fatal(err)
	}
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

// TestAnImportThroughTheGatewayCompletes requests an Import of a published commit
// and reads the finished Import and the Project's Snapshot back.
func TestAnImportThroughTheGatewayCompletes(t *testing.T) {
	f := startProjects(t)
	ctx := context.Background()
	f.connect("alice")
	gateway := NewGateway(f.conn)
	if _, err := gateway.CreateProject(ctx, contractprojects.CreateProject{ID: "p", OwnerID: "alice", Name: "Imported"}); err != nil {
		t.Fatal(err)
	}
	commit := f.publish(t, "alice/imported", map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	jobID, err := gateway.RequestImport(ctx, contractprojects.RequestImport{OwnerID: "alice", ProjectID: "p", Repository: "alice/imported", Branch: "main", Commit: commit})
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, jobID)
	job, found, err := gateway.FindImportJob(ctx, jobID, "alice")
	if err != nil || !found || job.Status != "complete" {
		t.Fatalf("FindImportJob = %+v, %v, %v", job, found, err)
	}
	snapshots, err := gateway.ListSnapshots(ctx, "alice", "p")
	if err != nil || len(snapshots) != 1 || !snapshots[0].Active || snapshots[0].Commit != commit {
		t.Fatalf("ListSnapshots = %+v, %v", snapshots, err)
	}
}

func TestNobodyAnsweringIsAnErrorButNotARefusal(t *testing.T) {
	gateway := NewGateway(natstest.Start(t).Conn())
	_, _, err := gateway.GetProject(context.Background(), "p", "alice")
	var refusal *contract.Error
	if err == nil || errors.As(err, &refusal) && refusal.Status != contract.StatusUnavailable {
		t.Fatalf("with no projects service = %v, want an unavailable error", err)
	}
}
