package httpapi

import (
	"errors"
	"net/http"
	"time"

	"janus/internal/app"
)

func (a *Server) registerAgentRoutes(app *mux) {
	app.Post("/api/agents/enrollment-codes", a.whenAgentsConfigured(a.issueAgentCode))
	app.Post("/api/agents/enroll", a.whenAgentsConfigured(a.enrollAgent))
	app.Get("/api/agents", a.whenAgentsConfigured(a.listAgents))
	app.Delete("/api/agents/:id", a.whenAgentsConfigured(a.removeAgent))
}

// whenAgentsConfigured answers 503 for every Agent route when no Headscale server
// is configured.
func (a *Server) whenAgentsConfigured(handle func(*call) any) func(*call) any {
	return func(c *call) any {
		if a.dependencies.Agents == nil {
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
	issued, err := a.dependencies.Agents.IssueCode(c.Request.Context(), owner)
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
	enrollment, err := a.dependencies.Agents.Enroll(ctx, input.Code, input.Name)
	switch {
	case errors.Is(err, app.ErrEnrollmentCodeInvalid), errors.Is(err, app.ErrAgentName):
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()})
	case err != nil:
		a.log(ctx, "error", "agent.enroll.failed", nil)
		return internalError()
	}
	return secretReply(http.StatusCreated, map[string]any{
		"agent_id": enrollment.AgentID, "auth_key": enrollment.AuthKey, "tag": enrollment.Tag,
		"control_url": enrollment.ControlURL, "key_expires_at": enrollment.KeyExpires,
	})
}

type agentView struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Tag       string     `json:"tag"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
}

func (a *Server) listAgents(c *call) any {
	owner, ok := a.authorizedSubject(c, "agent.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	statuses, err := a.dependencies.Agents.List(c.Request.Context(), owner)
	if err != nil {
		return internalError()
	}
	views := make([]agentView, 0, len(statuses))
	for _, status := range statuses {
		view := agentView{ID: status.ID, Name: status.Name, Tag: status.Tag, Status: status.Status, CreatedAt: status.CreatedAt}
		if !status.LastSeen.IsZero() {
			seen := status.LastSeen
			view.LastSeen = &seen
		}
		views = append(views, view)
	}
	return views
}

func (a *Server) removeAgent(c *call) any {
	owner, ok := a.authorizedSubject(c, "agent.remove", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	removed, err := a.dependencies.Agents.Remove(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		a.log(c.Request.Context(), "error", "agent.remove.failed", map[string]string{"user_id": owner, "agent_id": c.Param("id")})
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": "the Agent could not be removed from the tailnet; try again"})
	}
	if !removed {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "Agent not found"})
	}
	return reply{StatusCode: http.StatusNoContent}
}
