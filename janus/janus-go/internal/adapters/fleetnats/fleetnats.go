// Package fleetnats is the gateway's client of the fleet service. It asks over
// NATS and turns a refusal of the service into the error the gateway matches: an
// absent Agent is fleet.ErrAgentNotFound, an Agent that cannot be reached is
// fleet.ErrAgentUnreachable, and an Agent's refusal of a command is a
// *fleet.AgentError.
package fleetnats

import (
	"context"
	"errors"
	"net/http"

	"github.com/nats-io/nats.go"

	"janus/internal/contract"
	contractfleet "janus/internal/contract/fleet"
	domainfleet "janus/internal/domain/fleet"
	"janus/internal/platform/natsx"
)

// Client is the fleet service over a NATS connection.
type Client struct {
	conn *nats.Conn
}

// New returns a Client that sends its requests on conn.
func New(conn *nats.Conn) *Client {
	return &Client{conn: conn}
}

// call sends one request and turns a refusal the service reported into the error
// the port documents. Refusals the Account reads are plain errors carrying the
// service's sentence.
func (c *Client) call(ctx context.Context, subject string, request, reply any) error {
	return translate(natsx.Call(ctx, c.conn, subject, request, reply))
}

// translate turns the error of a call into the one the port documents.
func translate(err error) error {
	var remote *natsx.RemoteError
	if !errors.As(err, &remote) {
		return err
	}
	switch {
	case remote.Status == http.StatusNotFound && remote.Message == domainfleet.ErrAgentNotFound.Error():
		return domainfleet.ErrAgentNotFound
	case remote.Status == http.StatusServiceUnavailable && remote.Message == contractfleet.ErrAgentUnreachable.Error():
		return domainfleet.ErrAgentUnreachable
	case remote.Status == http.StatusBadRequest && remote.Message == domainfleet.ErrEnrollmentCodeInvalid.Error():
		return domainfleet.ErrEnrollmentCodeInvalid
	case remote.Status == http.StatusBadRequest && remote.Message == domainfleet.ErrAgentName.Error():
		return domainfleet.ErrAgentName
	case remote.Status == http.StatusBadRequest, remote.Status == http.StatusNotFound, remote.Status == http.StatusConflict:
		return errors.New(remote.Message)
	}
	return contract.FromStatus(remote.Status, remote.Message)
}

func (c *Client) IssueCode(ctx context.Context, owner string) (domainfleet.IssuedCode, error) {
	var issued contractfleet.IssuedCode
	err := c.call(ctx, contractfleet.SubjectIssueCode, contractfleet.Owner{OwnerID: owner}, &issued)
	return issued, err
}

func (c *Client) Enroll(ctx context.Context, code, name string) (domainfleet.Enrollment, error) {
	var enrollment contractfleet.Enrollment
	err := c.call(ctx, contractfleet.SubjectEnroll, contractfleet.EnrollRequest{Code: code, Name: name}, &enrollment)
	return enrollment, err
}

func (c *Client) ListAgents(ctx context.Context, owner string) ([]domainfleet.AgentStatus, error) {
	var list contractfleet.AgentList
	err := c.call(ctx, contractfleet.SubjectListAgents, contractfleet.Owner{OwnerID: owner}, &list)
	return list.Agents, err
}

func (c *Client) RemoveAgent(ctx context.Context, owner, agentID string) (bool, error) {
	var removed contractfleet.Removed
	err := c.call(ctx, contractfleet.SubjectRemoveAgent, contractfleet.AgentKey{OwnerID: owner, AgentID: agentID}, &removed)
	return removed.Removed, err
}

func (c *Client) AgentStatuses(ctx context.Context, owner string) (map[string]domainfleet.AgentStatusReport, error) {
	var statuses contractfleet.AgentStatuses
	err := c.call(ctx, contractfleet.SubjectAgentStatus, contractfleet.Owner{OwnerID: owner}, &statuses)
	return statuses.Reports, err
}

// PauseAgent tells the Owner's Agent to stop serving. An Agent that refuses says
// why in an *domainfleet.AgentError.
func (c *Client) PauseAgent(ctx context.Context, owner, agentID string) error {
	return c.agentCommand(ctx, contractfleet.SubjectPauseAgent, owner, agentID)
}

// ResumeAgent tells the Owner's Agent to serve again.
func (c *Client) ResumeAgent(ctx context.Context, owner, agentID string) error {
	return c.agentCommand(ctx, contractfleet.SubjectResumeAgent, owner, agentID)
}

// agentCommand sends a pause or a resume, and reads the Agent's refusal, which the
// service answers as a plain 400 with the Agent's reason, as an *domainfleet.AgentError.
func (c *Client) agentCommand(ctx context.Context, subject, owner, agentID string) error {
	err := natsx.Call(ctx, c.conn, subject, contractfleet.AgentKey{OwnerID: owner, AgentID: agentID}, nil)
	var remote *natsx.RemoteError
	if errors.As(err, &remote) && remote.Status == http.StatusBadRequest {
		return &domainfleet.AgentError{Message: remote.Message}
	}
	return translate(err)
}

func (c *Client) Usage(ctx context.Context, query domainfleet.RunUsageQuery) ([]domainfleet.RunUsageRecord, error) {
	var usage contractfleet.RunUsage
	err := c.call(ctx, contractfleet.SubjectListUsage, query, &usage)
	return usage.Runs, err
}

func (c *Client) CreateDeployment(ctx context.Context, deployment domainfleet.NewDeployment) (string, error) {
	var queued contractfleet.Queued
	err := c.call(ctx, contractfleet.SubjectCreateDeployment, deployment, &queued)
	return queued.ID, err
}

func (c *Client) RollbackDeployment(ctx context.Context, owner, id string) (string, error) {
	var queued contractfleet.Queued
	err := c.call(ctx, contractfleet.SubjectRollbackDeployment, contractfleet.DeploymentKey{OwnerID: owner, DeploymentID: id}, &queued)
	return queued.ID, err
}

func (c *Client) StopDeployment(ctx context.Context, owner, id string) error {
	return c.call(ctx, contractfleet.SubjectStopDeployment, contractfleet.DeploymentKey{OwnerID: owner, DeploymentID: id}, nil)
}

func (c *Client) FindDeployment(ctx context.Context, owner, id string) (domainfleet.Deployment, bool, error) {
	var found contractfleet.DeploymentFound
	err := c.call(ctx, contractfleet.SubjectFindDeployment, contractfleet.DeploymentKey{OwnerID: owner, DeploymentID: id}, &found)
	if err != nil || !found.Found {
		return domainfleet.Deployment{}, false, err
	}
	return found.Deployment, true, nil
}

// RevokeAgents takes the Owner's Agents off the tailnet and keeps their records.
// TeardownDeployments stops the Owner's running Deployments. DeleteOwner removes
// everything the Owner has in the service. The gateway does not call them: the
// fleet service runs them when it sees an Account deleting. The service's tests
// call them directly.
func (c *Client) RevokeAgents(ctx context.Context, owner string) error {
	return c.call(ctx, contractfleet.SubjectRevokeAgents, contractfleet.Owner{OwnerID: owner}, nil)
}

func (c *Client) TeardownDeployments(ctx context.Context, owner string) error {
	return c.call(ctx, contractfleet.SubjectTeardownDeployments, contractfleet.Owner{OwnerID: owner}, nil)
}

func (c *Client) DeleteOwner(ctx context.Context, owner string) error {
	return c.call(ctx, contractfleet.SubjectDeleteOwner, contractfleet.Owner{OwnerID: owner}, nil)
}
