package natsx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nats-io/nats.go/micro"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
)

type greeting struct {
	Name string `json:"name"`
}

type statusError struct{ status int }

func (e statusError) Error() string   { return "chosen status" }
func (e statusError) StatusCode() int { return e.status }

func startGreeter(t *testing.T) *natsx.Hub {
	t.Helper()
	hub := natstest.Start(t)
	service, err := micro.AddService(hub.Conn(), micro.Config{Name: "greeter", Version: "0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Stop() })
	err = natsx.AddEndpoint(service, "greet", "janus.test.greet", func(ctx context.Context, in greeting) (greeting, error) {
		switch in.Name {
		case "":
			return greeting{}, statusError{404}
		case "boom":
			return greeting{}, errors.New("plain failure")
		case "trace":
			return greeting{Name: trace.SpanContextFromContext(ctx).TraceID().String()}, nil
		}
		return greeting{Name: "hello " + in.Name}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hub
}

func TestCallGetsTheDecodedReply(t *testing.T) {
	hub := startGreeter(t)
	var out greeting
	if err := natsx.Call(context.Background(), hub.Conn(), "janus.test.greet", greeting{Name: "ada"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "hello ada" {
		t.Fatalf("reply %q", out.Name)
	}
}

func TestCallReportsTheStatusTheHandlerChose(t *testing.T) {
	hub := startGreeter(t)
	for name, want := range map[string]int{"": 404, "boom": 500} {
		var remote *natsx.RemoteError
		err := natsx.Call(context.Background(), hub.Conn(), "janus.test.greet", greeting{Name: name}, nil)
		if !errors.As(err, &remote) || remote.Status != want {
			t.Errorf("name %q: err = %v, want status %d", name, err, want)
		}
	}
}

func TestAMalformedRequestIsInvalid(t *testing.T) {
	hub := startGreeter(t)
	var remote *natsx.RemoteError
	err := natsx.Call(context.Background(), hub.Conn(), "janus.test.greet", "not an object", nil)
	if !errors.As(err, &remote) || remote.Status != 400 {
		t.Fatalf("err = %v, want 400", err)
	}
}

func TestNobodyAnsweringIsUnavailable(t *testing.T) {
	hub := natstest.Start(t)
	var remote *natsx.RemoteError
	err := natsx.Call(context.Background(), hub.Conn(), "janus.test.nobody", greeting{}, nil)
	if !errors.As(err, &remote) || remote.Status != 503 {
		t.Fatalf("err = %v, want 503", err)
	}
}

func TestAnEndpointContinuesTheCallersTrace(t *testing.T) {
	recordSpans(t)
	hub := startGreeter(t)
	ctx, parent := otel.Tracer("test").Start(context.Background(), "parent")
	defer parent.End()
	var out greeting
	if err := natsx.Call(ctx, hub.Conn(), "janus.test.greet", greeting{Name: "trace"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != parent.SpanContext().TraceID().String() {
		t.Fatalf("endpoint trace %s, want %s", out.Name, parent.SpanContext().TraceID())
	}
}
