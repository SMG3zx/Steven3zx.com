package agent

import (
	"context"
	"errors"

	domainfleet "janus/internal/domain/fleet"
	"janus/internal/domain/templates"
	"janus/internal/domain/usage"
)

// The Agent's own state and commands are defined by the fleet domain, which the
// control plane shares; the agent works with the same types.
type (
	AgentCommand     = domainfleet.AgentCommand
	AgentState       = domainfleet.AgentState
	DeploymentHash   = domainfleet.DeploymentHash
	HostedDeployment = domainfleet.HostedDeployment
	RunReport        = domainfleet.RunReport
)

// Runner runs a WASI module under input.Limits. Limits.Timeout bounds the run,
// and an adapter may also stop early when ctx ends. It reports what the module
// wrote and what the run consumed. A module that cannot be loaded, traps or
// exceeds a limit fails with an error, and the RunResult still holds the output
// and Usage measured up to that point. A run that spends all its fuel fails with
// an error matching ErrOutOfFuel, and one stopped by the timeout with an error
// matching ErrRunTimeout.
//
// The module reads input.Stdin and sees input.Env as its environment and
// input.Args as its arguments. When input.Dir is set, that host directory is
// the module's one preopened directory (file descriptor 3, named "/"), which it
// may read and write; nothing outside it is reachable. Without Dir the module
// has no files at all. Stdout is cut at input.MaxStdout bytes, 64 KiB when it
// is zero, and StdoutTruncated says so. Stderr is cut at 64 KiB.
type Runner interface {
	Run(ctx context.Context, module []byte, input RunInput) (RunResult, error)
}

// RunResult is what a WASM Run produced. Stdout and Stderr are bounded, and
// StdoutTruncated reports that Stdout was cut. Usage is filled in whenever the
// module started running, including when it traps, so a Run that exhausts its
// fuel still reports the fuel it spent.
type RunResult struct {
	Stdout, Stderr  []byte
	StdoutTruncated bool
	Usage           usage.Usage
}

// RunInput is what a module starts with: its arguments, its environment as
// "NAME=value" entries, its standard input, the host directory it may use, and
// its limits. See Runner for what each means.
type RunInput struct {
	Args, Env []string
	Stdin     []byte
	Dir       string
	MaxStdout int
	Limits    RunLimits
}

// RunLimits are the fuel, memory and time limits a WASM Run starts with.
type RunLimits = templates.Resources

// ErrOutOfFuel and ErrRunTimeout are what a Runner's error matches when a run
// spent all its fuel or ran out of time.
var (
	ErrOutOfFuel  = errors.New("all fuel consumed by WebAssembly")
	ErrRunTimeout = errors.New("the WebAssembly run timed out")
)

// LocalState is what an Agent keeps on its own machine, so that it serves the
// same Deployments, paused or not, after a restart. It is safe for concurrent
// use, and a pause written by another process using the same storage, such as
// the Agent's command line, is what the next Paused reads.
type LocalState interface {
	// SaveDeployment keeps a Deployment, replacing one with the same ID.
	SaveDeployment(ctx context.Context, deployment HostedDeployment) error
	// DeleteDeployment forgets a Deployment. Forgetting one that is not kept is
	// not an error.
	DeleteDeployment(ctx context.Context, id string) error
	// Deployments returns every kept Deployment, ordered by ID.
	Deployments(ctx context.Context) ([]HostedDeployment, error)
	// SetPaused records whether the Agent is paused.
	SetPaused(ctx context.Context, paused bool) error
	// Paused reads the pause as last recorded through any handle on the same
	// storage. It is false until SetPaused is first called.
	Paused(ctx context.Context) (bool, error)
}
