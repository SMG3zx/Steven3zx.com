package app

import "janus/internal/domain/agent"

// The Runner, LocalState and run types live in the agent package now. These
// aliases keep the single process compiling until internal/app is retired.
type (
	Runner     = agent.Runner
	LocalState = agent.LocalState
	RunResult  = agent.RunResult
	RunInput   = agent.RunInput
)

// ErrOutOfFuel and ErrRunTimeout are what a Runner's error matches when a run
// spent all its fuel or ran out of time.
var (
	ErrOutOfFuel  = agent.ErrOutOfFuel
	ErrRunTimeout = agent.ErrRunTimeout
)
