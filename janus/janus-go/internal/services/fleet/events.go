package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natsx"
)

// projectsConsumer is the durable consumer of the projects service's events.
const projectsConsumer = "fleet-projects"

// eventRetryDelay is how long a failed event waits before it is applied again.
const eventRetryDelay = 5 * time.Second

// startProjectEvents starts the consumer that removes the Deployments of Projects
// and Snapshots the projects service deleted. Events published while this service
// was down are applied when it starts.
func (s *Service) startProjectEvents(ctx context.Context) error {
	stream, err := s.js.Stream(ctx, natsx.EventsStream)
	if err != nil {
		return fmt.Errorf("open the %s stream: %w", natsx.EventsStream, err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       projectsConsumer,
		FilterSubject: contractprojects.SubjectEvents,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return fmt.Errorf("declare the project events consumer: %w", err)
	}
	if s.events, err = consumer.Consume(s.handleProjectEvent); err != nil {
		return fmt.Errorf("consume project events: %w", err)
	}
	return nil
}

// handleProjectEvent applies one event of the projects service. Each event is
// acknowledged once applied; one that fails is delivered again.
func (s *Service) handleProjectEvent(msg jetstream.Msg) {
	s.running.Add(1)
	defer s.running.Done()
	ctx, cancel := context.WithTimeout(s.stopCtx, operationTimeout)
	defer cancel()
	var err error
	var removed int
	fields := map[string]string{}
	switch msg.Subject() {
	case contractprojects.SubjectProjectDeleted:
		var event contractprojects.ProjectDeletedEvent
		if err = json.Unmarshal(msg.Data(), &event); err == nil {
			fields["project_id"] = event.ProjectID
			removed, err = s.removeDeployments(ctx, "project_id", event.ProjectID)
		}
	case contractprojects.SubjectSnapshotDeleted:
		var event contractprojects.SnapshotDeletedEvent
		if err = json.Unmarshal(msg.Data(), &event); err == nil {
			fields["snapshot_id"] = event.SnapshotID
			removed, err = s.removeDeployments(ctx, "snapshot_id", event.SnapshotID)
		}
	}
	var syntax *json.SyntaxError
	switch {
	case errors.As(err, &syntax):
		_ = msg.Term() // can never be applied
	case err != nil:
		_ = msg.NakWithDelay(eventRetryDelay)
	default:
		fields["removed"] = strconv.Itoa(removed)
		s.audit(ctx, "info", "project.deployments.removed", fields)
		_ = msg.Ack()
	}
}

// removeDeployments stops the Deployments whose column matches value and deletes
// them. A Deployment whose Agent cannot be reached is deleted all the same: the
// Project or Snapshot it serves is gone, and the Agent is told to stop as soon as
// it can be.
func (s *Service) removeDeployments(ctx context.Context, column, value string) (int, error) {
	listed, err := s.deploymentsWhere(ctx, column, value, "")
	if err != nil {
		return 0, err
	}
	for _, dep := range listed {
		if err = s.stopOnAgent(ctx, dep.OwnerID, dep.AgentID, dep.ID); err != nil {
			s.audit(ctx, "warn", "project.deployment.stop_failed", map[string]string{"deployment_id": dep.ID,
				"agent_id": dep.AgentID, "error": err.Error()})
		}
	}
	return s.deleteDeploymentsWhere(ctx, column, value)
}
