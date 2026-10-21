package storetest

import (
	"context"
	"strings"
	"testing"

	"janus/internal/app"
)

// testChecksAndDeployments holds the rules of Checks and Deployments that every
// Store must keep: a Check starts only on a completed build, a Deployment is
// owned and in flight one at a time, and its state moves compare and swap.
func testChecksAndDeployments(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.AddOwner(ctx, "alice"))
	must(t, store.AddOwner(ctx, "bob"))
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "p1", OwnerID: "alice"}))
	snapshot := ReadySnapshot(t, store, "alice", "p1", "one-snapshot", "Rust")
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "laptop", OwnerID: "alice", Name: "laptop", Tag: "tag:acct-alice", KeyID: "key-alice"}))
	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown", Status: "complete"}))

	must(t, store.ApplyCheck(ctx, app.CheckChange{BuildID: "b1", OwnerID: "alice", Status: app.CheckRunning}))
	check, found, err := store.FindCheck(ctx, "b1", "alice")
	must(t, err)
	if !found || check.Status != app.CheckRunning {
		t.Fatalf("running check = %+v, found %v", check, found)
	}
	if _, found, _ = store.FindCheck(ctx, "b1", "bob"); found {
		t.Fatal("another Account found the check")
	}
	must(t, store.ApplyCheck(ctx, app.CheckChange{BuildID: "b1", OwnerID: "alice", Status: app.CheckFailed, Error: "container crashed", Log: "log"}))
	check, _, err = store.FindCheck(ctx, "b1", "alice")
	must(t, err)
	if check.Status != app.CheckFailed || check.Error != "container crashed" || check.Log != "log" {
		t.Fatalf("finished check = %+v", check)
	}

	must(t, store.CreateDeployment(ctx, app.CreateDeployment{ID: "d1", OwnerID: "alice", ProjectID: "p1", BuildID: "b1", AgentID: "laptop"}))
	if err = store.CreateDeployment(ctx, app.CreateDeployment{ID: "d2", OwnerID: "alice", ProjectID: "p1", BuildID: "b1", AgentID: "laptop"}); err == nil || !strings.Contains(err.Error(), "already in flight") {
		t.Fatalf("a second in-flight Deployment = %v", err)
	}
	if err = store.CreateDeployment(ctx, app.CreateDeployment{ID: "d3", OwnerID: "bob", ProjectID: "p1", BuildID: "b1", AgentID: "laptop"}); err == nil {
		t.Fatal("another Account deployed the build")
	}
	deployment, found, err := store.FindDeployment(ctx, "d1", "alice")
	must(t, err)
	if !found || deployment.State != "queued" || deployment.BuildID != "b1" || deployment.DeployAnyway {
		t.Fatalf("queued deployment = %+v, found %v", deployment, found)
	}
	if _, found, _ = store.FindDeployment(ctx, "d1", "bob"); found {
		t.Fatal("another Account found the deployment")
	}
	if err = store.TransitionDeployment(ctx, app.DeploymentTransition{ID: "d1", From: "queued", To: "healthy"}); err == nil || !strings.Contains(err.Error(), "cannot move from queued to healthy") {
		t.Fatalf("queued to healthy = %v", err)
	}
	if err = store.TransitionDeployment(ctx, app.DeploymentTransition{ID: "d1", From: "starting", To: "healthy"}); err == nil || !strings.Contains(err.Error(), "changed while it was being updated") {
		t.Fatalf("a move from a state it is not in = %v", err)
	}
	must(t, store.TransitionDeployment(ctx, app.DeploymentTransition{ID: "d1", From: "queued", To: "starting"}))
	must(t, store.TransitionDeployment(ctx, app.DeploymentTransition{ID: "d1", From: "starting", To: "failed", Error: "Agent refused the module"}))
	deployment, _, err = store.FindDeployment(ctx, "d1", "alice")
	must(t, err)
	if deployment.State != "failed" || deployment.Error != "Agent refused the module" {
		t.Fatalf("failed deployment = %+v", deployment)
	}
	must(t, store.CreateDeployment(ctx, app.CreateDeployment{ID: "d4", OwnerID: "alice", ProjectID: "p1", BuildID: "b1", AgentID: "laptop", DeployAnyway: true}))
	deployment, _, err = store.FindDeployment(ctx, "d4", "alice")
	must(t, err)
	if !deployment.DeployAnyway {
		t.Fatalf("deploy anyway was not kept: %+v", deployment)
	}
}

