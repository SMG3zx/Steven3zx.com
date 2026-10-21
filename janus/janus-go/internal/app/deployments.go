package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"janus/internal/domain/deployments"
)

// deployTimeout bounds how long one Deployment may take to become healthy.
const deployTimeout = 2 * time.Minute

// rollbackDepth is how many of a Project's most recent stopped Deployments a
// rollback may go back to (GLOSSARY.md, Deployment).
const rollbackDepth = 5

// AgentDeployer is what a Deployment needs from its Agent. Ready says whether
// the Owner's Agent can take a Deploy right now, and returns an error saying why
// it cannot. Deploy hands a completed WASM Build to the Agent as a Deployment,
// and returns nil once the Agent reports it healthy. Stop tells the Agent to
// stop serving a Deployment, and returns an error when the Agent did not take
// it. Fleet is the real one.
type AgentDeployer interface {
	Ready(ctx context.Context, ownerID, agentID string) error
	Deploy(ctx context.Context, ownerID, agentID, deploymentID, buildID string) error
	Stop(ctx context.Context, ownerID, agentID, deploymentID string) error
}

// NewDeployment asks to deploy a WASM Build on one of the Owner's Agents.
type NewDeployment struct {
	OwnerID, ProjectID, BuildID, AgentID string
	// DeployAnyway deploys the Build even though its checks failed.
	DeployAnyway bool
}

// change says what queueing a Deployment does to the Project's live one.
type change struct {
	// replaces is the live Deployment this one replaces, when there is one.
	replaces string
	// reason is what the replaced Deployment is stopped with once this one is
	// healthy.
	reason string
	// event is the audit event that records the queueing.
	event string
	// fields are more audit fields of the queueing.
	fields map[string]string
}

// deployment identifies one Deployment being run, so its records can be written
// without passing the same five strings around.
type deployment struct {
	OwnerID, ProjectID, ID, BuildID, AgentID string
	// reason is what the Deployment stops the Project's healthy Deployments with
	// when it becomes healthy.
	reason string
}

// audit fields that name a Deployment.
func (d deployment) fields() map[string]string {
	return map[string]string{"user_id": d.OwnerID, "project_id": d.ProjectID, "deployment_id": d.ID}
}

// Deployments queues Deployments and runs each one on a goroutine the use case
// owns, until its Agent reports it healthy or refuses it. Each state change is
// recorded in the Store as it happens. A Project has one live Deployment, the
// healthy one; a redeploy or a rollback replaces it once the new one is healthy.
type Deployments struct {
	store    Store
	deployer AgentDeployer
	audit    Audit

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// NewDeployments returns a Deployments that records Deployments in store and
// hands them to deployer.
func NewDeployments(store Store, deployer AgentDeployer, audit Audit) *Deployments {
	return &Deployments{store: store, deployer: deployer, audit: audit}
}

// Create queues a Deployment and starts it, and returns the Deployment's ID. When
// the Project has a live Deployment, this is a redeploy: the live one keeps
// serving until the new one is healthy. It returns a plain error whose text is
// meant for the Account when the Deployment cannot be queued.
func (d *Deployments) Create(ctx context.Context, request NewDeployment) (string, error) {
	live, err := d.live(ctx, request.OwnerID, request.ProjectID)
	if err != nil {
		return "", err
	}
	if live == "" {
		return d.queue(ctx, request, change{event: "project.deployment.queued"})
	}
	return d.queue(ctx, request, change{replaces: live, reason: string(deployments.Replaced), event: "project.deployment.redeployed"})
}

// Rollback redeploys the WASM Build of one of the Account's stopped Deployments,
// one of the last five of its Project, as a new Deployment, and returns its ID.
// The live Deployment keeps serving until the rollback is healthy.
func (d *Deployments) Rollback(ctx context.Context, owner, id string) (string, error) {
	target, found, err := d.store.FindDeployment(ctx, id, owner)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("deployment not found")
	}
	if target.State != string(deployments.Stopped) {
		return "", errors.New("only a stopped deployment can be rolled back to")
	}
	listed, err := d.store.ListDeployments(ctx, target.ProjectID, owner)
	if err != nil {
		return "", err
	}
	if !withinRollbackDepth(listed, target.ID) {
		return "", errors.New("only one of the last five stopped deployments can be rolled back to")
	}
	live, err := d.live(ctx, owner, target.ProjectID)
	if err != nil {
		return "", err
	}
	request := NewDeployment{OwnerID: owner, ProjectID: target.ProjectID, BuildID: target.BuildID, AgentID: target.AgentID, DeployAnyway: target.DeployAnyway}
	return d.queue(ctx, request, change{replaces: live, reason: string(deployments.RolledBack), event: "project.deployment.rolled_back",
		fields: map[string]string{"rolled_back_to": target.ID}})
}

// withinRollbackDepth reports whether id is one of the rollbackDepth most recent
// stopped Deployments in listed, which is newest first.
func withinRollbackDepth(listed []Deployment, id string) bool {
	stopped := 0
	for _, dep := range listed {
		if dep.State != string(deployments.Stopped) {
			continue
		}
		if dep.ID == id {
			return true
		}
		if stopped++; stopped == rollbackDepth {
			return false
		}
	}
	return false
}

