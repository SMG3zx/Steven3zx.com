// Package wasmtime is the app.Runner adapter that executes WASI modules with the
// Wasmtime runtime (cgo; a build without cgo reports the runtime as unavailable).
package wasmtime

import (
	"context"

	"janus/internal/app"
)

// Wasmtime is the app.Runner backed by the CGO Wasmtime runtime. In a build
// without CGO every Run fails. A Run ends at input.Limits.Timeout or when ctx
// ends.
type Wasmtime struct{}

var _ app.Runner = Wasmtime{}

// Run executes module as a WASI command with input.
func (Wasmtime) Run(ctx context.Context, module []byte, input app.RunInput) (app.RunResult, error) {
	return execute(ctx, module, input)
}

// Available reports whether this build can run modules at all: it is false
// without CGO, where every Run would fail.
func (Wasmtime) Available() bool { return Available() }
