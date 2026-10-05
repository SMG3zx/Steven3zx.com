// Package monolog writes small, structured JSON Lines events for repository tools.
// Applications should keep using their native logging APIs and map fields into
// this format at their process boundary.
package monolog

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Level is the severity of an event.
type Level string

const (
	Debug Level = "debug"
	Info  Level = "info"
	Warn  Level = "warn"
	Error Level = "error"
)

// Event is the shared JSONL envelope. Fields must contain operational metadata,
// not request bodies, credentials, user content, or other sensitive values.
type Event struct {
	Time      time.Time      `json:"time"`
	Level     Level          `json:"level"`
	Service   string         `json:"service"`
	Message   string         `json:"message"`
	TraceID   string         `json:"trace_id,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Logger writes one JSON object per line. It is safe for concurrent use.
type Logger struct {
	mu      sync.Mutex
	encoder *json.Encoder
	service string
	clock   func() time.Time
}

// New creates a logger that writes to output. An empty service is normalized to
// "unknown" so every event remains attributable.
func New(output io.Writer, service string) *Logger {
	if output == nil {
		output = io.Discard
	}
	service = strings.TrimSpace(service)
	if service == "" {
		service = "unknown"
	}
	return &Logger{encoder: json.NewEncoder(output), service: service, clock: time.Now}
}

// Default creates a logger writing to standard error for service.
func Default(service string) *Logger { return New(os.Stderr, service) }

// Log writes an event. Unknown levels are normalized to info. Caller-owned
// fields are copied so subsequent map changes cannot affect the encoded event.
func (l *Logger) Log(level Level, message string, fields map[string]any) error {
	if l == nil || l.encoder == nil {
		return nil
	}
	if !validLevel(level) {
		level = Info
	}
	var copied map[string]any
	if len(fields) > 0 {
		copied = make(map[string]any, len(fields))
		for key, value := range fields {
			copied[key] = value
		}
	}
	event := Event{Time: l.clock().UTC(), Level: level, Service: l.service, Message: message, Fields: copied}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.encoder.Encode(event)
}

// Debug writes a debug event.
func (l *Logger) Debug(message string, fields map[string]any) error {
	return l.Log(Debug, message, fields)
}

// Info writes an informational event.
func (l *Logger) Info(message string, fields map[string]any) error {
	return l.Log(Info, message, fields)
}

// Warn writes a warning event.
func (l *Logger) Warn(message string, fields map[string]any) error {
	return l.Log(Warn, message, fields)
}

// Error writes an error event.
func (l *Logger) Error(message string, fields map[string]any) error {
	return l.Log(Error, message, fields)
}

func validLevel(level Level) bool {
	switch level {
	case Debug, Info, Warn, Error:
		return true
	default:
		return false
	}
}
