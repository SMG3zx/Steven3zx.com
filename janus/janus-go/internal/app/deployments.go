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

// AgentDeployer is what a Deployment needs from its Agent. Ready says whether
// the Owner's Agent can take a Deploy right now, and returns an error saying why
// it cannot. Deploy hands a completed WASM Build to the Agent as a Deployment,
// and returns nil once the Agent reports it healthy. Fleet is the real one.
type AgentDeployer interface {
	Ready(ctx context.Context, ownerID, agentID string) error
	Deploy(ctx context.Context, ownerID, agentID, deploymentID, buildID string) error
}

// NewDeployment asks to deploy a WASM Build on one of the Owner's Agents.
type NewDeployment struct {
	OwnerID, ProjectID, BuildID, AgentID string
	// DeployAnyway deploys the Build even though its checks failed.
	DeployAnyway bool
}

// deployment identifies one Deployment being run, so its records can be written
// without passing the same five strings around.
type deployment struct {
	OwnerID, ProjectID, ID, BuildID, AgentID string
}

// audit fields that name a Deployment.
func (d deployment) fields() map[string]string {
	return map[string]string{"user_id": d.OwnerID, "project_id": d.ProjectID, "deployment_id": d.ID}
}

// Deployments queues Deployments and runs each one on a goroutine the use case
// owns, until its Agent reports it healthy or refuses it. Each state change is
// recorded in the Store as it happens.
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

// Create queues a Deployment and starts it, and returns the Deployment's ID. It
// returns a plain error whose text is meant for the Account when the Deployment
// cannot be queued.
func (d *Deployments) Create(ctx context.Context, request NewDeployment) (string, error) {
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
	dep := deployment{OwnerID: request.OwnerID, ProjectID: request.ProjectID, ID: id, BuildID: request.BuildID, AgentID: request.AgentID}
	if err = d.store.CreateDeployment(ctx, CreateDeployment{ID: id, OwnerID: request.OwnerID, ProjectID: request.ProjectID,
		BuildID: request.BuildID, AgentID: request.AgentID, DeployAnyway: request.DeployAnyway}); err != nil {
		d.wg.Done()
		return "", err
	}
	fields := dep.fields()
	fields["build_id"], fields["agent_id"], fields["deploy_anyway"] = request.BuildID, request.AgentID, boolText(request.DeployAnyway)
	d.record(ctx, "info", "project.deployment.queued", fields)
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

// run hands the Deployment to its Agent and records whether it became healthy.
// Its own failures are recorded by fail, which writes with a fresh context, so a
// timeout or a shutdown cannot leave the Deployment in flight.
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
	if err := d.store.TransitionDeployment(ctx, DeploymentTransition{ID: dep.ID, From: string(deployments.Starting), To: string(deployments.Healthy)}); err != nil {
		d.fail(dep, "the Deployment could not become healthy: "+err.Error())
		return
	}
	d.record(context.Background(), "info", "project.deployment.healthy", dep.fields())
}

// fail ends a Deployment as failed with reason, from whichever state it is in.
// It writes with its own context and time bound, not the Deployment's.
func (d *Deployments) fail(dep deployment, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()
	for _, from := range []deployments.State{deployments.Starting, deployments.Queued} {
		if err := d.store.TransitionDeployment(ctx, DeploymentTransition{ID: dep.ID, From: string(from), To: string(deployments.Failed), Error: reason}); err == nil {
			break
		}
	}
	d.record(ctx, "error", "project.deployment.failed", dep.fields())
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
