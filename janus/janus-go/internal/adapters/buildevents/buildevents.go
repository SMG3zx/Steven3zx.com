// Package buildevents applies the builds service's events to the single process:
// each Build's and Check's state goes into the Store as the reference its
// Deployments refer to. It reads EVENTS with a durable consumer, so events
// published while the process was down are applied when it starts.
package buildevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/app"
	contractbuilds "janus/internal/contract/builds"
	"janus/internal/platform/natsx"
)

// Consumer is the durable consumer's name.
const Consumer = "janus-builds-events"

// retryDelay is how long a failed event waits before it is applied again.
const retryDelay = 5 * time.Second

// References stores the state of Builds and Checks.
type References interface {
	ApplyWASMBuild(ctx context.Context, build app.WASMBuildChange) error
	ApplyCheck(ctx context.Context, check app.CheckChange) error
}

// Events applies builds events to a References.
type Events struct {
	refs    References
	consume jetstream.ConsumeContext
}

// New returns stopped Events that apply to refs.
func New(refs References) *Events { return &Events{refs: refs} }

// Start declares EVENTS and the consumer if they are missing and applies events
// as they arrive. Each event is acknowledged once applied; one that fails is
// delivered again.
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
		FilterSubject: contractbuilds.SubjectEvents,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return fmt.Errorf("declare the build events consumer: %w", err)
	}
	if e.consume, err = consumer.Consume(e.handle); err != nil {
		return fmt.Errorf("consume build events: %w", err)
	}
	return nil
}

func (e *Events) handle(msg jetstream.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var err error
	switch msg.Subject() {
	case contractbuilds.SubjectBuildChanged:
		var event contractbuilds.BuildChanged
		if err = json.Unmarshal(msg.Data(), &event); err == nil {
			err = e.refs.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: event.BuildID, OwnerID: event.OwnerID, ProjectID: event.ProjectID,
				SnapshotID: event.SnapshotID, Target: event.Target, Status: event.Status, Error: event.Error})
		}
	case contractbuilds.SubjectCheckChanged:
		var event contractbuilds.CheckChanged
		if err = json.Unmarshal(msg.Data(), &event); err == nil {
			err = e.refs.ApplyCheck(ctx, app.CheckChange{BuildID: event.BuildID, OwnerID: event.OwnerID, Status: event.Status,
				Error: event.Error, Log: event.Log, NoTests: event.NoTests})
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
