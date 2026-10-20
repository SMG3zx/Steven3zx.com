// Package auditlog is the app.Audit adapter that writes Audit Events through a
// zap logger. Sensitive authentication and provider credentials should not be
// included in event data.
package auditlog
