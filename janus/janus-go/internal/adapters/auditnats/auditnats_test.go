package auditnats

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"janus/internal/adapters/audittest"
	contractaudit "janus/internal/contract/audit"
	"janus/internal/platform/natstest"
	"janus/internal/services/audit"
)

// eventsOf reads the events a logger observed back as AuditEvents.
func eventsOf(logs *observer.ObservedLogs) func() []contractaudit.Event {
	return func() []contractaudit.Event {
		var events []contractaudit.Event
		for _, entry := range logs.All() {
			fields := map[string]string{}
			for key, value := range entry.ContextMap() {
				fields[key] = value.(string)
			}
			events = append(events, contractaudit.Event{Level: entry.Level.String(), Name: entry.Message, Fields: fields})
		}
		return events
	}
}

func TestPublisherThroughTheAuditServicePassesTheAuditContract(t *testing.T) {
	audittest.Run(t, func(t *testing.T) (audittest.Audit, func() []contractaudit.Event) {
		hub := natstest.Start(t)
		core, logs := observer.New(zapcore.DebugLevel)
		service := audit.New(audit.Config{Logger: zap.New(core)})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Start(ctx, hub.Conn()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = service.Stop(context.Background()) })
		js, err := hub.JetStream()
		if err != nil {
			t.Fatal(err)
		}
		// The publisher's own logger must stay silent: every event goes through
		// the service.
		local, localLogs := observer.New(zapcore.DebugLevel)
		t.Cleanup(func() {
			if n := localLogs.Len(); n != 0 {
				t.Errorf("%d events were written locally, want none", n)
			}
		})
		publisher := New(zap.New(local), "test")
		publisher.Connect(js)
		return publisher, eventsOf(logs)
	})
}

func TestAPublisherWritesLocallyUntilConnected(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	publisher := New(zap.New(core), "test")
	publisher.Record(context.Background(), contractaudit.Event{Level: "info", Name: "early", Fields: map[string]string{"k": "v"}})
	got := eventsOf(logs)()
	if len(got) != 1 || got[0].Name != "early" || got[0].Fields["k"] != "v" {
		t.Fatalf("written %+v", got)
	}
}

func TestAFailedPublishIsWrittenLocallyWithAWarning(t *testing.T) {
	hub := natstest.Start(t)
	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zapcore.DebugLevel)
	publisher := New(zap.New(core), "test")
	publisher.Connect(js) // AUDIT was never declared, so the publish fails.

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // A cancelled caller does not lose the event either.
	publisher.Record(ctx, contractaudit.Event{Level: "error", Name: "lost?"})

	got := eventsOf(logs)()
	if len(got) != 2 || got[0].Name != "lost?" || got[0].Level != "error" || got[1].Name != "audit.publish.failed" || got[1].Fields["event"] != "lost?" {
		t.Fatalf("written %+v", got)
	}
}

func TestDisconnectGoesBackToTheLogger(t *testing.T) {
	hub := natstest.Start(t)
	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zapcore.DebugLevel)
	publisher := New(zap.New(core), "test")
	publisher.Connect(js)
	publisher.Disconnect()
	publisher.Record(context.Background(), contractaudit.Event{Level: "info", Name: "after"})
	if got := eventsOf(logs)(); len(got) != 1 || got[0].Name != "after" {
		t.Fatalf("written %+v", got)
	}
}
