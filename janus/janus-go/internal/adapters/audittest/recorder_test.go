package audittest

import (
	"testing"

	"janus/internal/app"
)

func TestRecorderPassesTheAuditContract(t *testing.T) {
	Run(t, func(*testing.T) (app.Audit, func() []app.AuditEvent) {
		r := NewRecorder()
		return r, r.Events
	})
}
