// Package audit is the audit service. It owns the AUDIT stream and writes
// every Audit Event published on it through zap, which also sends it to
// OpenTelemetry logs. It owns no database.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	contract "janus/internal/contract/audit"
	"janus/internal/platform/auditlog"
)

// Retention is how long AUDIT keeps an event, written or not.
const Retention = 7 * 24 * time.Hour

// Config configures the service.
type Config struct {
	// Logger receives every event. Nil drops them, after acknowledging.
	Logger *zap.Logger
}

// Service consumes AUDIT with a durable consumer, so events published while it
// is stopped are written when it starts again.
type Service struct {
	logger  *zap.Logger
	consume jetstream.ConsumeContext
}

// New returns a stopped service.
func New(cfg Config) *Service { return &Service{logger: cfg.Logger} }

// Start declares AUDIT and the service's consumer if they are missing, then
// writes events as they arrive. Each event is acknowledged after it is written.
func (s *Service) Start(ctx context.Context, conn *nats.Conn) error {
	js, err := jetstream.New(conn)
	if err != nil {
		return fmt.Errorf("open JetStream: %w", err)
	}
	stream, err := DeclareStream(ctx, js)
	if err != nil {
		return err
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       contract.Consumer,
		FilterSubject: contract.Subjects,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return fmt.Errorf("declare the audit consumer: %w", err)
	}
	s.consume, err = consumer.Consume(s.handle)
	if err != nil {
		return fmt.Errorf("consume %s: %w", contract.Stream, err)
	}
	return nil
}

// DeclareStream creates AUDIT, or brings its settings up to date, and returns
// it. Calling it again changes nothing.
func DeclareStream(ctx context.Context, js jetstream.JetStream) (jetstream.Stream, error) {
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      contract.Stream,
		Subjects:  []string{contract.Subjects},
		Retention: jetstream.LimitsPolicy,
		MaxAge:    Retention,
		Storage:   jetstream.FileStorage,
		Discard:   jetstream.DiscardOld,
	})
	if err != nil {
		return nil, fmt.Errorf("declare the %s stream: %w", contract.Stream, err)
	}
	return stream, nil
}

// handle writes one event. A message that is not an event can never be
// written, so it is reported and terminated rather than redelivered.
func (s *Service) handle(msg jetstream.Msg) {
	var event contract.Event
	if err := json.Unmarshal(msg.Data(), &event); err != nil {
		auditlog.Write(s.logger, "error", "audit.event.malformed", map[string]string{"subject": msg.Subject(), "error": err.Error()})
		_ = msg.Term()
		return
	}
	auditlog.Write(s.logger, event.Level, event.Name, event.Fields)
	_ = msg.Ack()
}

// Stop stops taking new events and waits for the one being written. Events
// not yet written stay in AUDIT for the next start.
func (s *Service) Stop(ctx context.Context) error {
	if s.consume == nil {
		return nil
	}
	s.consume.Drain()
	select {
	case <-s.consume.Closed():
		return nil
	case <-ctx.Done():
		s.consume.Stop()
		return errors.Join(errors.New("stop the audit consumer"), ctx.Err())
	}
}
