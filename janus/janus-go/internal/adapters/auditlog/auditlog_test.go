package auditlog

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"janus/internal/adapters/audittest"
	"janus/internal/app"
)

func TestLogPassesTheAuditContract(t *testing.T) {
	audittest.Run(t, func(t *testing.T) (app.Audit, func() []app.AuditEvent) {
		core, logs := observer.New(zapcore.DebugLevel)
		return New(zap.New(core)), func() []app.AuditEvent {
			var events []app.AuditEvent
			for _, entry := range logs.All() {
				fields := map[string]string{}
				for key, value := range entry.ContextMap() {
					fields[key] = value.(string)
				}
				events = append(events, app.AuditEvent{Level: entry.Level.String(), Name: entry.Message, Fields: fields})
			}
			return events
		}
	})
}

func TestLogWithoutALoggerDropsEvents(t *testing.T) {
	log := New(nil)
	log.Record(context.Background(), app.AuditEvent{Level: "info", Name: "nobody listens"})
	if err := log.Flush(); err != nil {
		t.Fatal(err)
	}
}
