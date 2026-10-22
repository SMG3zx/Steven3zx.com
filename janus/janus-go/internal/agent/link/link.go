// Package link is the link between the control plane and its Janus Agents
// over the hub's NATS server (ENG-122). Link is the control plane's end, the
// Agent link of the fleet service. Serve is the Agent's end: it answers the
// commands, writes the Agent's state for the hub to read, and publishes its
// Usage in the Agent's own JetStream domain, where the hub reads it from.
// See section 6 of docs/architecture.md.
package link

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractfleet "janus/internal/contract/fleet"
)

// retryMin and retryMax bound the wait before a Connect tries again, while the
// Agent's stream or its state is not there yet.
const (
	retryMin = 200 * time.Millisecond
	retryMax = 5 * time.Second
)

// redeliverDelay is how long an Usage report waits before the fleet gets it
// again, after the database refused it.
const redeliverDelay = time.Second

// Link is the control plane's end of the Agent link. It holds the Usage and state
// subscriptions of each connected Agent, and sends commands over the hub's
// connection.
type Link struct {
	conn *nats.Conn
	js   jetstream.JetStream

	mu     sync.Mutex
	stops  map[string]context.CancelFunc
	closed bool
	wg     sync.WaitGroup
}

// NewLink returns a Link over conn, the hub's connection.
func NewLink(conn *nats.Conn) (*Link, error) {
	js, err := jetstream.New(conn)
	if err != nil {
		return nil, err
	}
	return &Link{conn: conn, js: js, stops: map[string]context.CancelFunc{}}, nil
}

// Connect keeps the Usage and state of the Agent with agentID flowing to events
// until Disconnect. Connecting an Agent twice does nothing.
func (l *Link) Connect(agentID string, events func(contractfleet.AgentEvent) error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.stops[agentID]; ok || l.closed {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.stops[agentID] = cancel
	l.wg.Add(2)
	go func() {
		defer l.wg.Done()
		retry(ctx, func() error { return l.consumeUsage(ctx, agentID, events) })
	}()
	go func() {
		defer l.wg.Done()
		retry(ctx, func() error { return l.watchState(ctx, agentID, events) })
	}()
}

// Disconnect ends the Usage and state flow of the Agent with agentID.
func (l *Link) Disconnect(agentID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if stop, ok := l.stops[agentID]; ok {
		stop()
		delete(l.stops, agentID)
	}
}

// Close ends every flow and waits for them to return.
func (l *Link) Close() error {
	l.mu.Lock()
	l.closed = true
	for id, stop := range l.stops {
		stop()
		delete(l.stops, id)
	}
	l.mu.Unlock()
	l.wg.Wait()
	return nil
}

// Send delivers command to the Agent with agentID and waits for its reply. An
// Agent that does not answer is unreachable; one that refuses the command gives
// its reason in a *contractfleet.AgentError.
func (l *Link) Send(ctx context.Context, agentID string, command contractfleet.AgentCommand) error {
	l.mu.Lock()
	_, linked := l.stops[agentID]
	l.mu.Unlock()
	if !linked {
		return contractfleet.ErrAgentUnreachable
	}
	body, err := json.Marshal(command)
	if err != nil {
		return err
	}
	message, err := l.conn.RequestWithContext(ctx, contractfleet.AgentCommandSubject(agentID), body)
	switch {
	case errors.Is(err, nats.ErrNoResponders):
		return contractfleet.ErrAgentUnreachable
	case err != nil:
		return err
	}
	var reply contractfleet.AgentReply
	if err := json.Unmarshal(message.Data, &reply); err != nil {
		return err
	}
	if reply.Error != "" {
		return &contractfleet.AgentError{Message: reply.Error}
	}
	return nil
}

// consumeUsage delivers the Agent's runs from its USAGE stream, one at a time.
// A run is acknowledged only once events has kept it, so the Agent holds it
// until the database takes it.
func (l *Link) consumeUsage(ctx context.Context, agentID string, events func(contractfleet.AgentEvent) error) error {
	js, err := jetstream.NewWithDomain(l.conn, contractfleet.AgentDomain(agentID))
	if err != nil {
		return err
	}
	stream, err := js.Stream(ctx, contractfleet.AgentUsageStream)
	if err != nil {
		return err
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "fleet-" + agentID,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: contractfleet.AgentUsageSubject(agentID),
	})
	if err != nil {
		return err
	}
	consumed, err := consumer.Consume(func(message jetstream.Msg) {
		var report contractfleet.RunReport
		if err := json.Unmarshal(message.Data(), &report); err != nil {
			// A report that is not a report never becomes one; drop it.
			_ = message.Term()
			return
		}
		if err := events(contractfleet.AgentEvent{Usage: &report}); err != nil {
			_ = message.NakWithDelay(redeliverDelay)
			return
		}
		_ = message.Ack()
	})
	if err != nil {
		return err
	}
	defer consumed.Stop()
	<-ctx.Done()
	return nil
}

// watchState delivers each state the Agent writes under its ID in the state
// bucket. A later state supersedes one that events did not keep, so an error is
// not retried.
func (l *Link) watchState(ctx context.Context, agentID string, events func(contractfleet.AgentEvent) error) error {
	bucket, err := l.js.KeyValue(ctx, contractfleet.AgentStateBucket)
	if err != nil {
		return err
	}
	watcher, err := bucket.Watch(ctx, agentID)
	if err != nil {
		return err
	}
	defer watcher.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case entry, ok := <-watcher.Updates():
			if !ok {
				return errors.New("the state watch of the Agent ended")
			}
			if entry == nil || entry.Operation() != jetstream.KeyValuePut {
				continue
			}
			var state contractfleet.AgentState
			if err := json.Unmarshal(entry.Value(), &state); err != nil {
				continue
			}
			_ = events(contractfleet.AgentEvent{State: &state, At: entry.Created()})
		}
	}
}

// retry runs attempt until ctx ends, waiting longer after each failure up to
// retryMax. A flow that ran and ended on its own starts again at once.
func retry(ctx context.Context, attempt func() error) {
	delay := retryMin
	for ctx.Err() == nil {
		if err := attempt(); err == nil {
			delay = retryMin
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, retryMax)
	}
}
