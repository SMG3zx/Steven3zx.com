package fleet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	contractbuilds "janus/internal/contract/builds"
	contractfleet "janus/internal/contract/fleet"
	"janus/internal/domain/deployments"
)

// deployTimeout bounds how long one Deployment may take to become healthy.
const deployTimeout = 2 * time.Minute

// rollbackDepth is how many of a Project's most recent stopped Deployments a
// rollback may go back to (GLOSSARY.md, Deployment).
const rollbackDepth = 5

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
// without passing the same strings around.
type deployment struct {
	OwnerID, ProjectID, SnapshotID, ID, BuildID, AgentID string
	// reason is what the Deployment stops the Project's healthy Deployments with
	// when it becomes healthy.
	reason string
}

// fields are the audit fields that name a Deployment.
func (d deployment) fields() map[string]string {
	return map[string]string{"user_id": d.OwnerID, "project_id": d.ProjectID, "deployment_id": d.ID}
}

// createDeployment queues a Deployment of a WASM Build on one of the Owner's
// Agents and starts it. When the Project has a live Deployment, this is a
// redeploy: the live one keeps serving until the new one is healthy.
func (s *Service) createDeployment(ctx context.Context, request contractfleet.NewDeployment) (contractfleet.Queued, error) {
	live, err := s.liveDeployment(ctx, request.OwnerID, request.ProjectID)
	if err != nil {
		return contractfleet.Queued{}, err
	}
	if live == "" {
		id, err := s.queue(ctx, request, change{event: "project.deployment.queued"})
		return contractfleet.Queued{ID: id}, err
	}
	id, err := s.queue(ctx, request, change{replaces: live, reason: string(deployments.Replaced), event: "project.deployment.redeployed"})
	return contractfleet.Queued{ID: id}, err
}

// rollbackDeployment redeploys the WASM Build of one of the Owner's stopped
// Deployments, one of the last five of its Project, as a new Deployment. The live
// Deployment keeps serving until the rollback is healthy.
func (s *Service) rollbackDeployment(ctx context.Context, key contractfleet.DeploymentKey) (contractfleet.Queued, error) {
	if err := requireOwner(key.OwnerID); err != nil {
		return contractfleet.Queued{}, err
	}
	target, found, err := s.loadDeployment(ctx, key.DeploymentID, key.OwnerID)
	if err != nil {
		return contractfleet.Queued{}, err
	}
	if !found {
		return contractfleet.Queued{}, notFound("deployment not found")
	}
	if target.State != string(deployments.Stopped) {
		return contractfleet.Queued{}, invalid("only a stopped deployment can be rolled back to")
	}
	listed, err := s.listDeployments(ctx, target.ProjectID, key.OwnerID)
	if err != nil {
		return contractfleet.Queued{}, err
	}
	if !withinRollbackDepth(listed, target.ID) {
		return contractfleet.Queued{}, invalid("only one of the last five stopped deployments can be rolled back to")
	}
	live, err := s.liveDeployment(ctx, key.OwnerID, target.ProjectID)
	if err != nil {
		return contractfleet.Queued{}, err
	}
	request := contractfleet.NewDeployment{OwnerID: key.OwnerID, ProjectID: target.ProjectID, BuildID: target.BuildID,
		AgentID: target.AgentID, DeployAnyway: target.DeployAnyway}
	id, err := s.queue(ctx, request, change{replaces: live, reason: string(deployments.RolledBack), event: "project.deployment.rolled_back",
		fields: map[string]string{"rolled_back_to": target.ID}})
	return contractfleet.Queued{ID: id}, err
}

