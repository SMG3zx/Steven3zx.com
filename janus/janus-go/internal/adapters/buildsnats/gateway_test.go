package buildsnats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/contract"
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

	if _, err := gateway.Retry(ctx, "alice", "g1"); !errors.Is(err, contract.ErrInvalid) || err.Error() != "only a failed WASM build can be retried" {
		t.Fatalf("Retry of a complete Build = %v, want invalid with the service's sentence", err)
	}

	// The object goes missing; the next read copies it back from builds.db.
	if err := gateway.artifacts.Delete(ctx, "g1"); err != nil {
		t.Fatal(err)
	}
	restored, data, err := gateway.Artifact(ctx, "alice", "g1")
	if err != nil || !restored.Found || !bytes.Equal(data, artifactBytes) {
		t.Fatalf("Artifact after the object was lost = %+v, %q, %v", restored, data, err)
	}
}
