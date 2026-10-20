// Package jobs holds the state machines of Janus jobs. Persistence adapters
// compare and swap the stored status; whether a move is legal is decided here
// and nowhere else.
package jobs

import "slices"

// Machine is a job state machine: for each non-terminal status, the statuses a
// job may move to. Terminal statuses have no entry.
type Machine map[string][]string

// Import is the state machine for repository import jobs. A job that was
// interrupted returns to queued so that it can resume; terminal statuses
// (complete, failed, cancelled) have no outgoing transitions. A resumed import
// whose Staging Snapshot already lists every file skips straight to storing,
// and one that needs only some files skips scanning.
var Import = Machine{
	"queued":   {"cloning", "storing", "failed", "cancelled"},
	"cloning":  {"scanning", "storing", "queued", "failed", "cancelled"},
	"scanning": {"storing", "queued", "failed", "cancelled"},
	"storing":  {"complete", "queued", "failed", "cancelled"},
}

// WASM is the state machine for WASM build jobs.
var WASM = Machine{
	"queued":   {"building", "failed", "cancelled"},
	"building": {"complete", "queued", "failed", "cancelled"},
}

// CanMove reports whether a job may move from one status to another. Staying
// in the same non-terminal status is allowed so that progress can be reported.
func (m Machine) CanMove(from, to string) bool {
	if from == to {
		_, ok := m[from]
		return ok
	}
	return slices.Contains(m[from], to)
}
