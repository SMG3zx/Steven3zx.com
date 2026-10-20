// Package runnertest holds an in-memory app.Runner, WASI modules to run in its
// contract suite, and the suite every app.Runner adapter, real or test, must
// pass.
package runnertest

import (
	"bytes"
	"context"
	"errors"
	"sync"

	"janus/internal/app"
)

// Fake is an app.Runner that runs no WebAssembly. It knows the fixture modules of
// this package and any module a test registers with Handle, and refuses the
// rest. It is safe for concurrent use.
type Fake struct {
	mu       sync.Mutex
	programs map[string]Program
}

// Program is what a registered module does when it runs. It returns the Run's
// output and Usage and the error the run ends with.
type Program func(args []string, limits app.RunLimits) (app.RunResult, error)

var _ app.Runner = (*Fake)(nil)

// NewFake returns a Fake that runs the fixture modules Exits, Hello and Spins.
func NewFake() *Fake {
	f := &Fake{programs: map[string]Program{}}
	f.Handle(Exits, func([]string, app.RunLimits) (app.RunResult, error) {
		return app.RunResult{Usage: usageOf(2, 0)}, nil
	})
	f.Handle(Hello, func([]string, app.RunLimits) (app.RunResult, error) {
		return app.RunResult{Stdout: []byte("hi\n"), Usage: usageOf(9, 64<<10)}, nil
	})
	f.Handle(Spins, func(_ []string, limits app.RunLimits) (app.RunResult, error) {
		return app.RunResult{Usage: usageOf(limits.Fuel, 0)}, errors.New("all fuel consumed by WebAssembly")
	})
	return f
}

// Handle makes the Fake run module as program does, in place of anything it did.
func (f *Fake) Handle(module []byte, program Program) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.programs[string(module)] = program
}

// Run runs a registered module, and fails for bytes that are not a WebAssembly
// module or that no program was registered for. It does not consult ctx.
func (f *Fake) Run(_ context.Context, module []byte, args []string, limits app.RunLimits) (app.RunResult, error) {
	if !bytes.HasPrefix(module, []byte("\x00asm")) {
		return app.RunResult{}, errors.New("not a WebAssembly module")
	}
	f.mu.Lock()
	program, ok := f.programs[string(module)]
	f.mu.Unlock()
	if !ok {
		return app.RunResult{}, errors.New("runnertest: no program registered for this module")
	}
	return program(args, limits)
}
