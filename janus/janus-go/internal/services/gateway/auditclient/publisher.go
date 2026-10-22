// Package auditclient is the Audit adapter that publishes Audit Events to
// the AUDIT stream, for the audit service to write. Until it is connected, and
// whenever a publish fails, it writes the event through its own logger instead,
// so recording never fails the caller and no event is lost.
package auditclient

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	contract "janus/internal/contract/audit"
	"janus/internal/platform/auditlog"
	"janus/internal/platform/natsx"
)

// publishTimeout bounds waiting for JetStream to store one event.
const publishTimeout = 2 * time.Second

// Publisher records Audit Events. It is safe for concurrent use, including Connect
// and Disconnect while events are being recorded.
type Publisher struct {
	logger  *zap.Logger
	subject string
	js      atomic.Pointer[jetstream.JetStream]
}

// New returns a Publisher for the named service that writes through logger
// until it is connected.
func New(logger *zap.Logger, service string) *Publisher {
	return &Publisher{logger: logger, subject: contract.Subject(service)}
}

// Connect makes later events go to AUDIT through js. The stream must exist.
func (p *Publisher) Connect(js jetstream.JetStream) { p.js.Store(&js) }

// Disconnect makes later events go to the logger again.
func (p *Publisher) Disconnect() { p.js.Store(nil) }

// Record publishes event and waits until AUDIT has stored it. The caller's
// cancellation does not stop the publish. If the publish fails, the event is
// written locally, followed by a warning that says why.
func (p *Publisher) Record(ctx context.Context, event contract.Event) {
	js := p.js.Load()
	if js == nil {
		p.writeLocally(event)
		return
	}
	data, err := json.Marshal(contract.Event{Level: event.Level, Name: event.Name, Fields: event.Fields})
	if err == nil {
		msg := &nats.Msg{Subject: p.subject, Data: data}
		natsx.Inject(ctx, msg)
		publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
		_, err = (*js).PublishMsg(publishCtx, msg)
		cancel()
	}
	if err != nil {
		p.writeLocally(event)
		auditlog.Write(p.logger, "warn", "audit.publish.failed", map[string]string{"event": event.Name, "error": err.Error()})
	}
}

func (p *Publisher) writeLocally(event contract.Event) {
	auditlog.Write(p.logger, event.Level, event.Name, event.Fields)
}

// Flush writes out what the logger has buffered. Call it once, at shutdown.
func (p *Publisher) Flush() error {
	if p.logger == nil {
		return nil
	}
	return p.logger.Sync()
}
