package app

import (
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

// RunResult is what a WASM Run produced. Stdout and Stderr are bounded. Usage
// is filled in whenever the module started running, including when it traps,
// so a Run that exhausts its fuel still reports the fuel it spent.
type RunResult struct {
	Stdout, Stderr []byte
	Usage          usage.Usage
}

// RunLimits are the fuel, memory and time limits a WASM Run starts with.
type RunLimits = templates.Resources

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
