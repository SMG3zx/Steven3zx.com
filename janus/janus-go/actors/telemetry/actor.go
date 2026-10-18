package telemetry

import (
	"context"
	"errors"
	"fmt"

	"github.com/grafana/pyroscope-go"
	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Actor configures telemetry providers and handles telemetry lifecycle messages.
type Actor struct {
	shutdown func(context.Context) error
	profiler *pyroscope.Profiler
}

var _ goakt.Actor = (*Actor)(nil)

// New creates a telemetry setup actor.
func New() *Actor { return &Actor{} }

func (*Actor) PreStart(*goakt.Context) error { return nil }

func (a *Actor) Receive(ctx *goakt.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *Setup:
		shutdown, err := Init(ctx.Context(), "janus-go")
		if err != nil {
			_, span := StartSpan(ctx.Context(), "telemetry.initialize", trace.WithSpanKind(trace.SpanKindInternal))
			RecordError(span, err)
			span.End()
			ctx.Response(&Ready{Err: err})
			return
		}
		a.shutdown = shutdown

		_, span := StartSpan(ctx.Context(), "pyroscope.start", trace.WithSpanKind(trace.SpanKindInternal))
		span.SetAttributes(
			attribute.String("pyroscope.application.name", "janus-go"),
			attribute.Bool("pyroscope.enabled", msg.PyroscopeServerAddress != ""),
		)
		profiler, err := pyroscope.Start(pyroscope.Config{
			ApplicationName: "janus-go",
			ServerAddress:   msg.PyroscopeServerAddress,
			Logger:          sdkLogger{accounting: msg.Accounting},
			ProfileTypes: []pyroscope.ProfileType{
				pyroscope.ProfileCPU,
				pyroscope.ProfileAllocObjects,
				pyroscope.ProfileAllocSpace,
				pyroscope.ProfileInuseObjects,
				pyroscope.ProfileInuseSpace,
			},
		})
		RecordError(span, err)
		span.End()
		if err != nil {
			_ = a.shutdown(ctx.Context())
			a.shutdown = nil
			ctx.Response(&Ready{Err: err})
			return
		}
		a.profiler = profiler
		_, readySpan := StartSpan(ctx.Context(), "telemetry.ready")
		readySpan.SetAttributes(attribute.String("service.name", "janus-go"))
		readySpan.End()
		ctx.Response(&Ready{})
	case *Shutdown:
		_, span := StartSpan(ctx.Context(), "pyroscope.stop", trace.WithSpanKind(trace.SpanKindInternal))
		var profileErr error
		if a.profiler != nil {
			profileErr = a.profiler.Stop()
			a.profiler = nil
		}
		RecordError(span, profileErr)
		span.End()

		err := profileErr
		if a.shutdown != nil {
			err = errors.Join(err, a.shutdown(msg.Context))
			a.shutdown = nil
		}
		ctx.Response(&Stopped{Err: err})
	default:
		ctx.Unhandled()
	}
}

func (*Actor) PostStop(*goakt.Context) error { return nil }

type sdkLogger struct{ accounting *goakt.PID }

func (l sdkLogger) Infof(format string, args ...interface{}) {
	l.log("info", format, args...)
}

func (l sdkLogger) Debugf(format string, args ...interface{}) {
	l.log("debug", format, args...)
}

func (l sdkLogger) Errorf(format string, args ...interface{}) {
	l.log("error", format, args...)
}

func (l sdkLogger) log(level, format string, args ...interface{}) {
	if l.accounting == nil {
		return
	}
	_ = goakt.Tell(context.Background(), l.accounting, &SDKLog{
		Level:   level,
		Message: fmt.Sprintf(format, args...),
	})
}
