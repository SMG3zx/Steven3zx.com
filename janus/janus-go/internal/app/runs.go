package app

import (
	"context"
	"time"

	"janus/internal/domain/usage"
)

// Run is one execution of a WASM Build's module, as the single process reports
// it. Its ID and the Build are the runs service's.
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

// WASMRuns starts one-off runs of an Account's complete WASM Builds and reports
// on them, through the runs service. Request returns the ID of a run that happens
// in the background. A failure is a plain error whose text is meant for the
// Account, or a failure to reach the service. Get reports false for a run that is
// not kept or not the Account's.
type WASMRuns interface {
	Request(ctx context.Context, owner, buildID string, args []string) (string, error)
	Get(ctx context.Context, owner, runID string) (Run, bool, error)
}
