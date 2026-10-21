package fleet_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"janus/cmd/janus/fleetfixture"
	"janus/internal/adapters/localstatetest"
	"janus/internal/adapters/runnertest"
	"janus/internal/app"
	contractbuilds "janus/internal/contract/builds"
	contractprojects "janus/internal/contract/projects"
)

// pageModule is a WebAssembly module that answers the probe every Deployment
// gets: a GET / that returns a plain text body.
var pageModule = []byte("\x00asm\x01\x00\x00\x00page")

// brokenModule is a module the test Agent has no program for, so its probe fails.
var brokenModule = []byte("\x00asm\x01\x00\x00\x00broken")

const wasiTarget = "wasm32-wasip1"

// clock is a test clock that moves only when a test moves it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// agentRuntime is an Agent that serves pageModule and nothing else.
func agentRuntime() *app.AgentRuntime {
	runner := runnertest.NewFake()
	runner.Handle(pageModule, func(app.RunInput) (app.RunResult, error) {
		return app.RunResult{Stdout: []byte("Content-Type: text/plain\n\nok")}, nil
	})
	return app.NewAgentRuntime(runner, localstatetest.NewMemory(), "", time.Now)
}

// setup starts the fleet service with one Agent of owner, named laptop, joined
// to the tailnet and connected to the service.
type setup struct {
	*fleetfixture.Fixture
	t       testing.TB
	owner   string
	agent   app.Enrollment
	address string
	runtime *app.AgentRuntime
}

func newSetup(t *testing.T) *setup {
	t.Helper()
	f := fleetfixture.Start(t, fleetfixture.Options{})
	enrollment := f.Enroll("owner", "laptop")
	runtime := agentRuntime()
	address := f.Attach("owner", enrollment, "laptop", runtime)
	return &setup{Fixture: f, t: t, owner: "owner", agent: enrollment, address: address, runtime: runtime}
}

// build records a complete WASI build of the owner's project, with a check of
// checkStatus (empty for a Build with no Check), and returns its ID.
func (s *setup) build(id, checkStatus string, module []byte) string {
	s.t.Helper()
	build := contractbuilds.Build{ID: id, ProjectID: "project", SnapshotID: "snapshot-" + id, Target: wasiTarget, Status: contractbuilds.StatusComplete}
	var check *contractbuilds.Check
	if checkStatus != "" {
		check = &contractbuilds.Check{BuildID: id, Status: checkStatus}
	}
	s.Builds.Add(s.owner, build, check, module)
	return id
}

// deploy queues a Deployment of build on the owner's Agent and waits until it is
// healthy.
func (s *setup) deploy(build string) string {
	s.t.Helper()
	id := s.request(build, false)
	s.waitState(id, "healthy")
	return id
}

func (s *setup) request(build string, anyway bool) string {
	s.t.Helper()
	id, err := s.Fleet.CreateDeployment(context.Background(), app.NewDeployment{OwnerID: s.owner, ProjectID: "project",
		BuildID: build, AgentID: s.agent.AgentID, DeployAnyway: anyway})
	if err != nil {
		s.t.Fatalf("deploy %s: %v", build, err)
	}
	return id
}

func (s *setup) waitState(id, state string) {
	s.t.Helper()
	s.Eventually("deployment "+id+" to be "+state, func() bool {
		found, ok, err := s.Fleet.FindDeployment(context.Background(), s.owner, id)
		return err == nil && ok && found.State == state
	})
}

func (s *setup) deployment(id string) app.Deployment {
	s.t.Helper()
	d, found, err := s.Fleet.FindDeployment(context.Background(), s.owner, id)
	if err != nil || !found {
		s.t.Fatalf("deployment %s: found %v, %v", id, found, err)
	}
	return d
}

// hosted lists the Deployments the Agent serves, by ID.
func (s *setup) hosted() []string {
	s.t.Helper()
	reports, err := s.Fleet.AgentStatuses(context.Background(), s.owner)
	if err != nil {
		s.t.Fatal(err)
	}
	var ids []string
	for _, d := range reports[s.agent.AgentID].Deployments {
		ids = append(ids, d.ID)
	}
	return ids
}

