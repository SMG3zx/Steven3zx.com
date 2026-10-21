package storetest

import (
	"context"

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
	m.deployments[deployment.ID] = &memDeployment{ownerID: deployment.OwnerID, Deployment: app.Deployment{ID: deployment.ID,
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
