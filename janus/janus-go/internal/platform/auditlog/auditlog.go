// Package auditlog writes one Audit Event through a zap logger. The audit
// service writes the events it consumes with it, and a publisher writes with it
// the events it could not publish, so both look the same in the log.
package auditlog

import "go.uber.org/zap"

// DefaultName is the name an event with no name is written under.
const DefaultName = "audit.event"

// Write writes an event at its level: error, warn and debug map to their zap
// levels and anything else is info. A nil logger drops the event.
func Write(logger *zap.Logger, level, name string, fields map[string]string) {
	if logger == nil {
		return
	}
	if name == "" {
		name = DefaultName
	}
	zapFields := make([]zap.Field, 0, len(fields))
	for key, value := range fields {
		zapFields = append(zapFields, zap.String(key, value))
	}
	switch level {
	case "error":
		logger.Error(name, zapFields...)
	case "warn":
		logger.Warn(name, zapFields...)
	case "debug":
		logger.Debug(name, zapFields...)
	default:
		logger.Info(name, zapFields...)
	}
}
