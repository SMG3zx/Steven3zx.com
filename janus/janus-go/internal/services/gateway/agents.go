package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	domainfleet "janus/internal/domain/fleet"
)

func (a *Server) registerAgentRoutes(app *mux) {
	app.Post("/api/agents/enrollment-codes", a.whenAgentsConfigured(a.issueAgentCode))
	app.Post("/api/agents/enroll", a.whenAgentsConfigured(a.enrollAgent))
	app.Get("/api/agents", a.whenAgentsConfigured(a.listAgents))
	app.Delete("/api/agents/:id", a.whenAgentsConfigured(a.removeAgent))
	app.Post("/api/agents/:id/pause", a.whenAgentsConfigured(a.pauseAgent))
	app.Post("/api/agents/:id/resume", a.whenAgentsConfigured(a.resumeAgent))
	app.Get("/api/agents/:id/runs", a.whenAgentsConfigured(a.listAgentRuns))
}

// whenAgentsConfigured answers 503 for every Agent route when no Headscale server
// is configured.
func (a *Server) whenAgentsConfigured(handle func(*call) any) func(*call) any {
	return func(c *call) any {
		if a.dependencies.Fleet == nil {
			return agentsUnavailable()
		}
		return handle(c)
	}
}

// secretReply is a JSON reply that carries a code or a key, so no cache keeps it.
func secretReply(status int, body any) reply {
	return reply{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}, Body: body}
}

func agentsUnavailable() reply {
	return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Agent enrollment is not configured"})
}

func (a *Server) issueAgentCode(c *call) any {
	owner, ok := a.authorizedSubject(c, "agent.code.create", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	issued, err := a.dependencies.Fleet.IssueCode(c.Request.Context(), owner)
	if err != nil {
		return internalError()
	}
	return secretReply(http.StatusCreated, map[string]any{"code": issued.Code, "expires_at": issued.ExpiresAt})
}

type enrollInput struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// enrollAgent trades an Enrollment Code for the key an Agent joins the tailnet
// with. The Agent calls it from its command line, so there is no session.
func (a *Server) enrollAgent(c *call) any {
	ctx := c.Request.Context()
	allowed, err := a.allowed(ctx, "", "agent.enroll", nil)
	if err != nil {
		return internalError()
	}
	if !allowed {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "forbidden"})
	}
	var input enrollInput
	if err = decodeJSON(c.Request, &input); err != nil || input.Code == "" {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "send an enrollment code and a name for the Agent"})
	}
	enrollment, err := a.dependencies.Fleet.Enroll(ctx, input.Code, input.Name)
	switch {
	case errors.Is(err, domainfleet.ErrEnrollmentCodeInvalid), errors.Is(err, domainfleet.ErrAgentName):
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()})
	case err != nil:
		a.log(ctx, "error", "agent.enroll.failed", nil)
		return internalError()
	}
	return secretReply(http.StatusCreated, map[string]any{
		"agent_id": enrollment.AgentID, "owner_id": enrollment.OwnerID, "auth_key": enrollment.AuthKey, "tag": enrollment.Tag,
		"control_url": enrollment.ControlURL, "key_expires_at": enrollment.KeyExpires,
		"leaf_creds": enrollment.LeafCreds, "local_creds": enrollment.LocalCreds, "trust": enrollment.Trust,
	})
}

