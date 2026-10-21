package httpapi

import (
	"net/http"
	"strings"

	"janus/internal/app"
)

// deploymentInput is the body of a Deploy request.
type deploymentInput struct {
	BuildID      string `json:"build_id"`
	AgentID      string `json:"agent_id"`
	DeployAnyway bool   `json:"deploy_anyway"`
}

// startDeployment queues a Deployment of a WASM Build on one of the Account's
// Agents and answers 202 with the Deployment's ID, which is queued until the
// Agent reports it.
func (a *Server) startDeployment(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.deploy", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.Deployments == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Deployments are unavailable"})
	}
	var input deploymentInput
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "choose a WASM build and an Agent to deploy"})
	}
	id, err := a.dependencies.Deployments.Create(c.Request.Context(), app.NewDeployment{OwnerID: owner, ProjectID: c.Param("id"),
		BuildID: input.BuildID, AgentID: input.AgentID, DeployAnyway: input.DeployAnyway})
	if err != nil {
		return jsonResponse(deploymentErrorStatus(err), map[string]string{"error": err.Error()})
	}
	a.log(c.Request.Context(), "info", "project.deployment.requested", map[string]string{"user_id": owner, "project_id": c.Param("id"), "deployment_id": id})
	return reply{StatusCode: http.StatusAccepted, Body: map[string]string{"id": id, "state": "queued"}}
}

// getDeployment returns one of the Account's Deployments, with its Check.
func (a *Server) getDeployment(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.Deployments == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Deployments are unavailable"})
	}
	deployment, found, err := a.dependencies.Deployments.Find(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		return internalError()
	}
	if !found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "deployment not found"})
	}
	return jsonResponse(http.StatusOK, deployment)
}

// deploymentErrorStatus maps a refused Deploy to its status: 404 for something
// the Account does not have, 409 for a Deploy the current state of the Project
// or its build does not allow.
func deploymentErrorStatus(err error) int {
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return http.StatusNotFound
	}
	return http.StatusConflict
}
