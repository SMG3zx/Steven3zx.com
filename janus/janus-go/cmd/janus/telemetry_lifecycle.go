package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/grafana/pyroscope-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"janus/internal/app"
)

// telemetry is the running OpenTelemetry providers and Pyroscope profiler.
// Application startup owns it: it starts them once and stops them last, so the
// final spans and logs are flushed.
type telemetry struct {
	shutdown func(context.Context) error
	profiler *pyroscope.Profiler
}

// profilerLog receives the messages of the profiler's own SDK, with a level of "info",
// "debug" or "error". It may be nil.
type profilerLog func(level, message string)

// startTelemetry installs the OpenTelemetry providers for service "janus-go" and starts
// the Pyroscope profiler against pyroscopeAddress.
func startTelemetry(ctx context.Context, pyroscopeAddress string, log profilerLog) (*telemetry, error) {
	shutdown, err := initTelemetry(ctx, "janus-go")
	if err != nil {
		_, span := app.StartSpan(ctx, "telemetry.initialize", trace.WithSpanKind(trace.SpanKindInternal))
		app.RecordError(span, err)
		span.End()
		return nil, err
	}

	_, span := app.StartSpan(ctx, "pyroscope.start", trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(
		attribute.String("pyroscope.application.name", "janus-go"),
		attribute.Bool("pyroscope.enabled", pyroscopeAddress != ""),
	)
	profiler, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: "janus-go",
		ServerAddress:   pyroscopeAddress,
		Logger:          sdkLogger{log: log},
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
		},
	})
	app.RecordError(span, err)
	span.End()
	if err != nil {
		_ = shutdown(ctx)
		return nil, err
	}
	_, readySpan := app.StartSpan(ctx, "telemetry.ready")
	readySpan.SetAttributes(attribute.String("service.name", "janus-go"))
	readySpan.End()
	return &telemetry{shutdown: shutdown, profiler: profiler}, nil
}

// Stop stops the profiler and flushes and shuts down the providers.
func (t *telemetry) Stop(ctx context.Context) error {
	_, span := app.StartSpan(ctx, "pyroscope.stop", trace.WithSpanKind(trace.SpanKindInternal))
	var profileErr error
	if t.profiler != nil {
		profileErr = t.profiler.Stop()
		t.profiler = nil
	}
	app.RecordError(span, profileErr)
	span.End()

	err := profileErr
	if t.shutdown != nil {
		err = errors.Join(err, t.shutdown(ctx))
		t.shutdown = nil
	}
	return err
}

type sdkLogger struct{ log profilerLog }

func (l sdkLogger) Infof(format string, args ...interface{})  { l.write("info", format, args...) }
func (l sdkLogger) Debugf(format string, args ...interface{}) { l.write("debug", format, args...) }
func (l sdkLogger) Errorf(format string, args ...interface{}) { l.write("error", format, args...) }

func (l sdkLogger) write(level, format string, args ...interface{}) {
	if l.log != nil {
		l.log(level, fmt.Sprintf(format, args...))
	}
}
