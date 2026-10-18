package accounting

import (
	"fmt"

	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	actortrace "janus/actors/telemetry"
)

// Actor writes structured audit events through its configured logger.
type Actor struct{ logger *zap.Logger }

var _ goakt.Actor = (*Actor)(nil)

// New creates an accounting actor that writes events through logger.
func New(logger *zap.Logger) *Actor { return &Actor{logger: logger} }

func (*Actor) PreStart(*goakt.Context) error { return nil }

func (a *Actor) Receive(rctx *goakt.ReceiveContext) {
	_, span := actortrace.StartSpan(rctx.Context(), "actor.accounting.receive", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	span.SetAttributes(attribute.String("actor.type", "accounting"))
	switch msg := rctx.Message().(type) {
	case *Flush:
		rctx.Response(&Flushed{Err: a.logger.Sync()})
	case *Event:
		fields := make([]zap.Field, 0, len(msg.Fields))
		for key, value := range msg.Fields {
			fields = append(fields, zap.String(key, value))
		}
		switch msg.Level {
		case "error":
			a.logger.Error(msg.Name, fields...)
		case "warn":
			a.logger.Warn(msg.Name, fields...)
		case "debug":
			a.logger.Debug(msg.Name, fields...)
		default:
			if msg.Name == "" {
				msg.Name = fmt.Sprintf("%T", rctx.Message())
			}
			a.logger.Info(msg.Name, fields...)
		}
	case *actortrace.SDKLog:
		field := zap.String("message", msg.Message)
		switch msg.Level {
		case "error":
			a.logger.Error("pyroscope.sdk", field)
		case "debug":
			a.logger.Debug("pyroscope.sdk", field)
		default:
			a.logger.Info("pyroscope.sdk", field)
		}
	default:
		rctx.Unhandled()
	}
}

func (*Actor) PostStop(*goakt.Context) error { return nil }
