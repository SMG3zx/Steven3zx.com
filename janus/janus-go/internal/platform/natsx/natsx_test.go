package natsx_test

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
)

// recordSpans installs a tracer provider that keeps every ended span.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	return recorder
}

func TestPublishContinuesTheTraceInTheSubscriber(t *testing.T) {
	recordSpans(t)
	conn := natstest.Start(t).Conn()
	got := make(chan trace.SpanContext, 1)
	sub, err := natsx.Subscribe(conn, "janus.test.event", func(ctx context.Context, _ *nats.Msg) {
		got <- trace.SpanContextFromContext(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ctx, parent := otel.Tracer("test").Start(context.Background(), "parent")
	if err := natsx.Publish(ctx, conn, "janus.test.event", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	parent.End()

	select {
	case received := <-got:
		if received.TraceID() != parent.SpanContext().TraceID() {
			t.Fatalf("subscriber trace %s, want %s", received.TraceID(), parent.SpanContext().TraceID())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message received")
	}
}

func TestRequestContinuesTheTraceInTheResponder(t *testing.T) {
	recorder := recordSpans(t)
	conn := natstest.Start(t).Conn()
	sub, err := natsx.Subscribe(conn, "janus.test.echo", func(ctx context.Context, msg *nats.Msg) {
		_ = msg.Respond([]byte(trace.SpanContextFromContext(ctx).TraceID().String()))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ctx, parent := otel.Tracer("test").Start(context.Background(), "parent")
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	reply, err := natsx.Request(ctx, conn, "janus.test.echo", nil)
	parent.End()
	if err != nil {
		t.Fatal(err)
	}
	if string(reply.Data) != parent.SpanContext().TraceID().String() {
		t.Fatalf("responder trace %s, want %s", reply.Data, parent.SpanContext().TraceID())
	}
	names := map[string]bool{}
	for _, span := range recorder.Ended() {
		names[span.Name()] = true
	}
	for _, want := range []string{"send janus.test.echo", "process janus.test.echo"} {
		if !names[want] {
			t.Errorf("no span %q in %v", want, names)
		}
	}
}

func TestEmbeddedServerRunsJetStreamInTheHubDomain(t *testing.T) {
	hub := natstest.Start(t)
	if !hub.Embedded() {
		t.Fatal("natstest server is not embedded")
	}
	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := js.AccountInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Domain != natsx.HubDomain {
		t.Fatalf("JetStream domain %q, want %q", info.Domain, natsx.HubDomain)
	}
}

func TestStartConnectsToAnExternalServer(t *testing.T) {
	embedded := natstest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	external, err := natsx.Start(ctx, natsx.Config{URL: embedded.URL(), Name: "external"})
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close(ctx)
	if external.Embedded() {
		t.Fatal("a hub with a URL embedded a server")
	}
	if external.URL() != embedded.URL() {
		t.Fatalf("connected to %s, want %s", external.URL(), embedded.URL())
	}
}

func TestStartRefusesABadConfiguration(t *testing.T) {
	ctx := context.Background()
	for name, cfg := range map[string]natsx.Config{
		"no store":    {},
		"bad address": {StoreDir: t.TempDir(), Address: "localhost"},
		"bad port":    {StoreDir: t.TempDir(), Address: "127.0.0.1:99999"},
		"unreachable": {URL: "nats://127.0.0.1:1"},
	} {
		t.Run(name, func(t *testing.T) {
			if hub, err := natsx.Start(ctx, cfg); err == nil {
				_ = hub.Close(ctx)
				t.Fatal("Start succeeded")
			}
		})
	}
}
