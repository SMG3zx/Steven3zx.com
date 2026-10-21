package app_test

import (
	"context"
	"errors"
	"testing"

	"janus/internal/app"
)

// deployHealthy deploys the fixture's build and waits until it is healthy,
// replacing whatever was live.
func (f *deployFixture) deployHealthy() string {
	f.t.Helper()
	id, err := f.request(false)
	must(f.t, err)
	f.wait()
	if f.deployment(id).State != "healthy" {
		f.t.Fatalf("deployment %s did not become healthy: %+v", id, f.deployment(id))
	}
	return id
}

// lastEvent returns the fields of the most recent audit event with name.
func (f *deployFixture) lastEvent(name string) map[string]string {
	f.t.Helper()
	events := f.audit.Events()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Name == name {
			return events[i].Fields
		}
	}
	f.t.Fatalf("no %s audit event in %+v", name, events)
	return nil
}

func TestRedeployReplacesTheLiveDeploymentOnceItIsHealthy(t *testing.T) {
	f := newDeployFixture(t)
	first := f.deployHealthy()

	second, err := f.request(false)
	must(t, err)
	f.wait()

	if got := f.deployment(second); got.State != "healthy" {
		t.Fatalf("redeploy = %+v", got)
	}
	if old := f.deployment(first); old.State != "stopped" || old.Reason != "replaced" {
		t.Fatalf("replaced deployment = %+v", old)
	}
	if len(f.deployer.stops) != 1 || f.deployer.stops[0] != "owner/agent/"+first {
		t.Fatalf("the replaced deployment was not stopped on its Agent: %v", f.deployer.stops)
	}
	if fields := f.lastEvent("project.deployment.redeployed"); fields["replaces"] != first || fields["deployment_id"] != second {
		t.Fatalf("redeploy audit fields = %v", fields)
	}
}

func TestAFailedRedeployLeavesTheLiveDeploymentServing(t *testing.T) {
	f := newDeployFixture(t)
	first := f.deployHealthy()
	f.deployer.setErr(errors.New("the module does not match its SHA-256"))

	second, err := f.request(false)
	must(t, err)
	f.wait()

	if got := f.deployment(second); got.State != "failed" {
		t.Fatalf("failed redeploy = %+v", got)
	}
	if live := f.deployment(first); live.State != "healthy" || live.Reason != "" {
		t.Fatalf("the live deployment changed: %+v", live)
	}
	if len(f.deployer.stops) != 0 {
		t.Fatalf("a failed redeploy stopped %v", f.deployer.stops)
	}
}

func TestRollbackRedeploysAnEarlierDeployment(t *testing.T) {
	f := newDeployFixture(t)
	first := f.deployHealthy()
	second := f.deployHealthy()

	rolled, err := f.deployments.Rollback(context.Background(), "owner", first)
	must(t, err)
	f.wait()

	if got := f.deployment(rolled); got.State != "healthy" || got.BuildID != f.build {
		t.Fatalf("rollback = %+v", got)
	}
	if old := f.deployment(second); old.State != "stopped" || old.Reason != "rolled back" {
		t.Fatalf("rolled back deployment = %+v", old)
	}
	if fields := f.lastEvent("project.deployment.rolled_back"); fields["rolled_back_to"] != first || fields["deployment_id"] != rolled {
		t.Fatalf("rollback audit fields = %v", fields)
	}
}

func TestRollbackRefusesADeploymentThatIsNotStopped(t *testing.T) {
	f := newDeployFixture(t)
	live := f.deployHealthy()

	if _, err := f.deployments.Rollback(context.Background(), "owner", live); err == nil {
		t.Fatal("rolled back to the live deployment")
	}
}

func TestRollbackReachesOnlyTheLastFiveStoppedDeployments(t *testing.T) {
	f := newDeployFixture(t)
	var ids []string
	for range 7 {
		ids = append(ids, f.deployHealthy())
	}

	if _, err := f.deployments.Rollback(context.Background(), "owner", ids[0]); err == nil {
		t.Fatal("rolled back past the last five stopped deployments")
	}
	if _, err := f.deployments.Rollback(context.Background(), "owner", ids[1]); err != nil {
		t.Fatalf("rollback to the oldest kept deployment: %v", err)
	}
}

