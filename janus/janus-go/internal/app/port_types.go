package app

import (
	"errors"
	"time"

	"janus/internal/domain/templates"
	"janus/internal/domain/usage"
)

// BuildRequest asks for a workspace to be compiled for a Target. Entrypoint is
// the file the Account confirmed, relative to the workspace root; adapters that
// need one find their own when it is empty.
type BuildRequest struct{ Workspace, Target, Entrypoint string }

// BuildResult is a finished build. Artifact is a compressed ZIP of the WASM
// modules, and Log is the bounded compiler output. Log is set on failure too.
type BuildResult struct {
	Name          string
	Artifact, Log []byte
}

// ManifestError means the workspace lacks the manifest its Template requires.
// Message is the sentence an Account sees; Err is the underlying cause.
type ManifestError struct {
	Message string
	Err     error
}

func (e *ManifestError) Error() string { return e.Message + ": " + e.Err.Error() }
func (e *ManifestError) Unwrap() error { return e.Err }

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

// SourceRequest names the commit to fetch. Repository is the full name, such as
// "owner/name". Token is the access token of the Account, which a host that
// needs none ignores.
type SourceRequest struct{ Repository, Branch, Commit, Token string }

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

// SourceError is a failure to open a repository's tree. Message is the sentence
// an Account sees, such as "repository clone failed"; Err is the underlying cause.
type SourceError struct {
	Message string
	Err     error
}

func (e *SourceError) Error() string { return e.Message + ": " + e.Err.Error() }
func (e *SourceError) Unwrap() error { return e.Err }

// TailnetKey is a pre-auth key. ID names it for later calls; Key is the secret
// an Agent presents to join, and must not be logged or stored.
type TailnetKey struct {
	ID, Key string
	Tag     string
	Expires time.Time
}

// TailnetNode is a node on the tailnet. KeyID is the pre-auth key it joined with,
// and is empty for a node that was registered another way.
type TailnetNode struct {
	ID, Name, KeyID string
	Tags            []string
	Addresses       []string
	Online          bool
	LastSeen        time.Time
}

// CheckRequest asks for the checks of a compiled workspace. The workspace is the
// same source tree the build compiled.
type CheckRequest struct{ Workspace string }

// CheckResult is the outcome of a Check. Passed is false when a check or test
// command failed. NoTests is true when the Language's test command ran no tests,
// so the Check rests on the compile-level check alone. Log is bounded output.
type CheckResult struct {
	Passed, NoTests bool
	Log             []byte
}

// HostedDeployment is a Deployment as an Agent serves it: the module, its
// SHA-256 as lower-case hex, and the Template's fuel and memory limits.
// Limits.Timeout is not used; every request gets the same timeout.
type HostedDeployment struct {
	ID, SHA256 string
	Module     []byte
	Limits     RunLimits
}

// AgentState is what an Agent is doing: whether it is paused, and the
// Deployments it serves, ordered by ID.
type AgentState struct {
	Paused      bool
	Deployments []DeploymentHash
}

// DeploymentHash names a Deployment an Agent serves and its module's SHA-256.
type DeploymentHash struct{ ID, SHA256 string }

// RunReport is what one run of a Deployment's module consumed, as its Agent
// measured it. NetworkBytesIn and NetworkBytesOut are the size of the request
// and of the response, headers and body. Status is one of the usage statuses,
// and Error says why a run that was not ok ended. Probe marks the
// GET / run that decides whether a Deployment is healthy.
type RunReport struct {
	RunID, DeploymentID             string
	Usage                           usage.Usage
	NetworkBytesIn, NetworkBytesOut uint64
	Status, Error                   string
	Probe                           bool
	StartedAt                       time.Time
}

// AgentCommand is one instruction to an Agent. Exactly one field is set.
type AgentCommand struct {
	Deploy        *HostedDeployment
	Stop          string
	Pause, Resume bool
}

// AgentEvent is one message from an Agent. Exactly one field is set.
type AgentEvent struct {
	State *AgentState
	Usage *RunReport
}

// ErrAgentUnreachable is what AgentLink.Send's error matches when no connection
// to the Agent is up.
var ErrAgentUnreachable = errors.New("the Agent cannot be reached")

// AgentError is an Agent's refusal of a command. Message is the Agent's reason.
type AgentError struct{ Message string }

func (e *AgentError) Error() string { return e.Message }
