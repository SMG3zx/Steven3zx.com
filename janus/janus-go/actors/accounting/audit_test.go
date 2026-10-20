package accounting

import (
	"context"
	"testing"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"janus/internal/adapters/audittest"
	"janus/internal/app"
)

func TestAuditPassesTheAuditContract(t *testing.T) {
	audittest.Run(t, func(t *testing.T) (app.Audit, func() []app.AuditEvent) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		t.Cleanup(cancel)
		system, err := goakt.NewActorSystem("accounting-audit-test", goakt.WithLogger(golog.DiscardLogger))
		if err != nil {
			t.Fatal(err)
		}
		if err = system.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			stop, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			_ = system.Stop(stop)
		})
		core, logs := observer.New(zapcore.DebugLevel)
		pid, err := system.Spawn(ctx, "accounting", New(zap.New(core)))
		if err != nil {
			t.Fatal(err)
		}
		return NewAudit(pid), func() []app.AuditEvent {
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

func TestAuditWithoutAnActorDropsEvents(t *testing.T) {
	NewAudit(nil).Record(context.Background(), app.AuditEvent{Level: "info", Name: "nobody listens"})
}