func TestStopEndsAHealthyDeploymentOnItsAgent(t *testing.T) {
	f := newDeployFixture(t)
	id := f.deployHealthy()

	must(t, f.deployments.Stop(context.Background(), "owner", id))

	if got := f.deployment(id); got.State != "stopped" || got.Reason != "removed" {
		t.Fatalf("stopped deployment = %+v", got)
	}
	if len(f.deployer.stops) != 1 || f.deployer.stops[0] != "owner/agent/"+id {
		t.Fatalf("the Agent was not told to stop: %v", f.deployer.stops)
	}
	if fields := f.lastEvent("project.deployment.stopped"); fields["deployment_id"] != id || fields["reason"] != "removed" {
		t.Fatalf("stop audit fields = %v", fields)
	}
	if err := f.deployments.Stop(context.Background(), "owner", id); err == nil {
		t.Fatal("a stopped deployment was stopped again")
	}
}

func TestStopKeepsTheDeploymentWhenTheAgentCannotBeReached(t *testing.T) {
	f := newDeployFixture(t)
	id := f.deployHealthy()
	f.deployer.setStopErr(errors.New("the Agent is offline"))

	if err := f.deployments.Stop(context.Background(), "owner", id); err == nil {
		t.Fatal("a stop the Agent did not take was reported as done")
	}
	if got := f.deployment(id); got.State != "healthy" {
		t.Fatalf("deployment after a refused stop = %+v", got)
	}
}

func TestTeardownStopsEveryLiveDeploymentOfAnAccount(t *testing.T) {
	f := newDeployFixture(t)
	old := f.deployHealthy()
	live := f.deployHealthy()

	must(t, f.deployments.Teardown(context.Background(), "owner"))

	if got := f.deployment(live); got.State != "stopped" || got.Reason != "account deleted" {
		t.Fatalf("torn down deployment = %+v", got)
	}
	if got := f.deployment(old); got.Reason != "replaced" {
		t.Fatalf("an already stopped deployment changed: %+v", got)
	}
	// The redeploy stopped the old one already, so teardown adds only the live one.
	if len(f.deployer.stops) != 2 || f.deployer.stops[0] != "owner/agent/"+old || f.deployer.stops[1] != "owner/agent/"+live {
		t.Fatalf("Agents stopped = %v", f.deployer.stops)
	}
}

func TestDeletingAnAccountKeepsItWhenItsDeploymentsCannotBeStopped(t *testing.T) {
	f := newDeployFixture(t)
	id := f.deployHealthy()
	f.deployer.setStopErr(errors.New("the Agent is offline"))
	accounts := app.NewAccounts(f.store, users(t, "owner"), f.catalog).WithDeployments(f.deployments)

	if _, err := accounts.Delete(context.Background(), "owner", false); err == nil {
		t.Fatal("an Account was deleted while its deployment still runs")
	}
	if got := f.deployment(id); got.State != "healthy" {
		t.Fatalf("deployment after a refused deletion = %+v", got)
	}
}

func TestStopDuringDeployLeavesNoServingDeployment(t *testing.T) {
	f := newDeployFixture(t)
	f.deployer.gate = make(chan struct{})
	f.deployer.entered = make(chan struct{}, 1)
	id, err := f.request(false)
	must(t, err)
	<-f.deployer.entered

	must(t, f.deployments.Stop(context.Background(), "owner", id))
	close(f.deployer.gate)
	f.wait()

	if got := f.deployment(id); got.State != "stopped" || got.Reason != "removed" {
		t.Fatalf("stopped deployment = %+v", got)
	}
	// The Agent was told to stop by Stop, and again when the deploy finished
	// after the record had already been stopped.
	if len(f.deployer.stops) != 2 {
		t.Fatalf("Agent stops = %v", f.deployer.stops)
	}
	for _, event := range f.audit.Events() {
		if event.Name == "project.deployment.healthy" {
			t.Fatalf("a stopped deployment became healthy: %+v", event)
		}
	}
}

func TestTeardownAcceptsAnAgentThatWasRemoved(t *testing.T) {
	f := newDeployFixture(t)
	id := f.deployHealthy()
	f.deployer.setStopErr(app.ErrAgentNotFound)

	must(t, f.deployments.Teardown(context.Background(), "owner"))

	if got := f.deployment(id); got.State != "stopped" || got.Reason != "account deleted" {
		t.Fatalf("deployment of a removed Agent = %+v", got)
	}
}
