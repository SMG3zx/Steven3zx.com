package gateway

import (
	"context"

	contractaudit "janus/internal/contract/audit"
)

// auditRecorder records Audit Events. Recording never fails the caller and never blocks
// it for long; an implementation that cannot write an event drops it.
type auditRecorder interface {
	Record(ctx context.Context, event contractaudit.Event)
}
