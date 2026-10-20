package app

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var actorTracer = otel.Tracer("github.com/SMG3zx/Janus/actors")

// StartSpan starts an actor/application span using the shared instrumentation scope.
// StartSpan starts a span using Janus's shared instrumentation scope.
func StartSpan(ctx context.Context, name string, options ...trace.SpanStartOption) (context.Context, trace.Span) {
	return StartSpanWithScope(ctx, actorTracer, name, options...)
}

// StartSpanWithScope starts a span and enforces Jaeger's microsecond minimum duration.
// StartSpanWithScope starts a span with tracer and applies Janus's minimum
// duration adjustment for Jaeger visibility.
func StartSpanWithScope(ctx context.Context, tracer trace.Tracer, name string, options ...trace.SpanStartOption) (context.Context, trace.Span) {
	startedAt := time.Now()
	options = append(options, trace.WithTimestamp(startedAt))
	ctx, span := tracer.Start(ctx, name, options...)
	return ctx, minimumDurationSpan{Span: span, startedAt: startedAt}
}

// RecordError attaches an error to a span and marks it as failed.
// RecordError attaches err to span and marks the span as failed. A nil error is ignored.
func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

type minimumDurationSpan struct {
	trace.Span
	startedAt time.Time
}

func (s minimumDurationSpan) End(options ...trace.SpanEndOption) {
	elapsed := time.Since(s.startedAt)
	if elapsed < time.Microsecond {
		elapsed = time.Microsecond
	}
	options = append(options, trace.WithTimestamp(s.startedAt.Add(elapsed)))
	s.Span.End(options...)
}
