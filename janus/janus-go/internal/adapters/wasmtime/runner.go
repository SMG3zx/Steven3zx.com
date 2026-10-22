// Package wasmtime is the agent.Runner adapter that executes WASI modules with the
// Wasmtime runtime of platform/wasmrun (cgo; a build without cgo reports the
// runtime as unavailable).
package wasmtime

import (
	"context"
	"errors"

	"janus/internal/domain/agent"
	"janus/internal/platform/wasmrun"
)

// Wasmtime is the agent.Runner backed by the CGO Wasmtime runtime. In a build
// without CGO every Run fails. A Run ends at input.Limits.Timeout or when ctx
// ends.
type Wasmtime struct{}

var _ agent.Runner = Wasmtime{}

// Run executes module as a WASI command with input.
func (Wasmtime) Run(ctx context.Context, module []byte, input agent.RunInput) (agent.RunResult, error) {
	result, err := wasmrun.Run(ctx, module, wasmrun.Input{Args: input.Args, Env: input.Env, Stdin: input.Stdin,
		Dir: input.Dir, MaxStdout: input.MaxStdout, Limits: input.Limits})
	out := agent.RunResult{Stdout: result.Stdout, Stderr: result.Stderr, StdoutTruncated: result.StdoutTruncated, Usage: result.Usage}
	switch {
	case errors.Is(err, wasmrun.ErrOutOfFuel):
		err = agent.ErrOutOfFuel
	case errors.Is(err, wasmrun.ErrRunTimeout):
		err = agent.ErrRunTimeout
	}
	return out, err
}

// Available reports whether this build can run modules at all: it is false
// without CGO, where every Run would fail.
func (Wasmtime) Available() bool { return Available() }

// Available reports whether this build includes the CGO-backed Wasmtime runtime.
func Available() bool { return wasmrun.Available() }
