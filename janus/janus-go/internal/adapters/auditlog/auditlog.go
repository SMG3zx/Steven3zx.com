package auditlog

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"janus/internal/app"
)

// Log is the app.Audit that writes each event through its logger. zap loggers
// are safe for concurrent use, so Record needs no hand-off.
type Log struct{ logger *zap.Logger }

var _ app.Audit = (*Log)(nil)

// New returns a Log that writes events through logger. A nil logger gives a Log
// that drops every event.
func New(logger *zap.Logger) *Log { return &Log{logger: logger} }

// Record writes event at its level: error, warn and debug map to their zap
// levels and anything else is info. An info event with no name is logged under
// the name of its Go type.
func (l *Log) Record(_ context.Context, event app.AuditEvent) {
	if l.logger == nil {
		return
	}
	fields := make([]zap.Field, 0, len(event.Fields))
	for key, value := range event.Fields {
		fields = append(fields, zap.String(key, value))
	}
	switch event.Level {
	case "error":
		l.logger.Error(event.Name, fields...)
	case "warn":
		l.logger.Warn(event.Name, fields...)
	case "debug":
		l.logger.Debug(event.Name, fields...)
	default:
		if event.Name == "" {
			event.Name = fmt.Sprintf("%T", &event)
		}
		l.logger.Info(event.Name, fields...)
	}
}

// Flush writes out any buffered events. Call it once, at shutdown.
func (l *Log) Flush() error {
	if l.logger == nil {
		return nil
	}
	return l.logger.Sync()
}