// testRedeployAndStop holds the rules a redeploy, a rollback and a stop rely on.
// A starting Deployment becomes healthy and stops the Project's healthy
// Deployments in one change, with the reason it was given. The Project's
// Deployments are listed newest first and only for their Owner.
func testRedeployAndStop(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.AddOwner(ctx, "carol"))
	must(t, store.AddOwner(ctx, "dana"))
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "p9", OwnerID: "carol"}))
	snapshot := ReadySnapshot(t, store, "carol", "p9", "nine-snapshot", "Rust")
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "desk", OwnerID: "carol", Name: "desk", Tag: "tag:acct-carol", KeyID: "key-carol"}))
	must(t, store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "b9", OwnerID: "carol", ProjectID: "p9", SnapshotID: snapshot, Target: "wasm32-unknown-unknown", Status: "complete"}))

	must(t, store.CreateDeployment(ctx, app.CreateDeployment{ID: "r1", OwnerID: "carol", ProjectID: "p9", BuildID: "b9", AgentID: "desk"}))
	must(t, store.TransitionDeployment(ctx, app.DeploymentTransition{ID: "r1", From: "queued", To: "starting"}))
	superseded, err := store.PromoteDeployment(ctx, app.PromoteDeployment{ID: "r1", OwnerID: "carol", ProjectID: "p9"})
	must(t, err)
	if len(superseded) != 0 {
		t.Fatalf("the first Deployment superseded %v", superseded)
	}

	must(t, store.CreateDeployment(ctx, app.CreateDeployment{ID: "r2", OwnerID: "carol", ProjectID: "p9", BuildID: "b9", AgentID: "desk"}))
	must(t, store.TransitionDeployment(ctx, app.DeploymentTransition{ID: "r2", From: "queued", To: "starting"}))
	if _, err = store.PromoteDeployment(ctx, app.PromoteDeployment{ID: "r1", OwnerID: "carol", ProjectID: "p9"}); err == nil {
		t.Fatal("a healthy Deployment was promoted again")
	}
	if _, err = store.PromoteDeployment(ctx, app.PromoteDeployment{ID: "r2", OwnerID: "dana", ProjectID: "p9"}); err == nil {
		t.Fatal("another Account promoted the Deployment")
	}
	superseded, err = store.PromoteDeployment(ctx, app.PromoteDeployment{ID: "r2", OwnerID: "carol", ProjectID: "p9", Supersedes: "replaced"})
	must(t, err)
	if len(superseded) != 1 || superseded[0].ID != "r1" || superseded[0].AgentID != "desk" {
		t.Fatalf("superseded = %+v", superseded)
	}
	old, _, err := store.FindDeployment(ctx, "r1", "carol")
	must(t, err)
	if old.State != "stopped" || old.Reason != "replaced" {
		t.Fatalf("replaced Deployment = %+v", old)
	}
	current, _, err := store.FindDeployment(ctx, "r2", "carol")
	must(t, err)
	if current.State != "healthy" {
		t.Fatalf("new Deployment = %+v", current)
	}

	listed, err := store.ListDeployments(ctx, "p9", "carol")
	must(t, err)
	if len(listed) != 2 || listed[0].ID != "r2" || listed[1].ID != "r1" {
		t.Fatalf("listed Deployments = %+v", listed)
	}
	if others, _ := store.ListDeployments(ctx, "p9", "dana"); len(others) != 0 {
		t.Fatalf("another Account listed the Deployments: %+v", others)
	}

	must(t, store.TransitionDeployment(ctx, app.DeploymentTransition{ID: "r2", From: "healthy", To: "stopped", Reason: "removed"}))
	stopped, _, err := store.FindDeployment(ctx, "r2", "carol")
	must(t, err)
	if stopped.State != "stopped" || stopped.Reason != "removed" {
		t.Fatalf("stopped Deployment = %+v", stopped)
	}
}