func TestEnrollmentCodesAreSingleUse(t *testing.T) {
	f := fleetfixture.Start(t, fleetfixture.Options{})
	ctx := context.Background()
	code, err := f.Fleet.IssueCode(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Fleet.Enroll(ctx, code.Code, "laptop"); err != nil {
		t.Fatalf("first enrollment: %v", err)
	}
	if _, err := f.Fleet.Enroll(ctx, code.Code, "desktop"); err == nil || !strings.Contains(err.Error(), "invalid or has expired") {
		t.Fatalf("second enrollment with the same code = %v, want the code refused", err)
	}
	if _, err := f.Fleet.Enroll(ctx, "not-a-code", "laptop"); err == nil {
		t.Fatal("an unknown code enrolled an Agent")
	}
}

func TestEnrollmentCodesExpireAfterFifteenMinutes(t *testing.T) {
	clk := &clock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	f := fleetfixture.Start(t, fleetfixture.Options{Now: clk.Now})
	ctx := context.Background()
	code, err := f.Fleet.IssueCode(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(16 * time.Minute)
	if _, err := f.Fleet.Enroll(ctx, code.Code, "laptop"); err == nil {
		t.Fatal("an expired code enrolled an Agent")
	}
}

func TestEnrollmentNeedsANameOfOneToSixtyFourCharacters(t *testing.T) {
	f := fleetfixture.Start(t, fleetfixture.Options{})
	ctx := context.Background()
	code, err := f.Fleet.IssueCode(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Fleet.Enroll(ctx, code.Code, "   "); err == nil {
		t.Fatal("a blank name enrolled an Agent")
	}
	if _, err := f.Fleet.Enroll(ctx, code.Code, strings.Repeat("n", 65)); err == nil {
		t.Fatal("a 65 character name enrolled an Agent")
	}
	if _, err := f.Fleet.Enroll(ctx, code.Code, "laptop"); err != nil {
		t.Fatalf("a valid name after refused ones: %v", err)
	}
}

func TestAgentsAreOnlyTheOwnersAndListTheirTailnetStatus(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	pending := s.Enroll("owner", "desktop")

	agents, err := s.Fleet.ListAgents(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, agent := range agents {
		statuses[agent.ID] = agent.Status
	}
	if len(agents) != 2 || statuses[s.agent.AgentID] != "online" || statuses[pending.AgentID] != "pending" {
		t.Fatalf("the owner's Agents = %+v", agents)
	}
	if others, err := s.Fleet.ListAgents(ctx, "stranger"); err != nil || len(others) != 0 {
		t.Fatalf("a stranger's Agents = %+v, %v", others, err)
	}
	if removed, err := s.Fleet.RemoveAgent(ctx, "stranger", s.agent.AgentID); err != nil || removed {
		t.Fatalf("a stranger removed the owner's Agent: %v, %v", removed, err)
	}
	if _, err := s.Fleet.Usage(ctx, app.RunUsageQuery{OwnerID: "stranger", AgentID: s.agent.AgentID}); !errors.Is(err, app.ErrAgentNotFound) {
		t.Fatalf("a stranger's usage of the owner's Agent = %v, want ErrAgentNotFound", err)
	}
}

func TestRemovingAnAgentTakesItsNodeOffTheTailnet(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	removed, err := s.Fleet.RemoveAgent(ctx, s.owner, s.agent.AgentID)
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	nodes, err := s.Tailnet.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if node.Name == "laptop" {
			t.Fatalf("the Agent's node is still on the tailnet: %+v", node)
		}
	}
	if again, err := s.Fleet.RemoveAgent(ctx, s.owner, s.agent.AgentID); err != nil || again {
		t.Fatalf("removing the same Agent again = %v, %v, want false", again, err)
	}
}

func TestRevokingAndDeletingAnOwnerLeavesNothingBehind(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	s.Enroll(s.owner, "desktop")
	code, err := s.Fleet.IssueCode(ctx, s.owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Fleet.RevokeAgents(ctx, s.owner); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.Tailnet.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Fatalf("nodes left on the tailnet after revoke: %+v", nodes)
	}
	if err := s.Fleet.TeardownDeployments(ctx, s.owner); err != nil {
		t.Fatal(err)
	}
	if err := s.Fleet.DeleteOwner(ctx, s.owner); err != nil {
		t.Fatal(err)
	}
	if err := s.Fleet.DeleteOwner(ctx, s.owner); err != nil {
		t.Fatalf("deleting an Owner twice: %v", err)
	}
	if agents, _ := s.Fleet.ListAgents(ctx, s.owner); len(agents) != 0 {
		t.Fatalf("Agents after the Owner was deleted: %+v", agents)
	}
	if _, err := s.Fleet.Enroll(ctx, code.Code, "laptop"); err == nil {
		t.Fatal("an enrollment code survived its Owner")
	}
}

func TestAHealthyDeploymentIsRecordedAndItsProbeIsUsage(t *testing.T) {
	s := newSetup(t)
	id := s.deploy(s.build("b1", "passed", pageModule))

	records, err := s.Fleet.Usage(context.Background(), app.RunUsageQuery{OwnerID: s.owner, AgentID: s.agent.AgentID})
	if err != nil {
		t.Fatal(err)
	}
	var probe *app.RunUsageRecord
	for i := range records {
		if records[i].DeploymentID == id && records[i].Probe {
			probe = &records[i]
		}
	}
	if probe == nil || probe.Status != "ok" || probe.OwnerID != s.owner {
		t.Fatalf("usage of the deployment = %+v, want its probe", records)
	}
	if !s.HasAudit("project.deployment.healthy") || !s.HasAudit("project.deployment.queued") {
		t.Fatalf("audit events = %+v", s.AuditEvents())
	}
}

func TestDeployRefusesABuildWhoseChecksAreNotReady(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	cases := []struct {
		name, build, check, want string
		anyway                   bool
	}{
		{"no check yet", s.build("none", "", pageModule), "", "checks have not finished", false},
		{"checks running", s.build("running", "running", pageModule), "", "checks are still running", false},
		{"checks failed", s.build("failed", "failed", pageModule), "", "deploy anyway", false},
	}
	for _, tc := range cases {
		_, err := s.Fleet.CreateDeployment(ctx, app.NewDeployment{OwnerID: s.owner, ProjectID: "project", BuildID: tc.build, AgentID: s.agent.AgentID, DeployAnyway: tc.anyway})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: deploy = %v, want a refusal mentioning %q", tc.name, err, tc.want)
		}
	}
	id := s.request("failed", true)
	s.waitState(id, "healthy")
	if d := s.deployment(id); !d.DeployAnyway {
		t.Fatalf("a deploy anyway was not recorded: %+v", d)
	}
}

func TestDeployNeedsAConnectedUnpausedAgent(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	build := s.build("b1", "passed", pageModule)
	offline := s.Enroll(s.owner, "offline")
	if _, err := s.Fleet.CreateDeployment(ctx, app.NewDeployment{OwnerID: s.owner, ProjectID: "project", BuildID: build, AgentID: offline.AgentID}); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("deploy to an offline Agent = %v", err)
	}
	if err := s.Fleet.PauseAgent(ctx, s.owner, s.agent.AgentID); err != nil {
		t.Fatal(err)
	}
	s.Eventually("the Agent to report it is paused", func() bool {
		reports, err := s.Fleet.AgentStatuses(ctx, s.owner)
		return err == nil && reports[s.agent.AgentID].Paused
	})
	if _, err := s.Fleet.CreateDeployment(ctx, app.NewDeployment{OwnerID: s.owner, ProjectID: "project", BuildID: build, AgentID: s.agent.AgentID}); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("deploy to a paused Agent = %v", err)
	}
	if err := s.Fleet.ResumeAgent(ctx, s.owner, s.agent.AgentID); err != nil {
		t.Fatal(err)
	}
}

