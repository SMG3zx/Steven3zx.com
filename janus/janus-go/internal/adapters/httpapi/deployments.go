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
	if a.dependencies.Fleet == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Deployments are unavailable"})
	}
	var input deploymentInput
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "choose a WASM build and an Agent to deploy"})
	}
	id, err := a.dependencies.Fleet.CreateDeployment(c.Request.Context(), app.NewDeployment{OwnerID: owner, ProjectID: c.Param("id"),
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
	if a.dependencies.Fleet == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Deployments are unavailable"})
	}
	deployment, found, err := a.dependencies.Fleet.FindDeployment(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		return internalError()
	}
	if !found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "deployment not found"})
	}
	if a.dependencies.Builds != nil {
		check, ok, err := a.dependencies.Builds.Check(c.Request.Context(), owner, deployment.BuildID)
		if err != nil {
			return internalError()
		}
		if ok {
			deployment.Check = &app.Check{BuildID: check.BuildID, Status: check.Status, Error: check.Error, Log: check.Log, NoTests: check.NoTests}
		}
	}
	return jsonResponse(http.StatusOK, deployment)
}

// rollbackDeployment redeploys the WASM Build of one of the Account's recent
// stopped Deployments and answers 202 with the new Deployment's ID.
func (a *Server) rollbackDeployment(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.deploy", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.Fleet == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Deployments are unavailable"})
	}
	id, err := a.dependencies.Fleet.RollbackDeployment(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		return jsonResponse(deploymentErrorStatus(err), map[string]string{"error": err.Error()})
	}
	return reply{StatusCode: http.StatusAccepted, Body: map[string]string{"id": id, "state": "queued"}}
}

// stopDeployment stops one of the Account's Deployments and answers 200 when the
// Agent has stopped it.
func (a *Server) stopDeployment(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.deploy", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.Fleet == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Deployments are unavailable"})
	}
	if err := a.dependencies.Fleet.StopDeployment(c.Request.Context(), owner, c.Param("id")); err != nil {
		return jsonResponse(deploymentErrorStatus(err), map[string]string{"error": err.Error()})
	}
	return jsonResponse(http.StatusOK, map[string]string{"id": c.Param("id"), "state": "stopped"})
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
