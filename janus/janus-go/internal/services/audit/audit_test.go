package audit_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	contract "janus/internal/contract/audit"
	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
	"janus/internal/services/audit"
)

// written is a logger that hands each entry it writes to a channel, so a test
// waits for an event instead of polling for it.
type written struct {
	logger  *zap.Logger
	entries chan observer.LoggedEntry
	logs    *observer.ObservedLogs
}

func newWritten() *written {
	core, logs := observer.New(zapcore.DebugLevel)
	w := &written{entries: make(chan observer.LoggedEntry, 100), logs: logs}
	w.logger = zap.New(core, zap.Hooks(func(zapcore.Entry) error {
		all := logs.All()
		w.entries <- all[len(all)-1]
		return nil
	}))
	return w
}

func (w *written) next(t *testing.T) observer.LoggedEntry {
	t.Helper()
	select {
	case entry := <-w.entries:
		return entry
	case <-time.After(5 * time.Second):
		t.Fatal("no event written")
		return observer.LoggedEntry{}
	}
}

func startService(t *testing.T, hub *natsx.Hub, w *written) *audit.Service {
	t.Helper()
	service := audit.New(audit.Config{Logger: w.logger})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Start(ctx, hub.Conn()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Stop(ctx)
	})
	return service
}

func publish(t *testing.T, js jetstream.JetStream, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := js.Publish(ctx, contract.Subject("test"), data); err != nil {
		t.Fatal(err)
	}
}

func event(t *testing.T, e contract.Event) []byte {
	t.Helper()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func jetStream(t *testing.T, hub *natsx.Hub) jetstream.JetStream {
	t.Helper()
	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	return js
}

// settled waits until the consumer has nothing pending or unacknowledged.
func settled(t *testing.T, js jetstream.JetStream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	consumer, err := js.Consumer(ctx, contract.Stream, contract.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	for {
		info, err := consumer.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("consumer did not settle: %d pending, %d unacknowledged", info.NumPending, info.NumAckPending)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestAPublishedEventIsWrittenOnce(t *testing.T) {
	hub := natstest.Start(t)
	w := newWritten()
	startService(t, hub, w)
	js := jetStream(t, hub)

	publish(t, js, event(t, contract.Event{Level: "warn", Name: "build.failed", Fields: map[string]string{"build": "b1"}}))
	got := w.next(t)
	if got.Level != zapcore.WarnLevel || got.Message != "build.failed" || got.ContextMap()["build"] != "b1" {
		t.Fatalf("written %+v", got)
	}
	settled(t, js)
	if n := w.logs.Len(); n != 1 {
		t.Fatalf("written %d times, want once", n)
	}
}

func TestEventsPublishedWhileStoppedAreWrittenOnStart(t *testing.T) {
	hub := natstest.Start(t)
	js := jetStream(t, hub)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := audit.DeclareStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	publish(t, js, event(t, contract.Event{Level: "info", Name: "first"}))
	publish(t, js, event(t, contract.Event{Level: "info", Name: "second"}))

	w := newWritten()
	startService(t, hub, w)
	if got := w.next(t).Message; got != "first" {
		t.Fatalf("first written %q", got)
	}
	if got := w.next(t).Message; got != "second" {
		t.Fatalf("second written %q", got)
	}
}

func TestARestartDoesNotWriteEventsAgain(t *testing.T) {
	hub := natstest.Start(t)
	js := jetStream(t, hub)
	w := newWritten()
	service := audit.New(audit.Config{Logger: w.logger})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Start(ctx, hub.Conn()); err != nil {
		t.Fatal(err)
	}
	publish(t, js, event(t, contract.Event{Level: "info", Name: "before"}))
	w.next(t)
	settled(t, js)
	if err := service.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	startService(t, hub, w)
	publish(t, js, event(t, contract.Event{Level: "info", Name: "after"}))
	if got := w.next(t).Message; got != "after" {
		t.Fatalf("after restart wrote %q, want only the new event", got)
	}
}

func TestAMalformedMessageIsReportedAndNotRedelivered(t *testing.T) {
	hub := natstest.Start(t)
	w := newWritten()
	startService(t, hub, w)
	js := jetStream(t, hub)

	publish(t, js, []byte("not json"))
	got := w.next(t)
	if got.Level != zapcore.ErrorLevel || got.Message != "audit.event.malformed" || got.ContextMap()["subject"] != contract.Subject("test") {
		t.Fatalf("written %+v", got)
	}
	settled(t, js)
	if n := w.logs.Len(); n != 1 {
		t.Fatalf("written %d times, want once", n)
	}
}

func TestDeclaringTheStreamTwiceChangesNothing(t *testing.T) {
	js := jetStream(t, natstest.Start(t))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 2 {
		stream, err := audit.DeclareStream(ctx, js)
		if err != nil {
			t.Fatal(err)
		}
		if cfg := stream.CachedInfo().Config; cfg.MaxAge != audit.Retention || cfg.Subjects[0] != contract.Subjects {
			t.Fatalf("stream config %+v", cfg)
		}
	}
}
