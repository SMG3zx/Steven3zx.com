// Package audittest holds an in-memory app.Audit and the contract suite every
// app.Audit adapter, real or test, must pass.
package audittest

import (
	"context"
	"maps"
	"slices"
	"sync"

	"janus/internal/app"
)

// Recorder is an app.Audit that keeps the events it is given, in order, for a
// test to inspect. It is safe for concurrent use.
type Recorder struct {
	mu     sync.Mutex
	events []app.AuditEvent
}

var _ app.Audit = (*Recorder)(nil)

// NewRecorder returns a Recorder with no events.
func NewRecorder() *Recorder { return &Recorder{} }

// Record keeps a copy of event. A Level other than debug, info, warn or error
// is kept as "info", and an empty Name as "audit.event".
func (r *Recorder) Record(_ context.Context, event app.AuditEvent) {
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
func (r *Recorder) Events() []app.AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}
