package gateway

import (
	"context"
	"errors"
	"janus/internal/contract"
	contractbuilds "janus/internal/contract/builds"
	contractprojects "janus/internal/contract/projects"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

func (a *Server) registerProjectRoutes(app *mux) {
	app.Get("/api/projects", a.listProjects)
	app.Post("/api/projects", a.createProjectAPI)
	app.Get("/api/projects/:id", a.getProjectAPI)
	app.Get("/api/projects/:id/snapshots", a.listProjectSnapshots)
	app.Post("/api/projects/:id/snapshots/:snapshot/activate", a.activateProjectSnapshot)
	app.Put("/api/projects/:id", a.updateProjectAPI)
	app.Delete("/api/projects/:id", a.deleteProjectAPI)
	app.Get("/api/github/repos", a.listGitHubRepos)
	app.Get("/api/github/branches", a.listGitHubBranches)
	app.Get("/api/github/commits", a.listGitHubCommits)
	app.Post("/api/projects/:id/imports", a.startProjectImport)
	app.Get("/api/imports/:id", a.getImportStatus)
	app.Post("/api/projects/:id/entrypoint", a.setProjectEntrypoint)
	app.Post("/api/projects/:id/wasm-builds", a.startWASMBuild)
	app.Get("/api/wasm-builds/:id", a.getWASMBuild)
	app.Get("/api/wasm-builds/:id/artifact", a.getWASMArtifact)
	app.Post("/api/wasm-builds/:id/runs", a.startWASMRun)
	app.Post("/api/projects/:id/deployments", a.startDeployment)
	app.Get("/api/deployments/:id", a.getDeployment)
	app.Post("/api/deployments/:id/rollback", a.rollbackDeployment)
	app.Post("/api/deployments/:id/stop", a.stopDeployment)
	app.Get("/api/wasm-runs/:id", a.getWASMRun)
}

func (a *Server) listProjects(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", map[string]any{})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	projects, err := a.dependencies.Catalog.ListProjects(c.Request.Context(), owner)
	if err != nil {
		return internalError()
	}
	return projects
}

type createProjectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (a *Server) createProjectAPI(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.create", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	var input createProjectInput
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid project data"})
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 120 || len(input.Description) > 2000 {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "project name (1-120 characters) and description (up to 2000 characters) are required"})
	}
	created := a.createProject(c, owner, input.Name, input.Description)
	if failure, ok := created.(reply); ok {
		return failure
	}
	project, ok := created.(contractprojects.Project)
	if !ok {
		return internalError()
	}
	return reply{StatusCode: http.StatusCreated, Body: project}
}

func (a *Server) createProject(c *call, owner, name, description string) any {
	if strings.TrimSpace(name) == "" || len(name) > 120 || len(description) > 2000 {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "project name (1-120 characters) and description (up to 2000 characters) are required"})
	}
	project, err := a.dependencies.Catalog.CreateProject(c.Request.Context(), contractprojects.CreateProject{ID: uuid.NewString(), OwnerID: owner, Name: strings.TrimSpace(name), Description: strings.TrimSpace(description)})
	if err != nil {
		if errors.Is(err, contract.ErrConflict) {
			return jsonResponse(http.StatusConflict, map[string]string{"error": "project could not be created"})
		}
		return internalError()
	}
	return project
}

func (a *Server) getProjectAPI(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	project, found, err := a.dependencies.Catalog.GetProject(c.Request.Context(), c.Param("id"), owner)
	if err != nil {
		return internalError()
	}
	if !found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "project not found"})
	}
	return project
}

func (a *Server) listProjectSnapshots(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	snapshots, err := a.dependencies.Catalog.ListSnapshots(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return jsonResponse(http.StatusNotFound, map[string]string{"error": "project not found"})
		}
		return internalError()
	}
	return snapshots
}

func (a *Server) activateProjectSnapshot(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.update", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	activated, err := a.dependencies.Catalog.ActivateSnapshot(c.Request.Context(), owner, c.Param("id"), c.Param("snapshot"))
	if err != nil {
		return internalError()
	}
	if !activated {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "snapshot not found"})
	}
	a.log(c.Request.Context(), "info", "project.snapshot.activated", map[string]string{"user_id": owner, "project_id": c.Param("id"), "snapshot_id": c.Param("snapshot")})
	return map[string]bool{"active": true}
}

func (a *Server) updateProjectAPI(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.update", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	var input createProjectInput
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid project data"})
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 120 || len(input.Description) > 2000 {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "project name (1-120 characters) and description (up to 2000 characters) are required"})
	}
	project, err := a.dependencies.Catalog.UpdateProject(c.Request.Context(), contractprojects.UpdateProject{ID: c.Param("id"), OwnerID: owner, Name: input.Name, Description: input.Description})
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return jsonResponse(http.StatusNotFound, map[string]string{"error": "project not found"})
		}
		return internalError()
	}
	return project
}

func (a *Server) deleteProjectAPI(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.delete", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	deleted, err := a.dependencies.Catalog.DeleteProject(c.Request.Context(), c.Param("id"), owner)
	if err != nil {
		if errors.Is(err, contract.ErrConflict) {
			return jsonResponse(http.StatusConflict, map[string]string{"error": err.Error()})
		}
		return internalError()
	}
	if !deleted {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "project not found"})
	}
	return reply{StatusCode: http.StatusNoContent}
}

func (a *Server) listGitHubRepos(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	list, err := a.dependencies.Catalog.Repositories(c.Request.Context(), owner)
	if err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return list
}
func (a *Server) listGitHubBranches(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	branches, err := a.dependencies.Catalog.Branches(c.Request.Context(), owner, c.Query.Get("repository"))
	if err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return branches
}
func (a *Server) listGitHubCommits(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	commits, err := a.dependencies.Catalog.Commits(c.Request.Context(), owner, c.Query.Get("repository"), c.Query.Get("branch"))
	if err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return commits
}