func TestARedeployReplacesTheLiveDeploymentOnceItIsHealthy(t *testing.T) {
	s := newSetup(t)
	first := s.deploy(s.build("b1", "passed", pageModule))
	second := s.deploy(s.build("b2", "passed", pageModule))

	if old := s.deployment(first); old.State != "stopped" || old.Reason != "replaced" {
		t.Fatalf("the replaced deployment = %+v", old)
	}
	if hosted := s.hosted(); len(hosted) != 1 || hosted[0] != second {
		t.Fatalf("the Agent serves %v, want only %s", hosted, second)
	}
	if !s.HasAudit("project.deployment.redeployed") {
		t.Fatalf("no redeploy audit event: %+v", s.AuditEvents())
	}
}

func TestAFailedRedeployLeavesTheLiveDeploymentServing(t *testing.T) {
	s := newSetup(t)
	first := s.deploy(s.build("b1", "passed", pageModule))
	broken := s.request(s.build("b2", "passed", brokenModule), false)
	s.waitState(broken, "failed")

	if live := s.deployment(first); live.State != "healthy" || live.Reason != "" {
		t.Fatalf("the live deployment changed: %+v", live)
	}
	if hosted := s.hosted(); len(hosted) != 1 || hosted[0] != first {
		t.Fatalf("the Agent serves %v after a failed redeploy", hosted)
	}
}

func TestRollbackRedeploysAnEarlierDeployment(t *testing.T) {
	s := newSetup(t)
	first := s.deploy(s.build("b1", "passed", pageModule))
	second := s.deploy(s.build("b2", "passed", pageModule))
	ctx := context.Background()

	queued, err := s.Fleet.RollbackDeployment(ctx, s.owner, first)
	if err != nil {
		t.Fatal(err)
	}
	s.waitState(queued, "healthy")
	if got := s.deployment(queued); got.BuildID != "b1" {
		t.Fatalf("the rollback deployed %+v", got)
	}
	if old := s.deployment(second); old.State != "stopped" || old.Reason != "rolled back" {
		t.Fatalf("the rolled back deployment = %+v", old)
	}
	if !s.HasAudit("project.deployment.rolled_back") {
		t.Fatalf("no rollback audit event: %+v", s.AuditEvents())
	}
}

