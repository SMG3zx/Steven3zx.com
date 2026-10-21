package app

import (
	"errors"
	"time"

	domainfleet "janus/internal/domain/fleet"
	"janus/internal/domain/templates"
	"janus/internal/domain/usage"
)

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

// Repository is host repository metadata available to an authenticated user.
type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Language      string `json:"language"`
	Stars         int    `json:"stargazers_count"`
	Forks         int    `json:"forks_count"`
	CloneURL      string `json:"clone_url"`
	HTMLURL       string `json:"html_url"`
	Size          int64  `json:"size"`
}

// Branch identifies a repository branch and its current commit.
type Branch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// Commit contains the display metadata Janus uses for a repository revision.
type Commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Date    time.Time `json:"date"`
}

// AuditEvent is one structured record of something an Account or the system
// did. Level is "debug", "info", "warn" or "error"; anything else is "info".
type AuditEvent struct {
	Level  string
	Name   string
	Fields map[string]string
}

// Conflict is a request Identities refused because it clashes with what is
// stored. Reason is a sentence for the Account, such as "account already
// exists".
type Conflict struct{ Reason string }

func (c *Conflict) Error() string { return c.Reason }

// IsConflict reports whether err is, or wraps, a *Conflict.
func IsConflict(err error) bool {
	var conflict *Conflict
	return errors.As(err, &conflict)
}

// The Agent link's wire types are the fleet contract's: the fleet service and
// the Agent both speak them, so the single process uses the same types.
type (
	HostedDeployment = domainfleet.HostedDeployment
	AgentState       = domainfleet.AgentState
	DeploymentHash   = domainfleet.DeploymentHash
	RunReport        = domainfleet.RunReport
	AgentCommand     = domainfleet.AgentCommand
	AgentEvent       = domainfleet.AgentEvent
	AgentError       = domainfleet.AgentError
)

// ErrAgentUnreachable is what the Fleet's error matches when no connection to the
// Agent is up.
var ErrAgentUnreachable = domainfleet.ErrAgentUnreachable

// ErrAgentNotFound is what the Fleet's error matches for an Agent the Owner does
// not have.
var ErrAgentNotFound = errors.New("Agent not found")

// ErrEnrollmentCodeInvalid is what the Fleet's enrollment refuses a code with:
// unknown, used or expired, which are not told apart.
var ErrEnrollmentCodeInvalid = errors.New(domainfleet.MessageEnrollmentCodeInvalid)

// ErrAgentName is what the Fleet's enrollment refuses a blank or too long name
// with.
var ErrAgentName = errors.New(domainfleet.MessageAgentName)
