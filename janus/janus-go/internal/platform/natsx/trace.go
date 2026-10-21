package natsx

import (
	"context"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// propagator is fixed rather than global, so a message carries W3C trace
// context whether or not the process set up telemetry.
var propagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

// headerCarrier lets the propagator read and write NATS headers. Keys are used
// exactly as given, since NATS headers are not canonicalised on the wire.
type headerCarrier nats.Header

func (c headerCarrier) Get(key string) string {
	if values := c[key]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func (c headerCarrier) Set(key, value string) { c[key] = []string{value} }

func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for key := range c {
		keys = append(keys, key)
	}
	return keys
}

// Inject writes the trace context of ctx into msg's headers.
func Inject(ctx context.Context, msg *nats.Msg) {
	if msg.Header == nil {
		msg.Header = nats.Header{}
	}
	propagator.Inject(ctx, headerCarrier(msg.Header))
}

// Extract returns ctx carrying the trace context found in msg's headers.
func Extract(ctx context.Context, msg *nats.Msg) context.Context {
	if msg.Header == nil {
		return ctx
	}
	return propagator.Extract(ctx, headerCarrier(msg.Header))
}

func startSpan(ctx context.Context, operation, subject string, kind trace.SpanKind) (context.Context, trace.Span) {
	return otel.Tracer("janus/platform/natsx").Start(ctx, operation+" "+subject, trace.WithSpanKind(kind), trace.WithAttributes(
		attribute.String("messaging.system", "nats"),
		attribute.String("messaging.operation.name", operation),
		attribute.String("messaging.destination.name", subject),
	))
}

func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// Publish sends data on subject with the trace context of ctx.
func Publish(ctx context.Context, conn *nats.Conn, subject string, data []byte) error {
	ctx, span := startSpan(ctx, "publish", subject, trace.SpanKindProducer)
	msg := &nats.Msg{Subject: subject, Data: data}
	Inject(ctx, msg)
	err := conn.PublishMsg(msg)
	endSpan(span, err)
	return err
}

// Request sends data on subject with the trace context of ctx and waits for
// one reply until ctx ends.
func Request(ctx context.Context, conn *nats.Conn, subject string, data []byte) (*nats.Msg, error) {
	ctx, span := startSpan(ctx, "send", subject, trace.SpanKindClient)
	msg := &nats.Msg{Subject: subject, Data: data}
	Inject(ctx, msg)
	reply, err := conn.RequestMsgWithContext(ctx, msg)
	endSpan(span, err)
	return reply, err
}

// Handler handles one message. ctx continues the sender's trace.
type Handler func(ctx context.Context, msg *nats.Msg)

// Subscribe calls handle for each message on subject, in a span that
// continues the sender's trace.
func Subscribe(conn *nats.Conn, subject string, handle Handler) (*nats.Subscription, error) {
	return conn.Subscribe(subject, func(msg *nats.Msg) {
		ctx, span := startSpan(Extract(context.Background(), msg), "process", msg.Subject, trace.SpanKindConsumer)
		defer span.End()
		handle(ctx, msg)
	})
}
