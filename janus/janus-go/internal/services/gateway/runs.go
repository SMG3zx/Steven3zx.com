package gateway

import (
	"context"

	contractruns "janus/internal/contract/runs"
)

// runsService is the runs service as the gateway reaches it: one-off runs of an
// Account's complete WASM Builds. Request returns the ID of a run that happens in
// the background. A refusal is a plain error whose text is meant for the Account.
// Get reports false for a run that is not kept or not the Account's.
type runsService interface {
	Request(ctx context.Context, owner, buildID string, args []string) (string, error)
	Get(ctx context.Context, owner, runID string) (contractruns.Run, bool, error)
}