// Stop stops one of the Account's Deployments. It tells the Agent first, so a
// refused stop leaves the Deployment as it was, then records it stopped for the
// reason removed. A failed or stopped Deployment cannot be stopped.
func (d *Deployments) Stop(ctx context.Context, owner, id string) error {
	if d.deployer == nil {
		return errors.New("Agent enrollment is off, so there is no Agent to stop")
	}
	// A promotion can land between reading the state and moving it, so the move
	// is tried again from the state it finds the second time.
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		dep, found, err := d.store.FindDeployment(ctx, id, owner)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("deployment not found")
		}
		if dep.State == string(deployments.Failed) || dep.State == string(deployments.Stopped) {
			return fmt.Errorf("this deployment is already %s", dep.State)
		}
		if err = d.stopOnAgent(ctx, owner, dep.AgentID, id); err != nil {
			return err
		}
		err = d.store.TransitionDeployment(ctx, DeploymentTransition{ID: id, From: dep.State, To: string(deployments.Stopped), Reason: string(deployments.Removed)})
		if err == nil {
			d.record(ctx, "info", "project.deployment.stopped", map[string]string{"user_id": owner, "project_id": dep.ProjectID,
				"deployment_id": id, "reason": string(deployments.Removed)})
			return nil
		}
	}
	return err
}

// stopOnAgent tells the Agent to stop a Deployment. An Agent that is gone cannot
// serve it, so it counts as stopped; an Agent that is offline or refuses is an
// error, and the Deployment stays as it is.
func (d *Deployments) stopOnAgent(ctx context.Context, owner, agentID, id string) error {
	err := d.deployer.Stop(ctx, owner, agentID, id)
	if errors.Is(err, ErrAgentNotFound) {
		return nil
	}
	return err
}

// Teardown stops every Deployment the Account still has running, because the
// Account is being deleted. Each one is stopped on its Agent before it is
// recorded stopped. If an Agent refuses, the error comes back, and the
// Deployments already stopped stay stopped.
func (d *Deployments) Teardown(ctx context.Context, owner string) error {
	projects, err := d.store.ListOwnedProjects(ctx, owner)
	if err != nil {
		return err
	}
	for _, project := range projects {
		listed, err := d.store.ListDeployments(ctx, project, owner)
		if err != nil {
			return err
		}
		for _, dep := range listed {
			if dep.State != string(deployments.Healthy) && !deployments.InFlight(deployments.State(dep.State)) {
				continue
			}
			if d.deployer != nil {
				if err = d.stopOnAgent(ctx, owner, dep.AgentID, dep.ID); err != nil {
					return fmt.Errorf("stop Deployment %s: %w", dep.ID, err)
				}
			}
			if err = d.store.TransitionDeployment(ctx, DeploymentTransition{ID: dep.ID, From: dep.State, To: string(deployments.Stopped), Reason: string(deployments.AccountDeleted)}); err != nil {
				return err
			}
			d.record(ctx, "info", "project.deployment.stopped", map[string]string{"user_id": owner, "project_id": project,
				"deployment_id": dep.ID, "reason": string(deployments.AccountDeleted)})
		}
	}
	return nil
}

// live returns the ID of the Project's healthy Deployment, or "" when it has none.
func (d *Deployments) live(ctx context.Context, owner, projectID string) (string, error) {
	listed, err := d.store.ListDeployments(ctx, projectID, owner)
	if err != nil {
		return "", err
	}
	for _, dep := range listed {
		if dep.State == string(deployments.Healthy) {
			return dep.ID, nil
		}
	}
	return "", nil
}

