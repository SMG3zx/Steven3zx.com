// Package accountevents is how a service consumes events that ask it to clear
// an Account. A service's handler does its teardown, then confirms; a failed
// handler is delivered again, and the handlers are idempotent, so a restart in
// the middle of a deletion resumes it. This package only does the delivery and
// the acknowledgements: what an event means belongs to the service that
// consumes it.
package accountevents

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/platform/natsx"
)

// retryDelay is how long a failed event waits before it is delivered again.
const retryDelay = 5 * time.Second

// handlerTimeout bounds one delivery of an event.
const handlerTimeout = 30 * time.Second

// permanent marks an error that delivering the event again cannot fix.
type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// Permanent wraps err for a handler to return when the event can never be
// applied, such as one that does not decode. The event is then dropped rather
// than delivered again.
func Permanent(err error) error {
	return permanent{err: err}
}

// Consume starts the durable consumer durable on EVENTS for the events on
// subject, and runs handle on each one. An event is acknowledged when handle
// returns nil. Any other error delivers it again later, unless the error is
// Permanent. The returned context stops the consumer.
func Consume(js jetstream.JetStream, durable, subject string, handle func(ctx context.Context, data []byte) error) (jetstream.ConsumeContext, error) {
	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()
	stream, err := js.Stream(ctx, natsx.EventsStream)
	if err != nil {
		return nil, err
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       durable,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return nil, err
	}
	return consumer.Consume(func(msg jetstream.Msg) {
		deliver(msg, handle)
	})
}

// deliver runs handle on one event and settles the message with JetStream.
func deliver(msg jetstream.Msg, handle func(ctx context.Context, data []byte) error) {
	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()
	err := handle(ctx, msg.Data())
	var never permanent
	switch {
	case err == nil:
		_ = msg.Ack()
	case errors.As(err, &never):
		_ = msg.Term()
	default:
		_ = msg.NakWithDelay(retryDelay)
	}
}
