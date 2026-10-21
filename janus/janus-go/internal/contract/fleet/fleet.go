// Package fleet is the contract of the fleet service: enrollment of Janus
// Agents, the Agents themselves, their Deployments and the Usage they report.
// The types it carries are the domain's, so the single process and the service
// hold the same values; this package adds the subjects and the request and reply
// messages that go between them.
//
// Every request about an Owner's records carries owner_id, and a record that is
// not the Owner's is answered as absent. Failures are contract.Error values:
// StatusNotFound for an absent Agent or Deployment, StatusInvalid for a request
// that cannot be carried out as asked; the message is a sentence for the
// Account.
package fleet

import (
	domainfleet "janus/internal/domain/fleet"
)

// ServiceName is the service's name in NATS service discovery.
const ServiceName = "fleet"

// ControlTag is the tag of the control plane's own node on the tailnet. Nodes
// tagged ControlTag may open connections to every other tagged node.
const ControlTag = "tag:ctrl"

// AgentLeafPort is the port on the control plane's node that an Agent's leaf
// connection reaches. It is the only port of the control plane an Agent may open,
// so an Agent can join the hub and reach nothing else there.
const AgentLeafPort = 7102

// The names of an Agent's link. A command is a request on its command subject.
// Its state is the key of its ID in AgentStateBucket on the hub. Its Usage is
// a stream in its own JetStream domain, AgentDomain, under AgentUsageSubject.
// The modules an Agent deploys are objects in ModulesBucket of their Owner.
const AgentStateBucket = "agent_state"

// AgentCommandSubject is the subject a command to the Agent with agentID is sent
// on.
func AgentCommandSubject(agentID string) string {
	return "janus.agent." + agentID + ".command"
}

// AgentUsageSubject is the subject the Agent's runs are published on, in its
// USAGE stream.
func AgentUsageSubject(agentID string) string {
	return "janus.usage." + agentID + ".run"
}

// AgentUsageStream is the stream an Agent keeps its Usage in, in its own domain.
const AgentUsageStream = "USAGE"

// AgentDomain is the JetStream domain of the Agent with agentID.
func AgentDomain(agentID string) string {
	return "agent-" + agentID
}

// ModulesBucket is the object store that holds the modules of an Owner's
// Deployments, by SHA-256. The Owner's Agents read it through the hub.
func ModulesBucket(ownerID string) string {
	return "modules-" + ownerID
}

// The subjects the fleet service answers.
const (
	SubjectIssueCode           = "janus.fleet.code.issue"
	SubjectEnroll              = "janus.fleet.agent.enroll"
	SubjectListAgents          = "janus.fleet.agent.list"
	SubjectRemoveAgent         = "janus.fleet.agent.remove"
	SubjectRevokeAgents        = "janus.fleet.agent.revoke"
	SubjectAgentStatus         = "janus.fleet.agent.status"
	SubjectPauseAgent          = "janus.fleet.agent.pause"
	SubjectResumeAgent         = "janus.fleet.agent.resume"
	SubjectListUsage           = "janus.fleet.usage.list"
	SubjectCreateDeployment    = "janus.fleet.deployment.create"
	SubjectRollbackDeployment  = "janus.fleet.deployment.rollback"
	SubjectStopDeployment      = "janus.fleet.deployment.stop"
	SubjectFindDeployment      = "janus.fleet.deployment.find"
	SubjectTeardownDeployments = "janus.fleet.deployment.teardown"
	SubjectDeleteOwner         = "janus.fleet.owner.delete"
)

// The types the contract carries are the domain's.
type (
	IssuedCode        = domainfleet.IssuedCode
	Enrollment        = domainfleet.Enrollment
	AgentRecord       = domainfleet.AgentRecord
	AgentStatus       = domainfleet.AgentStatus
	DeploymentHash    = domainfleet.DeploymentHash
	AgentState        = domainfleet.AgentState
	AgentStatusReport = domainfleet.AgentStatusReport
	RunReport         = domainfleet.RunReport
	RunUsageRecord    = domainfleet.RunUsageRecord
	RunUsageQuery     = domainfleet.RunUsageQuery
	HostedDeployment  = domainfleet.HostedDeployment
	AgentCommand      = domainfleet.AgentCommand
	AgentEvent        = domainfleet.AgentEvent
	AgentReply        = domainfleet.AgentReply
	AgentTrust        = domainfleet.AgentTrust
	AgentError        = domainfleet.AgentError
	NewDeployment     = domainfleet.NewDeployment
	Deployment        = domainfleet.Deployment
)

// The sentences of the refusals an Enrollment Code and an Agent name get, and the
// error an Agent link returns for an Agent it cannot reach.
const (
	MessageEnrollmentCodeInvalid = domainfleet.MessageEnrollmentCodeInvalid
	MessageAgentName             = domainfleet.MessageAgentName
)

// ErrAgentUnreachable is what an Agent link's error matches when no connection to
// the Agent is up.
var ErrAgentUnreachable = domainfleet.ErrAgentUnreachable

// Empty is the reply of a request that returns nothing.
type Empty struct{}

// Owner names the Owner a request is about.
type Owner struct {
	OwnerID string `json:"owner_id"`
}

// AgentKey names one Agent of an Owner.
type AgentKey struct {
	OwnerID string `json:"owner_id"`
	AgentID string `json:"agent_id"`
}

// EnrollRequest exchanges an Enrollment Code for an Agent's key.
type EnrollRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// AgentList answers SubjectListAgents, oldest first.
type AgentList struct {
	Agents []AgentStatus `json:"agents"`
}

// Removed answers SubjectRemoveAgent. Removed is false for an Agent the Owner
// does not have.
type Removed struct {
	Removed bool `json:"removed"`
}

// AgentStatuses answers SubjectAgentStatus: the last report of each of the
// Owner's Agents, by Agent ID.
type AgentStatuses struct {
	Reports map[string]AgentStatusReport `json:"reports"`
}

// RunUsage answers SubjectListUsage, newest first.
type RunUsage struct {
	Runs []RunUsageRecord `json:"runs"`
}

// DeploymentKey names one Deployment of an Owner.
type DeploymentKey struct {
	OwnerID      string `json:"owner_id"`
	DeploymentID string `json:"deployment_id"`
}

// Queued answers SubjectCreateDeployment and SubjectRollbackDeployment with the
// ID of the Deployment that was queued.
type Queued struct {
	ID string `json:"id"`
}

// DeploymentFound answers SubjectFindDeployment.
type DeploymentFound struct {
	Found      bool       `json:"found"`
	Deployment Deployment `json:"deployment"`
}
