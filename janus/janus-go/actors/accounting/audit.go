package accounting

import (
	"context"

	goakt "github.com/tochemey/goakt/v4/actor"

	"janus/internal/app"
)

// Audit is the app.Audit that hands each event to an accounting actor, which
// writes it through its logger. Recording is a Tell, so it never waits for the
// write; an event that cannot be delivered is dropped.
type Audit struct{ pid *goakt.PID }

var _ app.Audit = (*Audit)(nil)

// NewAudit returns an Audit that sends events to the accounting actor pid. A
// nil pid gives an Audit that drops every event.
func NewAudit(pid *goakt.PID) *Audit { return &Audit{pid: pid} }

// Record sends event to the accounting actor.
func (a *Audit) Record(ctx context.Context, event app.AuditEvent) {
	if a.pid == nil {
		return
	}
	_ = goakt.Tell(ctx, a.pid, &event)
}
