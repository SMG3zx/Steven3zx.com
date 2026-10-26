// Package wasmrun runs WASI modules with the Wasmtime runtime (cgo; a build
// without cgo reports the runtime as unavailable). Services and adapters both
// use it, so it imports no service, contract or app package.
package wasmrun

import (
	"context"
	"errors"

	"janus/internal/domain/templates"
)

// Input is what a module starts with: its arguments, its environment as
// "NAME=value" entries, its standard input, the host directory it may use, and
// its limits. A zero MaxStdout means the default bound on output.
type Input struct {
	Args, Env []string
	Stdin     []byte
	Dir       string
	MaxStdout int
	Limits    templates.Resources
	// Grants are the capabilities this run may use. The zero value grants nothing, so
	// only a caller that sets it can give a module more than WASI: the agent does, the
	// control plane's runs service never does.
	Grants []Grant
	// Audit receives what the granted capabilities do. It may be nil.
	Audit AuditFunc
	// RawNetOpener opens packet captures for CapabilityRawNet. Nil uses the operating
	// system's; tests and privileged helpers set it.
	RawNetOpener PacketOpener
}

// Result is what a module produced. Stdout and Stderr are bounded, and
// StdoutTruncated reports that Stdout was cut. Usage is filled in whenever the
// module started running, including when it traps, so a run that exhausts its
// fuel still reports the fuel it spent.
type Result struct {
	Stdout, Stderr  []byte
	StdoutTruncated bool
	Usage           Usage
}

// ErrOutOfFuel and ErrRunTimeout are what Run's error matches when a run spent
// all its fuel or ran out of time.
var (
	ErrOutOfFuel  = errors.New("all fuel consumed by WebAssembly")
	ErrRunTimeout = errors.New("the WebAssembly run timed out")
)

// Run executes module as a WASI command with input. The run stops at
// input.Limits.Timeout or when ctx ends, whichever comes first. Without cgo
// every Run fails.
func Run(ctx context.Context, module []byte, input Input) (Result, error) {
	return execute(ctx, module, input)
}
