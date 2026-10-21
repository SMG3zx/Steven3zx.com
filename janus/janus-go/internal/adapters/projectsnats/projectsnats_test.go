package projectsnats

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/adapters/catalogtest"
	"janus/internal/app"
	contractaudit "janus/internal/contract/audit"
	contractid "janus/internal/contract/identity"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
	"janus/internal/services/audit"
	"janus/internal/services/projects"
	"janus/internal/services/projects/githubtest"
)

// startProjects runs the projects service with a fake identity service and a
// fake GitHub, and returns the fixture the Catalog contract drives.
func startProjects(t *testing.T) catalogtest.Fixture {
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

	return catalogtest.Fixture{
		Catalog: New(conn),
		Connect: func(owner string) {
			mu.Lock()
			defer mu.Unlock()
			connected[owner] = true
		},
		Publish: func(t *testing.T, repository string, files map[string]string) string {
			return github.Commit(t, repository, files)
		},
		Wait: func(t *testing.T, jobID string) {
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

func TestClientThroughTheProjectsServicePassesTheCatalogContract(t *testing.T) {
	catalogtest.Run(t, startProjects)
}

func TestAFileLargerThanAMessageArrivesWhole(t *testing.T) {
	f := startProjects(t)
	ctx := context.Background()
	f.Connect("alice")
	if _, err := f.Catalog.CreateProject(ctx, app.CreateProject{ID: "big", OwnerID: "alice", Name: "big"}); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("0123456789abcdef", 100_000) // 1.6 MB: three pieces
	commit := f.Publish(t, "alice/big", map[string]string{"a.txt": "a", "big.bin": large, "main.go": "package main\n\nfunc main() {}\n"})
	jobID, err := f.Catalog.RequestImport(ctx, app.ImportRequest{OwnerID: "alice", ProjectID: "big", Repository: "alice/big", Branch: "main", Commit: commit})
	if err != nil {
		t.Fatal(err)
	}
	f.Wait(t, jobID)
	project, _, err := f.Catalog.GetProject(ctx, "big", "alice")
	if err != nil {
		t.Fatal(err)
	}
	files, err := f.Catalog.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "alice", ProjectID: "big", SnapshotID: project.SnapshotID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[1].Path != "big.bin" || string(files[1].Content) != large || files[2].Path != "main.go" {
		t.Fatalf("got %d files; big.bin has %d bytes, want %d", len(files), len(files[1].Content), len(large))
	}
	next, err := f.Catalog.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "alice", ProjectID: "big", SnapshotID: project.SnapshotID, Offset: 1, Limit: 1})
	if err != nil || len(next) != 1 || string(next[0].Content) != large {
		t.Fatalf("a page of one large file = %d files, %v", len(next), err)
	}
}

func TestNobodyAnsweringIsAnErrorButNotARefusal(t *testing.T) {
	client := New(natstest.Start(t).Conn())
	_, _, err := client.GetProject(context.Background(), "p", "alice")
	if err == nil || app.IsReply(err) {
		t.Fatalf("with no projects service = %v, want an error that is not a refusal", err)
	}
}
