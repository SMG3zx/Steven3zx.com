package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"janus/internal/adapters/audittest"
	"janus/internal/adapters/buildertest"
	"janus/internal/adapters/checktest"
	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

// fakeDeployer stands in for Fleet. It returns err, and when gate is set it
// waits for the gate to close first, so a test can hold a Deployment in flight.
type fakeDeployer struct {
	mu       sync.Mutex
	err      error
	notReady error
	panics   bool
	gate     chan struct{}
	entered  chan struct{}
	calls    []string
	stopErr  error
	stops    []string
}

func (d *fakeDeployer) Ready(_ context.Context, _, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.notReady
}

func (d *fakeDeployer) Deploy(_ context.Context, owner, agent, deployment, build string) error {
	d.mu.Lock()
	gate := d.gate
	d.calls = append(d.calls, owner+"/"+agent+"/"+deployment+"/"+build)
	d.mu.Unlock()
	if d.entered != nil {
		d.entered <- struct{}{}
	}
	if gate != nil {
		<-gate
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.panics {
		panic("Agent hand-off broke")
	}
	return d.err
}

func (d *fakeDeployer) Stop(_ context.Context, owner, agent, deployment string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopErr != nil {
		return d.stopErr
	}
	d.stops = append(d.stops, owner+"/"+agent+"/"+deployment)
	return nil
}

func (d *fakeDeployer) setStopErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopErr = err
}

func (d *fakeDeployer) setErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.err = err
}

type deployFixture struct {
	t           *testing.T
	store       app.Store
	audit       *audittest.Recorder
	checks      *checktest.Fake
	deployer    *fakeDeployer
	deployments *app.Deployments
	build       string
}

// newDeployFixture makes owner's Rust project with a completed WASM build and
// the owner's Agent, ready to deploy. The build's Check runs on the checker.
func newDeployFixture(t *testing.T) *deployFixture {
	t.Helper()
	ctx := context.Background()
	store := storetest.NewMemory()
	importedProject(t, store, "owner", "project", "Rust")
	audit := audittest.NewRecorder()
	checks := checktest.NewFake()
	builds := app.NewBuilds(store, buildertest.NewFake(), checks, audit)
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "agent", OwnerID: "owner", Name: "laptop", Tag: "tag:acct-owner", KeyID: "key"}))
	build, err := builds.Request(ctx, app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget})
	must(t, err)
	waitBuilds(t, builds)
	deployer := &fakeDeployer{}
	return &deployFixture{t: t, store: store, audit: audit, checks: checks, deployer: deployer,
		deployments: app.NewDeployments(store, deployer, audit), build: build}
}

func waitBuilds(t *testing.T, builds *app.Builds) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	must(t, builds.Wait(ctx))
}

func (f *deployFixture) wait() {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	must(f.t, f.deployments.Wait(ctx))
}

func (f *deployFixture) request(anyway bool) (string, error) {
	return f.deployments.Create(context.Background(), app.NewDeployment{OwnerID: "owner", ProjectID: "project", BuildID: f.build, AgentID: "agent", DeployAnyway: anyway})
}

func (f *deployFixture) deployment(id string) app.Deployment {
	f.t.Helper()
	deployment, found, err := f.deployments.Find(context.Background(), "owner", id)
	must(f.t, err)
	if !found {
		f.t.Fatalf("deployment %s not found", id)
	}
	return deployment
}

func TestDeployQueuesAPassedBuildAndEndsHealthy(t *testing.T) {
	f := newDeployFixture(t)

	id, err := f.request(false)
	must(t, err)
	f.wait()

	deployment := f.deployment(id)
	if deployment.State != "healthy" || deployment.BuildID != f.build || deployment.AgentID != "agent" || deployment.DeployAnyway {
		t.Fatalf("deployment = %+v", deployment)
	}
	if len(f.deployer.calls) != 1 || f.deployer.calls[0] != "owner/agent/"+id+"/"+f.build {
		t.Fatalf("deployer calls = %v", f.deployer.calls)
	}
	events := f.audit.Events()
	if len(events) < 2 || events[len(events)-2].Name != "project.deployment.queued" || events[len(events)-1].Name != "project.deployment.healthy" {
		t.Fatalf("audit events = %+v", events)
	}
	queued := events[len(events)-2].Fields
	if len(queued) != 6 || queued["deployment_id"] != id || queued["build_id"] != f.build || queued["agent_id"] != "agent" || queued["deploy_anyway"] != "false" {
		t.Fatalf("queued audit fields = %v", queued)
	}
}

