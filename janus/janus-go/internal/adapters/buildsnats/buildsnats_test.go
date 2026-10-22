package buildsnats

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/app"
	contractaudit "janus/internal/contract/audit"
	contractbuilds "janus/internal/contract/builds"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
	"janus/internal/services/audit"
	"janus/internal/services/builds"
)

// artifactBytes is what the fake compiler returns for every Build.
var artifactBytes = []byte("zip")

// startBuilds runs the builds service on its own NATS server, with a fake
// projects service that has one Rust Project of alice's, and returns the
// connection the adapter uses and the audit events the service writes.
func startBuilds(t *testing.T) (*nats.Conn, <-chan contractaudit.Event) {
	t.Helper()
	ctx := context.Background()
	hub := natstest.Start(t)
	conn := hub.Conn()
	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.DeclareStream(ctx, js); err != nil {
		t.Fatal(err)
	}

	projects, err := micro.AddService(conn, micro.Config{Name: "projects-fake", Version: "0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = projects.Stop() })
	err = errors.Join(
		natsx.AddEndpoint(projects, "get", contractprojects.SubjectGetProject, func(_ context.Context, key contractprojects.ProjectKey) (contractprojects.ProjectFound, error) {
			return contractprojects.ProjectFound{Found: key.OwnerID == "alice", Project: contractprojects.Project{ID: key.ProjectID, OwnerID: key.OwnerID, SnapshotID: "s1", Language: "Rust"}}, nil
		}),
		natsx.AddEndpoint(projects, "list", contractprojects.SubjectListSnapshots, func(_ context.Context, key contractprojects.ProjectKey) (contractprojects.Snapshots, error) {
			return contractprojects.Snapshots{Snapshots: []contractprojects.Snapshot{{ID: "s1", Language: "Rust", Active: true}}}, nil
		}),
		natsx.AddEndpoint(projects, "files", contractprojects.SubjectSnapshotFiles, func(_ context.Context, request contractprojects.SnapshotFiles) (contractprojects.Files, error) {
			return contractprojects.Files{Files: []contractprojects.File{{Path: "Cargo.toml", Mode: "100644", Size: 4, Content: []byte("demo")}}}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan contractaudit.Event, 100)
	if _, err := conn.Subscribe(contractaudit.Subject(contractbuilds.ServiceName), func(msg *nats.Msg) {
		var event contractaudit.Event
		if json.Unmarshal(msg.Data, &event) == nil {
			events <- event
		}
	}); err != nil {
		t.Fatal(err)
	}

	service := builds.New(builds.Config{
		DatabasePath: filepath.Join(t.TempDir(), "builds.db"),
		Compile: func(context.Context, string, builds.CompileRequest) (builds.CompileResult, error) {
			return builds.CompileResult{Name: "wasm-artifacts.zip", Artifact: artifactBytes}, nil
		},
		AckWait: 3 * time.Second,
	})
	if err := service.Start(ctx, conn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Stop(context.Background()) })
	return conn, events
}

// waitCompleted waits until the service audits that the Build completed.
func waitCompleted(t *testing.T, events <-chan contractaudit.Event, id string) {
	t.Helper()
	for {
		select {
		case event := <-events:
			if event.Name == "project.wasm.build.completed" && event.Fields["job_id"] == id {
				return
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("build %s did not complete", id)
		}
	}
}

func TestBuildsAreStartedReadAndRestoredThroughTheService(t *testing.T) {
	ctx := context.Background()
	conn, events := startBuilds(t)
	client, err := New(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}

	started, err := client.StartBuild(ctx, app.StartBuild{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: "s1", Target: "wasm32-unknown-unknown"})
	// The worker may already have picked the job up, so the status is queued or building.
	if err != nil || started.ID != "b1" || (started.Status != contractbuilds.StatusQueued && started.Status != contractbuilds.StatusBuilding) {
		t.Fatalf("StartBuild = %+v, %v", started, err)
	}
	waitCompleted(t, events, "b1")

	build, found, err := client.FindBuild(ctx, "alice", "b1")
	if err != nil || !found || build.Status != contractbuilds.StatusComplete || build.SnapshotID != "s1" {
		t.Fatalf("FindBuild = %+v, %v, %v", build, found, err)
	}
	if _, found, _ := client.FindBuild(ctx, "bob", "b1"); found {
		t.Fatal("bob found alice's build")
	}

	artifact, err := client.Artifact(ctx, "alice", "b1")
	if err != nil || !artifact.Found || artifact.Name != "wasm-artifacts.zip" || string(artifact.Data) != string(artifactBytes) {
		t.Fatalf("Artifact = %+v, %v", artifact, err)
	}
	if other, err := client.Artifact(ctx, "bob", "b1"); err != nil || other.Found {
		t.Fatalf("another Owner read the artifact: %+v, %v", other, err)
	}

	// The object goes missing; the next read copies it back from builds.db.
	if err := client.gateway.artifacts.Delete(ctx, "b1"); err != nil {
		t.Fatal(err)
	}
	restored, err := client.Artifact(ctx, "alice", "b1")
	if err != nil || !restored.Found || string(restored.Data) != string(artifactBytes) {
		t.Fatalf("Artifact after the object was lost = %+v, %v", restored, err)
	}
}

func TestARefusalFromTheServiceIsAReplyError(t *testing.T) {
	ctx := context.Background()
	conn, events := startBuilds(t)
	client, err := New(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartBuild(ctx, app.StartBuild{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: "s1", Target: "wasm32-unknown-unknown"}); err != nil {
		t.Fatal(err)
	}
	waitCompleted(t, events, "b1")

	_, err = client.RetryBuild(ctx, "alice", "b1")
	var reply *app.ReplyError
	if !errors.As(err, &reply) || err.Error() != "only a failed WASM build can be retried" {
		t.Fatalf("RetryBuild of a complete build = %v, want a reply error", err)
	}
	if err := client.DeleteOwner(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := client.FindBuild(ctx, "alice", "b1"); found {
		t.Fatal("a deleted Owner's build survived")
	}
	if _, err := client.Artifact(ctx, "alice", "b1"); err != nil {
		t.Fatalf("reading a deleted Owner's artifact = %v", err)
	}
}