// queue checks that request can be deployed, records a Deployment of it that
// does what c says to the live one, and starts it.
func (d *Deployments) queue(ctx context.Context, request NewDeployment, c change) (string, error) {
	if request.OwnerID == "" || request.ProjectID == "" {
		return "", errors.New("project is required")
	}
	if d.deployer == nil {
		return "", errors.New("Agent enrollment is off, so there is no Agent to deploy to")
	}
	build, found, err := d.store.FindWASMBuild(ctx, request.BuildID, request.OwnerID)
	if err != nil {
		return "", err
	}
	if !found || build.ProjectID != request.ProjectID {
		return "", errors.New("WASM build not found")
	}
	if build.Status != "complete" {
		return "", errors.New("only a completed WASM build can be deployed")
	}
	check, found, err := d.store.FindCheck(ctx, build.ID, request.OwnerID)
	if err != nil {
		return "", err
	}
	switch {
	case !found:
		return "", errors.New("checks have not finished for this WASM build")
	case check.Status == CheckRunning:
		return "", errors.New("checks are still running for this WASM build")
	case check.Status == CheckFailed && !request.DeployAnyway:
		return "", errors.New("checks failed for this WASM build; deploy anyway to deploy it")
	}
	if _, found, err = d.store.FindAgent(ctx, request.AgentID, request.OwnerID); err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("Agent not found")
	}
	if err = d.deployer.Ready(ctx, request.OwnerID, request.AgentID); err != nil {
		return "", err
	}

	// Register the call under the lock, so Drain cannot finish before it, then
	// call the ports without holding the lock.
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return "", errors.New("deployments are shutting down")
	}
	d.wg.Add(1)
	d.mu.Unlock()

	id := uuid.NewString()
	dep := deployment{OwnerID: request.OwnerID, ProjectID: request.ProjectID, ID: id, BuildID: request.BuildID, AgentID: request.AgentID, reason: c.reason}
	if err = d.store.CreateDeployment(ctx, CreateDeployment{ID: id, OwnerID: request.OwnerID, ProjectID: request.ProjectID,
		BuildID: request.BuildID, AgentID: request.AgentID, DeployAnyway: request.DeployAnyway}); err != nil {
		d.wg.Done()
		return "", err
	}
	fields := dep.fields()
	fields["build_id"], fields["agent_id"], fields["deploy_anyway"] = request.BuildID, request.AgentID, boolText(request.DeployAnyway)
	if c.replaces != "" {
		fields["replaces"] = c.replaces
	}
	for name, value := range c.fields {
		fields[name] = value
	}
	d.record(ctx, "info", c.event, fields)
	go d.run(dep)
	return id, nil
}

// Find returns one of an Account's Deployments, with the Check of its WASM
// Build, which keeps the checks' own error.
func (d *Deployments) Find(ctx context.Context, owner, id string) (Deployment, bool, error) {
	deployment, found, err := d.store.FindDeployment(ctx, id, owner)
	if err != nil || !found {
		return Deployment{}, false, err
	}
	check, ok, err := d.store.FindCheck(ctx, deployment.BuildID, owner)
	if err != nil {
		return Deployment{}, false, err
	}
	if ok {
		deployment.Check = &check
	}
	return deployment, true, nil
}

// Wait waits for the running Deployments, or until ctx ends.
func (d *Deployments) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Drain makes Create refuse new Deployments and waits for the running ones, or
// until ctx ends.
func (d *Deployments) Drain(ctx context.Context) error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	return d.Wait(ctx)
}

// run hands the Deployment to its Agent and, once the Agent reports it healthy,
// makes it the Project's live Deployment and stops the one it replaces. Its own
// failures are recorded by fail, which writes with a fresh context, so a timeout
// or a shutdown cannot leave the Deployment in flight.
func (d *Deployments) run(dep deployment) {
	defer d.wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			d.fail(dep, fmt.Sprintf("Deployment panicked: %v", recovered))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), deployTimeout)
	defer cancel()
	if err := d.store.TransitionDeployment(ctx, DeploymentTransition{ID: dep.ID, From: string(deployments.Queued), To: string(deployments.Starting)}); err != nil {
		d.fail(dep, "the Deployment could not start: "+err.Error())
		return
	}
	if err := d.deployer.Deploy(ctx, dep.OwnerID, dep.AgentID, dep.ID, dep.BuildID); err != nil {
		d.fail(dep, err.Error())
		return
	}
	superseded, err := d.store.PromoteDeployment(ctx, PromoteDeployment{ID: dep.ID, OwnerID: dep.OwnerID, ProjectID: dep.ProjectID, Supersedes: dep.reason})
	if err != nil {
		// The Deployment was stopped while the Agent was deploying it, so the Agent
		// may hold a module that must not serve. Tell it to stop.
		if stopErr := d.stopOnAgent(ctx, dep.OwnerID, dep.AgentID, dep.ID); stopErr != nil {
			d.record(context.Background(), "warn", "project.deployment.stop_failed", dep.fields())
		}
		d.fail(dep, "the Deployment could not become healthy: "+err.Error())
		return
	}
	d.record(context.Background(), "info", "project.deployment.healthy", dep.fields())
	for _, old := range superseded {
		if err := d.stopOnAgent(ctx, dep.OwnerID, old.AgentID, old.ID); err != nil {
			d.record(context.Background(), "warn", "project.deployment.stop_failed", map[string]string{"user_id": dep.OwnerID,
				"project_id": dep.ProjectID, "deployment_id": old.ID, "error": err.Error()})
		}
	}
}

// fail ends a Deployment as failed with reason, from whichever state it is in.
// It writes with its own context and time bound, not the Deployment's. When no
// state moved, the Deployment was stopped while it ran, and nothing is recorded.
func (d *Deployments) fail(dep deployment, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()
	for _, from := range []deployments.State{deployments.Starting, deployments.Queued} {
		if err := d.store.TransitionDeployment(ctx, DeploymentTransition{ID: dep.ID, From: string(from), To: string(deployments.Failed), Error: reason}); err == nil {
			d.record(ctx, "error", "project.deployment.failed", dep.fields())
			return
		}
	}
}

func (d *Deployments) record(ctx context.Context, level, name string, fields map[string]string) {
	d.audit.Record(ctx, AuditEvent{Level: level, Name: name, Fields: fields})
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
