package gateway

import (
	"context"

	domainfleet "janus/internal/domain/fleet"
)

// fleetService is the fleet service as the gateway reaches it: enrollment of Agents, the
// Agents on the tailnet, their Deployments and the Usage they report. A request
// the service refuses fails with an error whose text is meant for the Account, or
// with domainfleet.ErrAgentNotFound, ErrAgentUnreachable or an *AgentError. An
// Agent or Deployment that is not the Owner's is absent.
type fleetService interface {
	IssueCode(ctx context.Context, owner string) (domainfleet.IssuedCode, error)
	Enroll(ctx context.Context, code, name string) (domainfleet.Enrollment, error)
	ListAgents(ctx context.Context, owner string) ([]domainfleet.AgentStatus, error)
	// RemoveAgent deletes the Owner's Agent and takes its node off the tailnet.
	// removed is false for an Agent the Owner does not have.
	RemoveAgent(ctx context.Context, owner, agentID string) (removed bool, err error)
	// AgentStatuses returns the last state each of the Owner's Agents reported,
	// by Agent ID.
	AgentStatuses(ctx context.Context, owner string) (map[string]domainfleet.AgentStatusReport, error)
	PauseAgent(ctx context.Context, owner, agentID string) error
	ResumeAgent(ctx context.Context, owner, agentID string) error
	// Usage returns the Owner's recorded runs that match query, newest first.
	Usage(ctx context.Context, query domainfleet.RunUsageQuery) ([]domainfleet.RunUsageRecord, error)
	// CreateDeployment queues a Deployment and returns its ID.
	CreateDeployment(ctx context.Context, deployment domainfleet.NewDeployment) (id string, err error)
	// RollbackDeployment queues a Deployment of a stopped one's WASM Build.
	RollbackDeployment(ctx context.Context, owner, id string) (string, error)
	StopDeployment(ctx context.Context, owner, id string) error
	FindDeployment(ctx context.Context, owner, id string) (deployment domainfleet.Deployment, found bool, err error)
}