// withinRollbackDepth reports whether id is one of the rollbackDepth most recent
// stopped Deployments in listed, which is newest first.
func withinRollbackDepth(listed []deploymentRow, id string) bool {
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

// stopDeployment stops one of the Owner's Deployments. It tells the Agent first,
// so a refused stop leaves the Deployment as it was, then records it stopped for
// the reason removed. A failed or stopped Deployment cannot be stopped.
func (s *Service) stopDeployment(ctx context.Context, key contractfleet.DeploymentKey) (contractfleet.Empty, error) {
	if err := requireOwner(key.OwnerID); err != nil {
		return contractfleet.Empty{}, err
	}
	// A promotion can land between reading the state and moving it, so the move
	// is tried again from the state it finds the second time.
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		dep, found, loadErr := s.loadDeployment(ctx, key.DeploymentID, key.OwnerID)
		if loadErr != nil {
			return contractfleet.Empty{}, loadErr
		}
		if !found {
			return contractfleet.Empty{}, notFound("deployment not found")
		}
		if dep.State == string(deployments.Failed) || dep.State == string(deployments.Stopped) {
			return contractfleet.Empty{}, invalid("this deployment is already %s", dep.State)
		}
		if err = s.stopOnAgent(ctx, key.OwnerID, dep.AgentID, key.DeploymentID); err != nil {
			return contractfleet.Empty{}, err
		}
		err = s.moveDeployment(ctx, key.DeploymentID, dep.State, string(deployments.Stopped), "", string(deployments.Removed))
		if err == nil {
			s.audit(ctx, "info", "project.deployment.stopped", map[string]string{"user_id": key.OwnerID, "project_id": dep.ProjectID,
				"deployment_id": key.DeploymentID, "reason": string(deployments.Removed)})
			return contractfleet.Empty{}, nil
		}
	}
	return contractfleet.Empty{}, err
}

// stopOnAgent tells the Agent to stop a Deployment. An Agent that is gone cannot
// serve it, so it counts as stopped; an Agent that is offline or refuses is an
// error, and the Deployment stays as it is.
func (s *Service) stopOnAgent(ctx context.Context, owner, agentID, deploymentID string) error {
	err := s.sendToAgent(ctx, owner, agentID, contractfleet.AgentCommand{Stop: deploymentID})
	if errors.Is(err, errAgentNotFound) {
		return nil
	}
	return err
}

// findDeployment returns one of the Owner's Deployments.
func (s *Service) findDeployment(ctx context.Context, key contractfleet.DeploymentKey) (contractfleet.DeploymentFound, error) {
	if err := requireOwner(key.OwnerID); err != nil {
		return contractfleet.DeploymentFound{}, err
	}
	dep, found, err := s.loadDeployment(ctx, key.DeploymentID, key.OwnerID)
	if err != nil || !found {
		return contractfleet.DeploymentFound{}, err
	}
	return contractfleet.DeploymentFound{Found: true, Deployment: dep.Deployment}, nil
}

// teardownDeployments stops every Deployment the Owner still has running, because
// the Account is being deleted. Each one is stopped on its Agent before it is
// recorded stopped. If an Agent refuses, the error comes back, and the
// Deployments already stopped stay stopped.
func (s *Service) teardownDeployments(ctx context.Context, request contractfleet.Owner) (contractfleet.Empty, error) {
	if err := requireOwner(request.OwnerID); err != nil {
		return contractfleet.Empty{}, err
	}
	live, err := s.liveDeployments(ctx, request.OwnerID)
	if err != nil {
		return contractfleet.Empty{}, err
	}
	for _, dep := range live {
		if err = s.stopOnAgent(ctx, request.OwnerID, dep.AgentID, dep.ID); err != nil {
			return contractfleet.Empty{}, fmt.Errorf("stop Deployment %s: %w", dep.ID, err)
		}
		if err = s.moveDeployment(ctx, dep.ID, dep.State, string(deployments.Stopped), "", string(deployments.AccountDeleted)); err != nil {
			return contractfleet.Empty{}, err
		}
		s.audit(ctx, "info", "project.deployment.stopped", map[string]string{"user_id": request.OwnerID, "project_id": dep.ProjectID,
			"deployment_id": dep.ID, "reason": string(deployments.AccountDeleted)})
	}
	return contractfleet.Empty{}, nil
}