func TestRollbackRefusesALiveDeploymentAndReachesOnlyTheLastFive(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 7; i++ {
		ids = append(ids, s.deploy(s.build("b"+string(rune('a'+i)), "passed", pageModule)))
	}
	live := ids[len(ids)-1]
	if _, err := s.Fleet.RollbackDeployment(ctx, s.owner, live); err == nil {
		t.Fatal("rolled back to the live deployment")
	}
	if _, err := s.Fleet.RollbackDeployment(ctx, s.owner, ids[0]); err == nil || !strings.Contains(err.Error(), "last five") {
		t.Fatalf("rollback past the last five = %v", err)
	}
	if _, err := s.Fleet.RollbackDeployment(ctx, s.owner, ids[2]); err != nil {
		t.Fatalf("rollback to the oldest kept deployment: %v", err)
	}
}

func TestStopEndsAHealthyDeploymentOnItsAgent(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	id := s.deploy(s.build("b1", "passed", pageModule))

	if err := s.Fleet.StopDeployment(ctx, s.owner, id); err != nil {
		t.Fatal(err)
	}
	if got := s.deployment(id); got.State != "stopped" || got.Reason != "removed" {
		t.Fatalf("stopped deployment = %+v", got)
	}
	if hosted := s.hosted(); len(hosted) != 0 {
		t.Fatalf("the Agent still serves %v", hosted)
	}
	if err := s.Fleet.StopDeployment(ctx, s.owner, id); err == nil {
		t.Fatal("a stopped deployment was stopped again")
	}
}

func TestStopKeepsTheDeploymentWhenTheAgentCannotBeReached(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	id := s.deploy(s.build("b1", "passed", pageModule))
	s.Link.Unserve(s.address)

	if err := s.Fleet.StopDeployment(ctx, s.owner, id); err == nil {
		t.Fatal("a deployment was stopped on an Agent that could not be reached")
	}
	if got := s.deployment(id); got.State != "healthy" {
		t.Fatalf("the deployment changed although its Agent was not told: %+v", got)
	}
}

func TestDeploymentsBelongToTheirAccount(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	id := s.deploy(s.build("b1", "passed", pageModule))
	if _, found, err := s.Fleet.FindDeployment(ctx, "stranger", id); err != nil || found {
		t.Fatalf("a stranger found the deployment: %v, %v", found, err)
	}
	if err := s.Fleet.StopDeployment(ctx, "stranger", id); err == nil {
		t.Fatal("a stranger stopped the deployment")
	}
}

func TestTeardownStopsEveryRunningDeployment(t *testing.T) {
	s := newSetup(t)
	ctx := context.Background()
	id := s.deploy(s.build("b1", "passed", pageModule))
	if err := s.Fleet.TeardownDeployments(ctx, s.owner); err != nil {
		t.Fatal(err)
	}
	if got := s.deployment(id); got.State != "stopped" || got.Reason != "account deleted" {
		t.Fatalf("torn down deployment = %+v", got)
	}
	if hosted := s.hosted(); len(hosted) != 0 {
		t.Fatalf("the Agent still serves %v after teardown", hosted)
	}
}

func TestAProjectDeletedRemovesItsDeployments(t *testing.T) {
	s := newSetup(t)
	id := s.deploy(s.build("b1", "passed", pageModule))
	s.Publish(contractprojects.SubjectProjectDeleted, contractprojects.ProjectDeletedEvent{ProjectID: "project", OwnerID: s.owner})

	s.Eventually("the deleted project's deployment to go", func() bool {
		_, found, err := s.Fleet.FindDeployment(context.Background(), s.owner, id)
		return err == nil && !found
	})
	if hosted := s.hosted(); len(hosted) != 0 {
		t.Fatalf("the Agent still serves %v for a deleted project", hosted)
	}
}

func TestASnapshotDeletedRemovesTheDeploymentsOfItsBuild(t *testing.T) {
	s := newSetup(t)
	id := s.deploy(s.build("b1", "passed", pageModule))
	s.Publish(contractprojects.SubjectSnapshotDeleted, contractprojects.SnapshotDeletedEvent{SnapshotID: "snapshot-b1", ProjectID: "project", OwnerID: s.owner})

	s.Eventually("the deleted snapshot's deployment to go", func() bool {
		_, found, err := s.Fleet.FindDeployment(context.Background(), s.owner, id)
		return err == nil && !found
	})
}
