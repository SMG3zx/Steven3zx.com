// Package fleet holds the types of Janus Agents, their Deployments and the Usage
// they report, and the link between the control plane and an Agent. The fleet
// service, its contract and the single process all use these types, so they are
// pure values with no dependency beyond the domain.
package fleet

import (
	"errors"
	"time"

	"janus/internal/domain/templates"
	"janus/internal/domain/usage"
)

// ErrAgentUnreachable is what an Agent link's error matches when no connection to
// the Agent is up.
var ErrAgentUnreachable = errors.New("the Agent cannot be reached")

// AgentError is an Agent's refusal of a command. Message is the Agent's reason.
type AgentError struct{ Message string }

func (e *AgentError) Error() string { return e.Message }

// The sentences of the refusals an Enrollment Code and an Agent name get. The
// single process matches them to report the refusal to the Account.
const (
	MessageEnrollmentCodeInvalid = "the enrollment code is invalid or has expired"
	MessageAgentName             = "give the Agent a name of 1 to 64 characters"
)

// IssuedCode is a new Enrollment Code. Code is shown to the Owner once and is
// never stored; only its hash is.
type IssuedCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Enrollment is what an Agent needs to join the tailnet: a single-use key, the
// control server to join, and the tag its node will carry. AuthKey is a secret.
type Enrollment struct {
	AgentID    string    `json:"agent_id"`
	AuthKey    string    `json:"auth_key"`
	Tag        string    `json:"tag"`
	ControlURL string    `json:"control_url"`
	KeyExpires time.Time `json:"key_expires"`
}

// AgentRecord is a stored Agent.
type AgentRecord struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id"`
	Name      string    `json:"name"`
	Tag       string    `json:"tag"`
	KeyID     string    `json:"key_id"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentStatus is an Agent with whether its node is on the tailnet. Status is
// "online", "offline", "pending" or "unknown". LastSeen is the tailnet's last
// report of the node, and is zero for a pending Agent.
type AgentStatus struct {
	AgentRecord
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
}

// DeploymentHash names a Deployment an Agent serves and its module's SHA-256.
type DeploymentHash struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// AgentState is what an Agent is doing: whether it is paused, and the
// Deployments it serves, ordered by ID.
type AgentState struct {
	Paused      bool             `json:"paused"`
	Deployments []DeploymentHash `json:"deployments"`
}

// AgentStatusReport is the last state an Agent reported over its connection.
// Connected is false when the control plane has no state from it yet.
type AgentStatusReport struct {
	Connected bool `json:"connected"`
	AgentState
}

// RunReport is what one run of a Deployment's module consumed, as its Agent
// measured it. NetworkBytesIn and NetworkBytesOut are the size of the request
// and of the response, headers and body. Status is one of the usage statuses,
// and Error says why a run that was not ok ended. Probe marks the GET / run that
// decides whether a Deployment is healthy.
type RunReport struct {
	RunID           string      `json:"run_id"`
	DeploymentID    string      `json:"deployment_id"`
	Usage           usage.Usage `json:"usage"`
	NetworkBytesIn  uint64      `json:"network_bytes_in"`
	NetworkBytesOut uint64      `json:"network_bytes_out"`
	Status          string      `json:"status"`
	Error           string      `json:"error,omitempty"`
	Probe           bool        `json:"probe"`
	StartedAt       time.Time   `json:"started_at"`
}

// RunUsageRecord is one run an Agent reported, with the Owner and the Agent it
// came from. The Owner is the Agent's, never a value the Agent sent.
type RunUsageRecord struct {
	RunReport
	OwnerID string `json:"owner_id"`
	AgentID string `json:"agent_id"`
}

// RunUsageQuery selects an Owner's runs. AgentID and DeploymentID narrow it
// when set. Limit caps how many come back; zero or less means 100.
type RunUsageQuery struct {
	OwnerID      string `json:"owner_id"`
	AgentID      string `json:"agent_id,omitempty"`
	DeploymentID string `json:"deployment_id,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

// HostedDeployment is a Deployment as an Agent serves it: the module, its
// SHA-256 as lower-case hex, and the Template's fuel and memory limits.
// Limits.Timeout is not used; every request gets the same timeout.
type HostedDeployment struct {
	ID     string              `json:"id"`
	SHA256 string              `json:"sha256"`
	Module []byte              `json:"module"`
	Limits templates.Resources `json:"limits"`
}

// AgentCommand is one instruction to an Agent. Exactly one field is set.
type AgentCommand struct {
	Deploy *HostedDeployment `json:"deploy,omitempty"`
	Stop   string            `json:"stop,omitempty"`
	Pause  bool              `json:"pause,omitempty"`
	Resume bool              `json:"resume,omitempty"`
}

// AgentEvent is one message from an Agent. Exactly one field is set.
type AgentEvent struct {
	State *AgentState `json:"state,omitempty"`
	Usage *RunReport  `json:"usage,omitempty"`
}

// NewDeployment asks to deploy a WASM Build on one of the Owner's Agents.
// DeployAnyway deploys the Build even though its checks failed.
type NewDeployment struct {
	OwnerID      string `json:"owner_id"`
	ProjectID    string `json:"project_id"`
	BuildID      string `json:"build_id"`
	AgentID      string `json:"agent_id"`
	DeployAnyway bool   `json:"deploy_anyway,omitempty"`
}

// Deployment is a Deployment as its Owner sees it.
type Deployment struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	BuildID      string `json:"build_id"`
	AgentID      string `json:"agent_id"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	Error        string `json:"error,omitempty"`
	DeployAnyway bool   `json:"deploy_anyway"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}
