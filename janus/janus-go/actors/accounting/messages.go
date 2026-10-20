package accounting

import "janus/internal/app"

// Event is an Audit Event, kept under its old name for callers of this package.
type Event = app.AuditEvent

type Flush struct{}

type Flushed struct{ Err error }
