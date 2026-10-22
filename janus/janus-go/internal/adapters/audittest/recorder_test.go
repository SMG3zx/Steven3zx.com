package audittest

import (
	"testing"

	contractaudit "janus/internal/contract/audit"
)

func TestRecorderPassesTheAuditContract(t *testing.T) {
	Run(t, func(*testing.T) (Audit, func() []contractaudit.Event) {
		r := NewRecorder()
		return r, r.Events
	})
}