// liveDeployment returns the ID of the Project's healthy Deployment, or "" when
// it has none.
func (s *Service) liveDeployment(ctx context.Context, owner, projectID string) (string, error) {
	listed, err := s.listDeployments(ctx, projectID, owner)
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

// queue checks that request can be deployed, records a Deployment of it that does
// what c says to the live one, and starts it.
func (s *Service) queue(ctx context.Context, request contractfleet.NewDeployment, c change) (string, error) {
	if request.OwnerID == "" || request.ProjectID == "" {
		return "", invalid("project is required")
	}
	build, found, err := s.findBuild(ctx, request.OwnerID, request.BuildID)
	if err != nil {
		return "", err
	}
	if !found || build.ProjectID != request.ProjectID {
		return "", invalid("WASM build not found")
	}
	if build.Status != contractbuilds.StatusComplete {
		return "", invalid("only a completed WASM build can be deployed")
	}
	check, found, err := s.findCheck(ctx, request.OwnerID, build.ID)
	if err != nil {
		return "", err
	}
	switch {
	case !found:
		return "", invalid("checks have not finished for this WASM build")
	case check.Status == checkRunning:
		return "", invalid("checks are still running for this WASM build")
	case check.Status == checkFailed && !request.DeployAnyway:
		return "", invalid("checks failed for this WASM build; deploy anyway to deploy it")
	}
	if _, found, err = s.loadAgent(ctx, request.AgentID, request.OwnerID); err != nil {
		return "", err
	} else if !found {
		return "", errAgentNotFound
	}
	if err = s.ready(ctx, request.OwnerID, request.AgentID); err != nil {
		return "", err
	}

	// Register the Deployment under the lock, so Stop cannot finish before it.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", contractUnavailable("deployments are shutting down")
	}
	s.running.Add(1)
	s.mu.Unlock()

	id := uuid.NewString()
	dep := deployment{OwnerID: request.OwnerID, ProjectID: request.ProjectID, SnapshotID: build.SnapshotID, ID: id,
		BuildID: request.BuildID, AgentID: request.AgentID, reason: c.reason}
	if err = s.insertDeployment(ctx, request.OwnerID, deploymentRow{Deployment: contractfleet.Deployment{ID: id, ProjectID: request.ProjectID,
		BuildID: request.BuildID, AgentID: request.AgentID, DeployAnyway: request.DeployAnyway}, SnapshotID: build.SnapshotID}); err != nil {
		s.running.Done()
		if errors.Is(err, inFlightError) {
			return "", invalid("%s", err.Error())
		}
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
	s.audit(ctx, "info", c.event, fields)
	go s.run(dep)
	return id, nil
}

// run hands the Deployment to its Agent and, once the Agent reports it healthy,
// makes it the Project's live Deployment and stops the one it replaces. Its own
// failures are recorded by fail, which writes with a fresh context, so a timeout
// or a shutdown cannot leave the Deployment in flight.
func (s *Service) run(dep deployment) {
	defer s.running.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			s.fail(dep, fmt.Sprintf("Deployment panicked: %v", recovered))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), deployTimeout)
	defer cancel()
	if err := s.moveDeployment(ctx, dep.ID, string(deployments.Queued), string(deployments.Starting), "", ""); err != nil {
		s.fail(dep, "the Deployment could not start: "+err.Error())
		return
	}
	hosted, err := s.hostedDeployment(ctx, dep.OwnerID, dep.ProjectID, dep.BuildID, dep.ID)
	if err == nil {
		err = s.sendToAgent(ctx, dep.OwnerID, dep.AgentID, contractfleet.AgentCommand{Deploy: &hosted})
	}
	if err != nil {
		s.fail(dep, err.Error())
		return
	}
	superseded, err := s.promoteDeployment(ctx, dep.ID, dep.OwnerID, dep.ProjectID, dep.reason)
	if err != nil {
		// The Deployment was stopped while the Agent was deploying it, so the Agent
		// may hold a module that must not serve. Tell it to stop.
		if stopErr := s.stopOnAgent(ctx, dep.OwnerID, dep.AgentID, dep.ID); stopErr != nil {
			s.audit(context.Background(), "warn", "project.deployment.stop_failed", dep.fields())
		}
		s.fail(dep, "the Deployment could not become healthy: "+err.Error())
		return
	}
	s.audit(context.Background(), "info", "project.deployment.healthy", dep.fields())
	for _, old := range superseded {
		if err := s.stopOnAgent(ctx, dep.OwnerID, old.AgentID, old.ID); err != nil {
			s.audit(context.Background(), "warn", "project.deployment.stop_failed", map[string]string{"user_id": dep.OwnerID,
				"project_id": dep.ProjectID, "deployment_id": old.ID, "error": err.Error()})
		}
	}
}

// fail ends a Deployment as failed with reason, from whichever state it is in.
// It writes with its own context and time bound, not the Deployment's. When no
// state moved, the Deployment was stopped while it ran, and nothing is recorded.
func (s *Service) fail(dep deployment, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
	defer cancel()
	for _, from := range []deployments.State{deployments.Starting, deployments.Queued} {
		if err := s.moveDeployment(ctx, dep.ID, string(from), string(deployments.Failed), reason, ""); err == nil {
			s.audit(ctx, "error", "project.deployment.failed", dep.fields())
			return
		}
	}
}

// The Check statuses of the builds service that a Deployment looks at.
const (
	checkRunning = "running"
	checkFailed  = "failed"
)

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
