// Package runs is the contract of the runs service: one-off runs of an Account's
// complete WASI WebAssembly Builds, executed with Wasmtime.
//
// Every request about an Owner's runs carries owner_id, and a run that is not the
// Owner's is answered as absent. A Build that is not the Owner's is answered as
// not found. Failures are contract.Error values: StatusNotFound for an absent
// Build, StatusConflict when two runs are already active, StatusInvalid for a
// request that cannot be carried out as asked, StatusUnavailable when the runtime
// is not built in or the service is shutting down. The message is a sentence for
// the Account.
package runs

import (
	"time"

	"janus/internal/domain/usage"
)

// ServiceName is the service's name in NATS service discovery.
const ServiceName = "runs"

// The subjects the runs service answers.
const (
	SubjectRequestRun = "janus.runs.run.request"
	SubjectGetRun     = "janus.runs.run.get"
)

// The statuses a run ends with, or is in while it runs.
const (
	StatusRunning  = "running"
	StatusComplete = "complete"
	StatusFailed   = "failed"
)

// RequestRun asks for a run of a complete WASI Build of the Owner. Args are the
// WASI arguments of the module.
type RequestRun struct {
	OwnerID string   `json:"owner_id"`
	BuildID string   `json:"build_id"`
	Args    []string `json:"args,omitempty"`
}

// RunAccepted answers SubjectRequestRun. The run itself happens in the background.
type RunAccepted struct {
	RunID string `json:"run_id"`
}

// RunKey names one run of an Owner.
type RunKey struct {
	OwnerID string `json:"owner_id"`
	RunID   string `json:"run_id"`
}

// Run is one execution of a WASM Build's module. Stdout and Stderr are bounded.
type Run struct {
	ID        string      `json:"id"`
	BuildID   string      `json:"build_id"`
	Module    string      `json:"module,omitempty"`
	Status    string      `json:"status"`
	Stdout    string      `json:"stdout,omitempty"`
	Stderr    string      `json:"stderr,omitempty"`
	Error     string      `json:"error,omitempty"`
	Usage     usage.Usage `json:"usage"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// RunFound answers SubjectGetRun. Found is false when the run is not kept or is
// not the Owner's.
type RunFound struct {
	Found bool `json:"found"`
	Run   Run  `json:"run"`
}
