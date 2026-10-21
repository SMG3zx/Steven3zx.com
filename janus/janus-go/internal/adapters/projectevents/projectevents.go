// Package projectevents applies the projects service's events to the single
// process: when a Project or Snapshot is deleted there, its WASM Builds and
// what refers to them go here too. It reads EVENTS with a durable consumer, so
// events published while the process was down are applied when it starts.
package projectevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natsx"
)

// Consumer is the durable consumer's name.
const Consumer = "janus-projects-events"

// retryDelay is how long a failed event waits before it is applied again.
const retryDelay = 5 * time.Second

// Forgetter deletes what refers to a Project or Snapshot that no longer
// exists. app.Builds is one.
type Forgetter interface {
	ForgetProject(ctx context.Context, projectID string) error
	ForgetSnapshot(ctx context.Context, snapshotID string) error
}

// Events applies project events to a Forgetter.
type Events struct {
	forget  Forgetter
	consume jetstream.ConsumeContext
}

// New returns stopped Events that apply to forget.
func New(forget Forgetter) *Events { return &Events{forget: forget} }

// Start declares EVENTS and the consumer if they are missing and applies
// events as they arrive. Each event is acknowledged once applied; one that
// fails is delivered again.
func (e *Events) Start(ctx context.Context, conn *nats.Conn) error {
	js, err := jetstream.New(conn)
	if err != nil {
		return fmt.Errorf("open JetStream: %w", err)
	}
	stream, err := natsx.DeclareEvents(ctx, js)
	if err != nil {
		return err
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       Consumer,
		FilterSubject: contractprojects.SubjectEvents,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return fmt.Errorf("declare the project events consumer: %w", err)
	}
	if e.consume, err = consumer.Consume(e.handle); err != nil {
		return fmt.Errorf("consume project events: %w", err)
	}
	return nil
}

func (e *Events) handle(msg jetstream.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var err error
	switch msg.Subject() {
	case contractprojects.SubjectProjectDeleted:
		var event contractprojects.ProjectDeletedEvent
		if err = json.Unmarshal(msg.Data(), &event); err == nil {
			err = e.forget.ForgetProject(ctx, event.ProjectID)
		}
	case contractprojects.SubjectSnapshotDeleted:
		var event contractprojects.SnapshotDeletedEvent
		if err = json.Unmarshal(msg.Data(), &event); err == nil {
			err = e.forget.ForgetSnapshot(ctx, event.SnapshotID)
		}
	}
	var syntax *json.SyntaxError
	switch {
	case errors.As(err, &syntax):
		_ = msg.Term() // can never be applied
	case err != nil:
		_ = msg.NakWithDelay(retryDelay)
	default:
		_ = msg.Ack()
	}
}

// Stop stops taking events and waits for the one being applied.
func (e *Events) Stop(ctx context.Context) error {
	if e.consume == nil {
		return nil
	}
	e.consume.Drain()
	select {
	case <-e.consume.Closed():
		return nil
	case <-ctx.Done():
		e.consume.Stop()
		return ctx.Err()
	}
}