// agentView is an Agent as the API shows it. Connected, Paused and Deployments
// are what the Agent last reported over its connection to the control plane;
// Connected is false until it has reported.
type agentView struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Tag         string     `json:"tag"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	LastSeen    *time.Time `json:"last_seen,omitempty"`
	Connected   bool       `json:"connected"`
	Paused      bool       `json:"paused"`
	Deployments []string   `json:"deployments"`
}

func (a *Server) listAgents(c *call) any {
	owner, ok := a.authorizedSubject(c, "agent.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	statuses, err := a.dependencies.Fleet.ListAgents(c.Request.Context(), owner)
	if err != nil {
		return internalError()
	}
	reported, err := a.dependencies.Fleet.AgentStatuses(c.Request.Context(), owner)
	if err != nil {
		return internalError()
	}
	views := make([]agentView, 0, len(statuses))
	for _, status := range statuses {
		view := agentView{ID: status.ID, Name: status.Name, Tag: status.Tag, Status: status.Status, CreatedAt: status.CreatedAt, Deployments: []string{}}
		if !status.LastSeen.IsZero() {
			seen := status.LastSeen
			view.LastSeen = &seen
		}
		if report := reported[status.ID]; report.Connected {
			view.Connected, view.Paused = true, report.Paused
			for _, deployment := range report.Deployments {
				view.Deployments = append(view.Deployments, deployment.ID)
			}
		}
		views = append(views, view)
	}
	return views
}

func (a *Server) pauseAgent(c *call) any {
	return a.controlAgent(c, "agent.paused", a.dependencies.Fleet.PauseAgent)
}

func (a *Server) resumeAgent(c *call) any {
	return a.controlAgent(c, "agent.resumed", a.dependencies.Fleet.ResumeAgent)
}

// controlAgent sends the Owner's Agent a pause or a resume and answers with
// the outcome.
func (a *Server) controlAgent(c *call, done string, command func(ctx context.Context, ownerID, agentID string) error) any {
	owner, ok := a.authorizedSubject(c, "agent.control", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.Fleet == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "the control plane is not on the tailnet, so it cannot reach Agents"})
	}
	err := command(c.Request.Context(), owner, c.Param("id"))
	var refused *domainfleet.AgentError
	switch {
	case errors.Is(err, domainfleet.ErrAgentNotFound):
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "Agent not found"})
	case errors.Is(err, domainfleet.ErrAgentUnreachable):
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "the Agent is offline; start it and try again"})
	case errors.As(err, &refused):
		return jsonResponse(http.StatusConflict, map[string]string{"error": refused.Message})
	case err != nil:
		return internalError()
	}
	return jsonResponse(http.StatusOK, map[string]string{"status": done})
}

// runView is one run's Usage as the API shows it.
type runView struct {
	RunID           string    `json:"run_id"`
	DeploymentID    string    `json:"deployment_id"`
	Status          string    `json:"status"`
	Error           string    `json:"error,omitempty"`
	Probe           bool      `json:"probe"`
	StartedAt       time.Time `json:"started_at"`
	Fuel            uint64    `json:"fuel"`
	PeakMemoryBytes uint64    `json:"peak_memory_bytes"`
	DurationMS      int64     `json:"duration_ms"`
	CPUTimeMicros   int64     `json:"cpu_time_us"`
	NetworkBytesIn  uint64    `json:"network_bytes_in"`
	NetworkBytesOut uint64    `json:"network_bytes_out"`
}

// listAgentRuns lists the newest runs the Owner's Agent reported, at most 100.
func (a *Server) listAgentRuns(c *call) any {
	owner, ok := a.authorizedSubject(c, "agent.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.Fleet == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "the control plane is not on the tailnet, so it has no Agent runs"})
	}
	records, err := a.dependencies.Fleet.Usage(c.Request.Context(), domainfleet.RunUsageQuery{OwnerID: owner, AgentID: c.Param("id")})
	switch {
	case errors.Is(err, domainfleet.ErrAgentNotFound):
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "Agent not found"})
	case err != nil:
		return internalError()
	}
	views := make([]runView, 0, len(records))
	for _, r := range records {
		views = append(views, runView{RunID: r.RunID, DeploymentID: r.DeploymentID, Status: r.Status, Error: r.Error, Probe: r.Probe,
			StartedAt: r.StartedAt, Fuel: r.Usage.Fuel, PeakMemoryBytes: r.Usage.PeakMemoryBytes, DurationMS: r.Usage.DurationMS,
			CPUTimeMicros: r.Usage.CPUTimeMicros, NetworkBytesIn: r.NetworkBytesIn, NetworkBytesOut: r.NetworkBytesOut})
	}
	return views
}

func (a *Server) removeAgent(c *call) any {
	owner, ok := a.authorizedSubject(c, "agent.remove", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	removed, err := a.dependencies.Fleet.RemoveAgent(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		a.log(c.Request.Context(), "error", "agent.remove.failed", map[string]string{"user_id": owner, "agent_id": c.Param("id")})
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": "the Agent could not be removed from the tailnet; try again"})
	}
	if !removed {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "Agent not found"})
	}
	return reply{StatusCode: http.StatusNoContent}
}
