package main

import (
	"context"
	"io"
	"reflect"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	contractaudit "janus/internal/contract/audit"
)

func TestTheComponentTableHasTheDocumentedGroupsInStartOrder(t *testing.T) {
	var names []string
	for _, g := range newApplication(nil, nil).groups() {
		names = append(names, g.name)
		if len(g.components) == 0 {
			t.Errorf("group %q has no components", g.name)
		}
	}
	want := []string{"foundation", "identity", "projects and builds", "runtime", "web"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("groups = %v, want %v", names, want)
	}
}

func TestNATSAndTheAuditServiceStartAfterTelemetry(t *testing.T) {
	var names []string
	for _, c := range newApplication(nil, nil).groups()[0].components {
		names = append(names, c.name)
	}
	want := []string{"audit", "telemetry", "nats", "audit service"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("foundation = %v, want %v", names, want)
	}
}

func TestTheNATSComponentEmbedsAServerAndStopsIt(t *testing.T) {
	a := newApplication(nil, nil)
	a.settings = settings{NATSAddress: "127.0.0.1:0", NATSDir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.startNATS(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.nats.Embedded() || !a.nats.Conn().IsConnected() {
		t.Fatalf("NATS embedded = %v, connected = %v, want both", a.nats.Embedded(), a.nats.Conn().IsConnected())
	}
	if err := a.stopNATS(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.nats.Conn().IsClosed() {
		t.Fatal("the connection is still open after stop")
	}
}

func TestAuditEventsGoThroughTheAuditServiceWhileItRuns(t *testing.T) {
	written := make(chan string, 10)
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(io.Discard), zapcore.DebugLevel)
	logger := zap.New(core, zap.Hooks(func(entry zapcore.Entry) error {
		written <- entry.Message
		return nil
	}))
	a := newApplication(logger, nil)
	a.settings = settings{NATSAddress: "127.0.0.1:0", NATSDir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, start := range []func(context.Context) error{a.startAudit, a.startNATS, a.startAuditService} {
		if err := start(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := <-written; got != "NATS is ready" {
		t.Fatalf("first log line %q", got)
	}

	a.audit.Record(ctx, contractaudit.Event{Level: "info", Name: "through.nats"})
	select {
	case got := <-written:
		if got != "through.nats" {
			t.Fatalf("written %q", got)
		}
	case <-ctx.Done():
		t.Fatal("the event was not written")
	}
	js, err := a.nats.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(ctx, "AUDIT")
	if err != nil || stream.CachedInfo().State.Msgs != 1 {
		t.Fatalf("AUDIT = %v, %v; want it to hold the one event published through it", stream, err)
	}

	if err := a.stopAuditService(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.stopNATS(ctx); err != nil {
		t.Fatal(err)
	}
}
