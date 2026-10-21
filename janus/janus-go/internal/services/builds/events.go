package builds

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
const projectsConsumer = "builds-projects"

// eventRetryDelay is how long a failed event waits before it is applied again.
const eventRetryDelay = 5 * time.Second

// startProjectEvents starts the consumer that deletes the Builds of Projects and
// Snapshots the projects service deleted. Events published while this service
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
	s.wg.Add(1)
	defer s.wg.Done()
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
			removed, err = s.deleteWhere(ctx, "project_id=?", event.ProjectID)
		}
	case contractprojects.SubjectSnapshotDeleted:
		var event contractprojects.SnapshotDeletedEvent
		if err = json.Unmarshal(msg.Data(), &event); err == nil {
			fields["snapshot_id"] = event.SnapshotID
			removed, err = s.deleteWhere(ctx, "snapshot_id=?", event.SnapshotID)
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
		s.audit(ctx, "info", "project.wasm.builds.removed", fields)
		_ = msg.Ack()
	}
}
