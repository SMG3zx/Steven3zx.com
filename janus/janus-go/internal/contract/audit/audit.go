// Package audit is the contract of the audit service: the subjects Audit
// Events are published on, the stream that keeps them, and the message.
package audit

import "strings"

const (
	// Stream keeps Audit Events until the audit service has written them.
	Stream = "AUDIT"
	// Subjects matches every Audit Event subject; the stream captures it.
	Subjects = "janus.audit.>"
	// Consumer is the audit service's durable consumer on Stream.
	Consumer = "audit"
)

// Subject is where service publishes its Audit Events. A service name is one
// subject token, so dots and wildcards are replaced.
func Subject(service string) string {
	token := strings.NewReplacer(".", "_", "*", "_", ">", "_", " ", "_").Replace(service)
	if token == "" {
		token = "unknown"
	}
	return "janus.audit." + token
}

// Event is one Audit Event as published. Level is "debug", "info", "warn" or
// "error"; the audit service writes anything else as "info". Fields must not
// hold passwords, tokens or other credentials.
type Event struct {
	Level  string            `json:"level"`
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields,omitempty"`
}
