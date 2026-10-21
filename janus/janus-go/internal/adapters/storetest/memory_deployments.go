package storetest

import (
	"context"
	"sort"

	"janus/internal/app"
	"janus/internal/domain/deployments"
)

// memCheck is a Check and the Owner of the WASM Build it checked.
type memCheck struct {
	app.Check
	ownerID string
}

// memDeployment is a Deployment and the Owner and Project it belongs to.
type memDeployment struct {
	app.Deployment
	ownerID string
	order   int
}

// inFlightMessage is the error CreateDeployment gives while another Deployment
// of the Project is queued or starting. The SQL store gives the same words.
const inFlightMessage = "a Deployment is already in flight for this Project"

func (m *Memory) StartCheck(_ context.Context, check app.StartCheck) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[check.BuildID]
	if !ok || build.Status != "complete" {
		return fail("WASM build not found")
	}
	if _, exists := m.checks[check.BuildID]; exists {
		return constraint("UNIQUE constraint failed: project_checks.build_id")
	}
	m.checks[check.BuildID] = &memCheck{ownerID: build.ownerID, Check: app.Check{BuildID: check.BuildID, Status: app.CheckRunning}}
	return nil
}

func (m *Memory) FinishCheck(_ context.Context, check app.FinishCheck) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.checks[check.BuildID]
	if !ok || stored.Status != app.CheckRunning {
		return fail("check not found")
	}
	if check.Status != app.CheckPassed && check.Status != app.CheckFailed {
		return fail("check cannot finish as %s", check.Status)
	}
	stored.Status, stored.Error, stored.Log, stored.NoTests = check.Status, check.Error, check.Log, check.NoTests
	return nil
}

func (m *Memory) FindCheck(_ context.Context, buildID, ownerID string) (app.Check, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.checks[buildID]
	if !ok || stored.ownerID != ownerID {
		return app.Check{}, false, nil
	}
	return stored.Check, true, nil
}

func (m *Memory) CreateDeployment(_ context.Context, deployment app.CreateDeployment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[deployment.BuildID]
	if !ok || build.ownerID != deployment.OwnerID || build.ProjectID != deployment.ProjectID {
		return fail("WASM build not found")
	}
	agent, ok := m.agents[deployment.AgentID]
	if !ok || agent.OwnerID != deployment.OwnerID {
		return fail("Agent not found")
	}
	if _, exists := m.deployments[deployment.ID]; exists {
		return constraint("UNIQUE constraint failed: deployments.id")
	}
	for _, other := range m.deployments {
		if other.ProjectID == deployment.ProjectID && deployments.InFlight(deployments.State(other.State)) {
			return fail("%s", inFlightMessage)
		}
	}
	now := m.now()
	m.deploySeq++
	m.deployments[deployment.ID] = &memDeployment{ownerID: deployment.OwnerID, order: m.deploySeq, Deployment: app.Deployment{ID: deployment.ID,
		ProjectID: deployment.ProjectID, BuildID: deployment.BuildID, AgentID: deployment.AgentID, State: string(deployments.Queued),
		DeployAnyway: deployment.DeployAnyway, CreatedAt: now, UpdatedAt: now}}
	return nil
}

func (m *Memory) TransitionDeployment(_ context.Context, t app.DeploymentTransition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.deployments[t.ID]
	if !ok {
		return fail("deployment not found")
	}
	if !deployments.CanMove(deployments.State(t.From), deployments.State(t.To)) {
		return fail("deployment cannot move from %s to %s", t.From, t.To)
	}
	if stored.State != t.From {
		return fail("deployment changed while it was being updated")
	}
	stored.State, stored.Error, stored.Reason, stored.UpdatedAt = t.To, t.Error, t.Reason, m.now()
	return nil
}

func (m *Memory) FindDeployment(_ context.Context, id, ownerID string) (app.Deployment, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.deployments[id]
	if !ok || stored.ownerID != ownerID {
		return app.Deployment{}, false, nil
	}
	return stored.Deployment, true, nil
}

func (m *Memory) PromoteDeployment(_ context.Context, p app.PromoteDeployment) ([]app.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.deployments[p.ID]
	if !ok || stored.ownerID != p.OwnerID || stored.ProjectID != p.ProjectID {
		return nil, fail("deployment not found")
	}
	if !deployments.CanMove(deployments.State(stored.State), deployments.Healthy) {
		return nil, fail("deployment cannot move from %s to %s", stored.State, deployments.Healthy)
	}
	if stored.State != string(deployments.Starting) {
		return nil, fail("deployment changed while it was being updated")
	}
	now := m.now()
	stored.State, stored.UpdatedAt = string(deployments.Healthy), now
	var superseded []app.Deployment
	for _, other := range m.deployments {
		if other.ID == p.ID || other.ownerID != p.OwnerID || other.ProjectID != p.ProjectID || other.State != string(deployments.Healthy) {
			continue
		}
		other.State, other.Reason, other.UpdatedAt = string(deployments.Stopped), p.Supersedes, now
		superseded = append(superseded, other.Deployment)
	}
	return superseded, nil
}

func (m *Memory) ListDeployments(_ context.Context, projectID, ownerID string) ([]app.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []*memDeployment
	for _, stored := range m.deployments {
		if stored.ProjectID == projectID && stored.ownerID == ownerID {
			matched = append(matched, stored)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].order > matched[j].order })
	listed := make([]app.Deployment, 0, len(matched))
	for _, stored := range matched {
		listed = append(listed, stored.Deployment)
	}
	return listed, nil
}
