package wasmruntime

import (
	"context"

	"janus/internal/app"
)

// Wasmtime is the app.Runner backed by the CGO Wasmtime runtime. In a build
// without CGO every Run fails. A Run is bounded by limits.Timeout, not by ctx.
type Wasmtime struct{}

var _ app.Runner = Wasmtime{}

// Run executes module as a WASI command with args under limits.
func (Wasmtime) Run(_ context.Context, module []byte, args []string, limits app.RunLimits) (app.RunResult, error) {
	stdout, stderr, usage, err := ExecuteWASIWith(module, args, limits)
	return app.RunResult{Stdout: stdout, Stderr: stderr, Usage: usage}, err
}