type importInput struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Commit     string `json:"commit"`
}

func (a *Server) startProjectImport(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.import", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	var input importInput
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid import request"})
	}
	jobID, err := a.dependencies.Catalog.RequestImport(c.Request.Context(), contractprojects.RequestImport{OwnerID: owner, ProjectID: c.Param("id"), Repository: input.Repository, Branch: input.Branch, Commit: input.Commit})
	if err != nil {
		a.log(c.Request.Context(), "warn", "project.import.request.rejected", map[string]string{
			"user_id": owner, "project_id": c.Param("id"), "reason": err.Error(),
		})
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return reply{StatusCode: http.StatusAccepted, Body: map[string]string{"job_id": jobID, "status": "queued"}}
}

func (a *Server) getImportStatus(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	job, found, err := a.dependencies.Catalog.FindImportJob(c.Request.Context(), c.Param("id"), owner)
	if err != nil {
		return internalError()
	}
	if !found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "import job not found"})
	}
	return job
}

type wasmBuildInput struct {
	Target string `json:"target"`
}

func (a *Server) startWASMBuild(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.build", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.Builds == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "WASM builds are unavailable"})
	}
	var input wasmBuildInput
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "select a supported WASM target"})
	}
	requested, err := a.dependencies.Builds.Request(c.Request.Context(), contractbuilds.RequestBuild{ID: uuid.NewString(), OwnerID: owner, ProjectID: c.Param("id"), Target: input.Target})
	if err != nil {
		return jsonResponse(wasmBuildErrorStatus(err), map[string]string{"error": err.Error()})
	}
	jobID := requested.ID
	a.log(c.Request.Context(), "info", "project.wasm.build.requested", map[string]string{"user_id": owner, "project_id": c.Param("id"), "job_id": jobID, "target": input.Target})
	return reply{StatusCode: http.StatusAccepted, Body: map[string]string{"job_id": jobID, "status": "queued"}}
}

func wasmBuildErrorStatus(err error) int {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "not configured"), strings.Contains(message, "unavailable"):
		return http.StatusServiceUnavailable
	case strings.Contains(message, "project not found"), strings.Contains(message, "snapshot not found"):
		return http.StatusNotFound
	case strings.Contains(message, "already running"), strings.Contains(message, "constraint failed"):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

// wasmRunErrorStatus is the status a refused run request is answered with. A Build
// that is not the Account's is not found; two active runs are a conflict.
func wasmRunErrorStatus(err error) int {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "not found"):
		return http.StatusNotFound
	case strings.Contains(message, "already active"):
		return http.StatusConflict
	default:
		return wasmBuildErrorStatus(err)
	}
}

func (a *Server) getWASMBuild(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.Builds == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "WASM builds are unavailable"})
	}
	build, found, err := a.dependencies.Builds.Find(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		return internalError()
	}
	if !found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "WASM build not found"})
	}
	return buildView(build)
}

func (a *Server) getWASMArtifact(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	artifact, data, err := a.dependencies.Builds.Artifact(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		return internalError()
	}
	if !artifact.Found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "WASM artifact not found"})
	}
	contentType := "application/zip"
	if strings.HasSuffix(strings.ToLower(artifact.ArtifactName), ".zst") {
		contentType = "application/zstd"
	}
	return reply{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}, "Content-Disposition": {`attachment; filename="` + artifact.ArtifactName + `"`}}, Body: data}
}

func (a *Server) startWASMRun(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.run", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.Runs == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "WASM runtime unavailable"})
	}
	var input struct {
		Args []string `json:"args"`
	}
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid run request"})
	}
	runID, err := a.dependencies.Runs.Request(c.Request.Context(), owner, c.Param("id"), input.Args)
	if err != nil {
		return jsonResponse(wasmRunErrorStatus(err), map[string]string{"error": err.Error()})
	}
	return reply{StatusCode: http.StatusAccepted, Body: map[string]string{"run_id": runID, "status": "running"}}
}

func (a *Server) getWASMRun(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.Runs == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "WASM runtime unavailable"})
	}
	run, found, err := a.dependencies.Runs.Get(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "WASM runtime unavailable"})
	}
	if !found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "WASM run not found"})
	}
	return run
}

type entrypointInput struct {
	SnapshotID string `json:"snapshot_id"`
	EntryPoint string `json:"entry_point"`
}

func (a *Server) setProjectEntrypoint(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.import", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	var input entrypointInput
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid entrypoint selection"})
	}
	if err := a.dependencies.Catalog.SetEntryPoint(c.Request.Context(), contractprojects.SetEntryPoint{OwnerID: owner, ProjectID: c.Param("id"), SnapshotID: input.SnapshotID, EntryPoint: input.EntryPoint}); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return map[string]string{"entry_point": input.EntryPoint}
}

func (a *Server) authorizedSubject(c *call, action string, resource map[string]any) (string, bool) {
	owner, ok := a.subject(c.Request.Context(), c.Request)
	if !ok {
		return "", false
	}
	if resource == nil {
		resource = map[string]any{}
	}
	resource["owner_id"] = owner
	allowed, err := a.allowed(c.Request.Context(), owner, action, resource)
	return owner, err == nil && allowed
}

// gitHubConnected reports whether the Account has a GitHub Connection. The token
// stays with the identity service; the projects service uses it for the
// Account's repositories.
func (a *Server) gitHubConnected(ctx context.Context, owner string) (bool, error) {
	found, err := a.dependencies.Identity.FindGitHubToken(ctx, owner)
	return found.Connected, err
}
