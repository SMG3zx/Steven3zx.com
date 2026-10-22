// Package audittest holds an in-memory Audit and the contract suite every
// Audit, real or test, must pass.
package audittest

import (
	"context"
	"maps"
	"slices"
	"sync"

	contractaudit "janus/internal/contract/audit"
)

// Recorder is an audit recorder that keeps the events it is given, in order, for a
// test to inspect. It is safe for concurrent use.
type Recorder struct {
	mu     sync.Mutex
	events []contractaudit.Event
}

var _ recorder = (*Recorder)(nil)

// NewRecorder returns a Recorder with no events.
func NewRecorder() *Recorder { return &Recorder{} }

// Record keeps a copy of event. A Level other than debug, info, warn or error
// is kept as "info", and an empty Name as "audit.event".
func (r *Recorder) Record(_ context.Context, event contractaudit.Event) {
	switch event.Level {
	case "debug", "info", "warn", "error":
	default:
		event.Level = "info"
	}
	if event.Name == "" {
		event.Name = "audit.event"
	}
	event.Fields = maps.Clone(event.Fields)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

// Events returns the events recorded so far, oldest first.
func (r *Recorder) Events() []contractaudit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// recorder is where Audit Events are recorded: what the Recorder is, and what the
// contract suite runs against. Recording never fails the caller and never blocks
// it for long; an implementation that cannot write an event drops it. An event
// with no Level and no Name is recorded as info under a generic name, and every
// Field is recorded.
type recorder interface {
	Record(ctx context.Context, event contractaudit.Event)
}