func TestDeployRefusesFailingChecksUnlessDeployAnyway(t *testing.T) {
	f := newDeployFixture(t)
	// The fixture's build passed its Check; a second build of the Project gets a failing one.
	f.checks.Answer(app.CheckResult{Passed: false, Log: []byte("test failed\n")})
	builds := app.NewBuilds(f.store, buildertest.NewFake(), f.checks, f.audit)
	build, err := builds.Request(context.Background(), app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget})
	must(t, err)
	waitBuilds(t, builds)
	f.build = build

	if _, err := f.request(false); err == nil || !strings.Contains(err.Error(), "checks failed") {
		t.Fatalf("deploy with failing checks: error = %v, want a refusal", err)
	}
	id, err := f.request(true)
	must(t, err)
	f.wait()
	if deployment := f.deployment(id); !deployment.DeployAnyway || deployment.State != "healthy" {
		t.Fatalf("deploy anyway = %+v", deployment)
	}
}

func TestDeployKeepsTheCheckErrorAndAllowsDeployAnyway(t *testing.T) {
	f := newDeployFixture(t)
	f.checks.Break(errors.New("container crashed"))
	builds := app.NewBuilds(f.store, buildertest.NewFake(), f.checks, f.audit)
	build, err := builds.Request(context.Background(), app.NewBuild{OwnerID: "owner", ProjectID: "project", Target: rustTarget})
	must(t, err)
	waitBuilds(t, builds)

	check, found, err := f.store.FindCheck(context.Background(), build, "owner")
	must(t, err)
	if !found || check.Status != app.CheckFailed || !strings.Contains(check.Error, "container crashed") {
		t.Fatalf("check = %+v, found %v", check, found)
	}
	f.build = build
	if _, err := f.request(false); err == nil {
		t.Fatal("a Check that errored let a Deploy through without deploy anyway")
	}
}

func TestOnlyOneDeploymentOfAProjectIsInFlight(t *testing.T) {
	f := newDeployFixture(t)
	f.deployer.gate = make(chan struct{})

	first, err := f.request(false)
	must(t, err)
	if _, err := f.request(false); err == nil || !strings.Contains(err.Error(), "already in flight") {
		t.Fatalf("second Deploy error = %v, want an in-flight refusal", err)
	}
	close(f.deployer.gate)
	f.wait()
	if deployment := f.deployment(first); deployment.State != "healthy" {
		t.Fatalf("first deployment = %+v", deployment)
	}
	if _, err := f.request(false); err != nil {
		t.Fatalf("Deploy after the first is healthy: %v", err)
	}
	f.wait()
}

func TestDeployFailureKeepsItsError(t *testing.T) {
	f := newDeployFixture(t)
	f.deployer.setErr(errors.New("Agent refused the module hash"))

	id, err := f.request(false)
	must(t, err)
	f.wait()

	deployment := f.deployment(id)
	if deployment.State != "failed" || !strings.Contains(deployment.Error, "Agent refused the module hash") {
		t.Fatalf("deployment = %+v", deployment)
	}
}

func TestDeployRefusesAnAgentThatCannotTakeItUpFront(t *testing.T) {
	f := newDeployFixture(t)
	f.deployer.notReady = errors.New("the Agent is offline, so it cannot take a Deploy")

	if _, err := f.request(false); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline Agent error = %v, want an up-front refusal", err)
	}
	if len(f.deployer.calls) != 0 {
		t.Fatalf("an offline Agent was handed a Deployment: %v", f.deployer.calls)
	}
}

func TestDeployPanicEndsTheDeploymentAsFailed(t *testing.T) {
	f := newDeployFixture(t)
	f.deployer.panics = true

	id, err := f.request(false)
	must(t, err)
	f.wait()

	deployment := f.deployment(id)
	if deployment.State != "failed" || !strings.Contains(deployment.Error, "Agent hand-off broke") {
		t.Fatalf("deployment after a panic = %+v", deployment)
	}
}

func TestDeployIsRefusedForAnotherOwnersBuildOrAgent(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()

	if _, err := f.deployments.Create(ctx, app.NewDeployment{OwnerID: "intruder", ProjectID: "project", BuildID: f.build, AgentID: "agent"}); err == nil {
		t.Fatal("another Account deployed the Owner's build")
	}
	if _, err := f.deployments.Create(ctx, app.NewDeployment{OwnerID: "owner", ProjectID: "project", BuildID: f.build, AgentID: "missing"}); err == nil || !strings.Contains(err.Error(), "Agent not found") {
		t.Fatalf("unknown Agent error = %v", err)
	}
	id, err := f.request(false)
	must(t, err)
	f.wait()
	if _, found, err := f.deployments.Find(ctx, "intruder", id); err != nil || found {
		t.Fatalf("another Account read the Deployment: found %v, err %v", found, err)
	}
}
