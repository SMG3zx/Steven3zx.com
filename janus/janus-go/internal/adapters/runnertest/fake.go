// Package runnertest holds an in-memory agent.Runner, WASI modules to run in its
// contract suite, and the suite every agent.Runner adapter, real or test, must
// pass.
package runnertest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"janus/internal/domain/agent"
)

const defaultMaxOutput = 64 << 10

// Fake is an agent.Runner that runs no WebAssembly. It knows the fixture modules of
// this package and any module a test registers with Handle, and refuses the
// rest. It is safe for concurrent use.
type Fake struct {
	mu       sync.Mutex
	programs map[string]Program
}

// Program is what a registered module does when it runs. It returns the Run's
// output and Usage and the error the run ends with. The Fake cuts Stdout to
// input.MaxStdout afterwards.
type Program func(input agent.RunInput) (agent.RunResult, error)

var _ agent.Runner = (*Fake)(nil)

// NewFake returns a Fake that runs the fixture modules of this package.
func NewFake() *Fake {
	f := &Fake{programs: map[string]Program{}}
	f.Handle(Exits, func(agent.RunInput) (agent.RunResult, error) {
		return agent.RunResult{Usage: usageOf(2, 0)}, nil
	})
	f.Handle(Hello, func(agent.RunInput) (agent.RunResult, error) {
		return agent.RunResult{Stdout: []byte("hi\n"), Usage: usageOf(9, 64<<10)}, nil
	})
	f.Handle(Spins, func(input agent.RunInput) (agent.RunResult, error) {
		// Time is not simulated: a fuel limit too large to spend first means the
		// timeout ended the run.
		if input.Limits.Fuel >= 1<<40 {
			return agent.RunResult{Usage: usageOf(1<<20, 0)}, agent.ErrRunTimeout
		}
		return agent.RunResult{Usage: usageOf(input.Limits.Fuel, 0)}, agent.ErrOutOfFuel
	})
	f.Handle(Echo, func(input agent.RunInput) (agent.RunResult, error) {
		return agent.RunResult{Stdout: bytes.Clone(input.Stdin), Usage: usageOf(uint64(100+len(input.Stdin)), 64<<10)}, nil
	})
	f.Handle(Environ, func(input agent.RunInput) (agent.RunResult, error) {
		var out bytes.Buffer
		for _, variable := range input.Env {
			out.WriteString(variable)
			out.WriteByte(0)
		}
		return agent.RunResult{Stdout: out.Bytes(), Usage: usageOf(50, 64<<10)}, nil
	})
	f.Handle(ReadFile, readFile)
	return f
}

// readFile does what the ReadFile fixture does, with the folder opened as an
// os.Root so that, like a WASI preopen, nothing outside it can be reached.
func readFile(input agent.RunInput) (agent.RunResult, error) {
	result := agent.RunResult{Usage: usageOf(80, 64<<10)}
	const badf, notcapable = 8, 76
	if input.Dir == "" {
		return result, fmt.Errorf("program exited with status %d", badf)
	}
	root, err := os.OpenRoot(input.Dir)
	if err != nil {
		return result, err
	}
	defer root.Close()
	name := string(input.Stdin)
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return result, fmt.Errorf("program exited with status %d", notcapable)
	}
	file, err := root.Open(name)
	if err != nil {
		return result, fmt.Errorf("program exited with status %d", notcapable)
	}
	defer file.Close()
	result.Stdout, err = io.ReadAll(io.LimitReader(file, 4096))
	return result, err
}

// Handle makes the Fake run module as program does, in place of anything it did.
func (f *Fake) Handle(module []byte, program Program) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.programs[string(module)] = program
}

// Run runs a registered module, and fails for bytes that are not a WebAssembly
// module or that no program was registered for. It does not consult ctx.
func (f *Fake) Run(_ context.Context, module []byte, input agent.RunInput) (agent.RunResult, error) {
	if !bytes.HasPrefix(module, []byte("\x00asm")) {
		return agent.RunResult{}, errors.New("not a WebAssembly module")
	}
	f.mu.Lock()
	program, ok := f.programs[string(module)]
	f.mu.Unlock()
	if !ok {
		return agent.RunResult{}, errors.New("runnertest: no program registered for this module")
	}
	result, err := program(input)
	limit := input.MaxStdout
	if limit <= 0 {
		limit = defaultMaxOutput
	}
	if len(result.Stdout) > limit {
		result.Stdout, result.StdoutTruncated = result.Stdout[:limit], true
	}
	return result, err
}
