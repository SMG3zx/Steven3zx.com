package routes

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"janus/actors/persistence"
	projectactor "janus/actors/projects"
	wasmactor "janus/actors/wasm"
	wasmruntime "janus/actors/wasmruntime"
	"janus/internal/domain/templates"
	"janus/web"
)

func (a *Actor) registerProjectRoutes(app *web.App) {
	app.Get("/projects", a.projectsPage)
	app.Get("/projects/new", a.newProjectPage)
	app.Get("/projects/import", a.repoImportPage)
	app.Post("/projects", a.createProjectPage)
	app.Get("/projects/:id", a.projectPage)
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
	app.Get("/api/wasm-runs/:id", a.getWASMRun)
}

func (a *Actor) projectsPage(c *web.Context) any {
	owner, ok := a.subject(c.Request.Context(), c.Request)
	if !ok {
		return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/"}}}
	}
	projects, err := a.dependencies.Store.ListProjects(c.Request.Context(), owner)
	if err != nil {
		return internalError()
	}
	_, connected, tokenErr := a.dependencies.GitHub.Token(c.Request.Context(), owner)
	if tokenErr != nil {
		connected = false
	}
	var body strings.Builder
	if err = projectsListTemplate.Execute(&body, projectsPageData{Projects: projects, GitHubConnected: connected, GitHubEnabled: a.dependencies.GitHubEnabled}); err != nil {
		return internalError()
	}
	return web.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: body.String()}
}

func (a *Actor) newProjectPage(c *web.Context) any {
	if _, ok := a.subject(c.Request.Context(), c.Request); !ok {
		return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/"}}}
	}
	return htmlPage(newProjectTemplate, "new project data", nil)
}

func (a *Actor) repoImportPage(c *web.Context) any {
	owner, ok := a.subject(c.Request.Context(), c.Request)
	if !ok {
		return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/"}}}
	}
	if !a.dependencies.GitHubEnabled {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "GitHub OAuth is not configured"})
	}
	_, connected, err := a.dependencies.GitHub.Token(c.Request.Context(), owner)
	if err != nil {
		return internalError()
	}
	if !connected {
		return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/auth/github/connect"}}}
	}
	var out strings.Builder
	if err = repoImportTemplate.Execute(&out, nil); err != nil {
		return internalError()
	}
	return web.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: out.String()}
}

func (a *Actor) createProjectPage(c *web.Context) any {
	owner, ok := a.subject(c.Request.Context(), c.Request)
	if !ok {
		return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/"}}}
	}
	allowed, authErr := a.allowed(c.Request.Context(), owner, "project.create", nil)
	if authErr != nil {
		return internalError()
	}
	if !allowed {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "project creation is disabled"})
	}
	if err := c.Request.ParseForm(); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid form"})
	}
	created := a.createProject(c, owner, c.Request.FormValue("name"), c.Request.FormValue("description"))
	if response, ok := created.(web.Response); ok && response.StatusCode >= 400 {
		return response
	}
	if project, ok := created.(persistence.Project); ok {
		return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/projects/" + project.ID}}}
	}
	return internalError()
}

func (a *Actor) listProjects(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.read", map[string]any{})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	projects, err := a.dependencies.Store.ListProjects(c.Request.Context(), owner)
	if err != nil {
		return internalError()
	}
	return projects
}

type createProjectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (a *Actor) createProjectAPI(c *web.Context) any {
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
	if failure, ok := created.(web.Response); ok {
		return failure
	}
	project, ok := created.(persistence.Project)
	if !ok {
		return internalError()
	}
	return web.Response{StatusCode: http.StatusCreated, Body: project}
}

func (a *Actor) createProject(c *web.Context, owner, name, description string) any {
	if strings.TrimSpace(name) == "" || len(name) > 120 || len(description) > 2000 {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "project name (1-120 characters) and description (up to 2000 characters) are required"})
	}
	project, err := a.dependencies.Store.CreateProject(c.Request.Context(), persistence.CreateProject{ID: uuid.NewString(), OwnerID: owner, Name: strings.TrimSpace(name), Description: strings.TrimSpace(description)})
	if err != nil {
		if persistence.IsReply(err) {
			return jsonResponse(http.StatusConflict, map[string]string{"error": "project could not be created"})
		}
		return internalError()
	}
	return project
}

func (a *Actor) getProjectAPI(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	project, found, err := a.dependencies.Store.GetProject(c.Request.Context(), c.Param("id"), owner)
	if err != nil {
		return internalError()
	}
	if !found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "project not found"})
	}
	return project
}

func (a *Actor) listProjectSnapshots(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	snapshots, err := a.dependencies.Store.ListSnapshots(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		if persistence.IsReply(err) {
			return jsonResponse(http.StatusNotFound, map[string]string{"error": "project not found"})
		}
		return internalError()
	}
	return snapshots
}

func (a *Actor) activateProjectSnapshot(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.update", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	activated, err := a.dependencies.Store.ActivateSnapshot(c.Request.Context(), owner, c.Param("id"), c.Param("snapshot"))
	if err != nil {
		return internalError()
	}
	if !activated {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "snapshot not found"})
	}
	a.log(c.Request.Context(), "info", "project.snapshot.activated", map[string]string{"user_id": owner, "project_id": c.Param("id"), "snapshot_id": c.Param("snapshot")})
	return map[string]bool{"active": true}
}

func (a *Actor) updateProjectAPI(c *web.Context) any {
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
	project, err := a.dependencies.Store.UpdateProject(c.Request.Context(), persistence.UpdateProject{ID: c.Param("id"), OwnerID: owner, Name: input.Name, Description: input.Description})
	if err != nil {
		if err.Error() == "project not found" {
			return jsonResponse(http.StatusNotFound, map[string]string{"error": "project not found"})
		}
		return internalError()
	}
	return project
}

func (a *Actor) deleteProjectAPI(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.delete", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	deleted, err := a.dependencies.Store.DeleteProject(c.Request.Context(), c.Param("id"), owner)
	if err != nil {
		if persistence.IsReply(err) {
			return jsonResponse(http.StatusConflict, map[string]string{"error": err.Error()})
		}
		return internalError()
	}
	if !deleted {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "project not found"})
	}
	return web.Response{StatusCode: http.StatusNoContent}
}

func (a *Actor) listGitHubRepos(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	list, err := a.dependencies.GitHub.Repositories(c.Request.Context(), owner)
	if err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return list
}
func (a *Actor) listGitHubBranches(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	branches, err := a.dependencies.GitHub.Branches(c.Request.Context(), owner, c.Query.Get("repository"))
	if err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return branches
}
func (a *Actor) listGitHubCommits(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	commits, err := a.dependencies.GitHub.Commits(c.Request.Context(), owner, c.Query.Get("repository"), c.Query.Get("branch"))
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

func (a *Actor) startProjectImport(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.import", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	var input importInput
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid import request"})
	}
	response, err := a.ask(c.Request.Context(), a.dependencies.Projects, &projectactor.RequestImport{OwnerID: owner, ProjectID: c.Param("id"), Repository: input.Repository, Branch: input.Branch, Commit: input.Commit})
	if err != nil {
		return internalError()
	}
	result, ok := response.(*projectactor.ImportRequested)
	if !ok {
		return internalError()
	}
	if result.Err != nil {
		a.log(c.Request.Context(), "warn", "project.import.request.rejected", map[string]string{
			"user_id": owner, "project_id": c.Param("id"), "reason": result.Err.Error(),
		})
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": result.Err.Error()})
	}
	return web.Response{StatusCode: http.StatusAccepted, Body: map[string]string{"job_id": result.JobID, "status": "queued"}}
}

func (a *Actor) getImportStatus(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	job, found, err := a.dependencies.Store.FindImportJob(c.Request.Context(), c.Param("id"), owner)
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

func (a *Actor) startWASMBuild(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.build", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.WASMBuilds == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "WASM builds are unavailable"})
	}
	var input wasmBuildInput
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "select a supported WASM target"})
	}
	response, err := a.ask(c.Request.Context(), a.dependencies.WASMBuilds, &wasmactor.RequestBuild{OwnerID: owner, ProjectID: c.Param("id"), Target: input.Target})
	if err != nil {
		return internalError()
	}
	result, ok := response.(*wasmactor.BuildRequested)
	if !ok {
		return internalError()
	}
	if result.Err != nil {
		return jsonResponse(wasmBuildErrorStatus(result.Err), map[string]string{"error": result.Err.Error()})
	}
	a.log(c.Request.Context(), "info", "project.wasm.build.requested", map[string]string{"user_id": owner, "project_id": c.Param("id"), "job_id": result.JobID, "target": input.Target})
	return web.Response{StatusCode: http.StatusAccepted, Body: map[string]string{"job_id": result.JobID, "status": "queued"}}
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

func (a *Actor) getWASMBuild(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.WASMBuilds == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "WASM builds are unavailable"})
	}
	response, err := a.ask(c.Request.Context(), a.dependencies.WASMBuilds, &wasmactor.Build{OwnerID: owner, JobID: c.Param("id")})
	if err != nil {
		return internalError()
	}
	result, ok := response.(*wasmactor.BuildFound)
	if !ok || result.Err != nil {
		return internalError()
	}
	if !result.Found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "WASM build not found"})
	}
	return result.Build
}

func (a *Actor) getWASMArtifact(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	artifact, err := a.dependencies.Store.FindWASMBuildArtifact(c.Request.Context(), c.Param("id"), owner)
	if err != nil {
		return internalError()
	}
	if !artifact.Found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "WASM artifact not found"})
	}
	contentType := "application/zip"
	if strings.HasSuffix(strings.ToLower(artifact.Name), ".zst") {
		contentType = "application/zstd"
	}
	return web.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}, "Content-Disposition": {`attachment; filename="` + artifact.Name + `"`}}, Body: artifact.Data}
}

func (a *Actor) startWASMRun(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.run", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.WASMRuntime == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "WASM runtime unavailable"})
	}
	var input struct {
		Args []string `json:"args"`
	}
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid run request"})
	}
	response, err := a.ask(c.Request.Context(), a.dependencies.WASMRuntime, &wasmruntime.RequestRun{OwnerID: owner, BuildID: c.Param("id"), Args: input.Args})
	if err != nil {
		return internalError()
	}
	result, ok := response.(*wasmruntime.RunRequested)
	if !ok {
		return internalError()
	}
	if result.Err != nil {
		return jsonResponse(wasmBuildErrorStatus(result.Err), map[string]string{"error": result.Err.Error()})
	}
	return web.Response{StatusCode: http.StatusAccepted, Body: map[string]string{"run_id": result.RunID, "status": "running"}}
}

func (a *Actor) getWASMRun(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.read", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	if a.dependencies.WASMRuntime == nil {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "WASM runtime unavailable"})
	}
	response, err := a.ask(c.Request.Context(), a.dependencies.WASMRuntime, &wasmruntime.GetRun{OwnerID: owner, RunID: c.Param("id")})
	if err != nil {
		return internalError()
	}
	result, ok := response.(*wasmruntime.RunFound)
	if !ok || result.Err != nil {
		return internalError()
	}
	if !result.Found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "WASM run not found"})
	}
	return result.Run
}

type entrypointInput struct {
	SnapshotID string `json:"snapshot_id"`
	EntryPoint string `json:"entry_point"`
}

func (a *Actor) setProjectEntrypoint(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.import", map[string]any{"owner_id": ""})
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	var input entrypointInput
	if err := decodeJSON(c.Request, &input); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid entrypoint selection"})
	}
	response, err := a.ask(c.Request.Context(), a.dependencies.Projects, &projectactor.SetEntryPoint{OwnerID: owner, ProjectID: c.Param("id"), SnapshotID: input.SnapshotID, EntryPoint: input.EntryPoint})
	if err != nil {
		return internalError()
	}
	result, ok := response.(*projectactor.EntryPointSet)
	if !ok {
		return internalError()
	}
	if result.Err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": result.Err.Error()})
	}
	return map[string]string{"entry_point": input.EntryPoint}
}

func (a *Actor) projectPage(c *web.Context) any {
	owner, ok := a.subject(c.Request.Context(), c.Request)
	if !ok {
		return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/"}}}
	}
	allowed, authErr := a.allowed(c.Request.Context(), owner, "project.read", map[string]any{"owner_id": owner})
	if authErr != nil {
		return internalError()
	}
	if !allowed {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "forbidden"})
	}
	project, found, err := a.dependencies.Store.GetProject(c.Request.Context(), c.Param("id"), owner)
	if err != nil {
		return internalError()
	}
	if !found {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "project not found"})
	}
	_, connected, tokenErr := a.dependencies.GitHub.Token(c.Request.Context(), owner)
	if tokenErr != nil {
		connected = false
	}
	var metadata projectMetadata
	_ = json.Unmarshal([]byte(project.MetadataJSON), &metadata)
	snapshots, err := a.dependencies.Store.ListSnapshots(c.Request.Context(), owner, project.ID)
	if err != nil {
		return internalError()
	}
	buildTemplate, canBuildWASM := templates.Buildable(project.Language)
	if !canBuildWASM {
		for _, candidate := range metadata.Scan.Candidates {
			if tpl, ok := templates.Buildable(candidate.Ecosystem); ok && tpl.ID == "rust" {
				buildTemplate, canBuildWASM = tpl, true
				break
			}
		}
	}
	data := projectPageData{Project: project, GitHubConnected: connected, GitHubEnabled: a.dependencies.GitHubEnabled, CanBuildWASM: canBuildWASM, BuildTargets: buildTemplate.Targets, Candidates: metadata.Scan.Candidates, Snapshots: snapshots}
	var page strings.Builder
	if err = projectTemplate.Execute(&page, data); err != nil {
		return internalError()
	}
	return web.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: page.String()}
}

type projectMetadata struct {
	Scan struct {
		Candidates []projectactor.Candidate `json:"entrypoint_candidates"`
	}
}
type projectsPageData struct {
	Projects                       []persistence.Project
	GitHubConnected, GitHubEnabled bool
}
type projectPageData struct {
	Project                        persistence.Project
	GitHubConnected, GitHubEnabled bool
	CanBuildWASM                   bool
	BuildTargets                   []string
	Candidates                     []projectactor.Candidate
	Snapshots                      []persistence.ProjectSnapshot
}

var snapshotHistoryTemplate = template.Must(template.New("snapshot-history").Funcs(template.FuncMap{"formatMB": formatMB}).Parse(`<section style="font:16px system-ui;background:#101827;color:#e6edf7;max-width:900px;margin:0 auto 3rem;padding:1rem"><h2>Repository snapshots</h2><p>The five most recent successful imports are kept. Activating a snapshot switches the project back to that saved revision.</p><ol>{{range .Snapshots}}<li style="padding:.6rem 0">{{.Repository}} · {{.Branch}} @ {{printf "%.7s" .Commit}} — {{.FileCount}} files, {{formatMB .TotalBytes}}{{if .Active}} · <strong>Active</strong>{{else}} <button type="button" onclick="activateSnapshot('{{.ID}}')">Use this snapshot</button>{{end}}</li>{{else}}<li>No imported snapshots yet.</li>{{end}}</ol><p id="snapshot-status" role="status"></p><script id="snapshot-history" data-project-id="{{.Project.ID}}">const snapshotProjectID=document.getElementById('snapshot-history').dataset.projectId;async function activateSnapshot(id){const status=document.getElementById('snapshot-status');status.textContent='Switching snapshot…';try{const response=await fetch('/api/projects/'+encodeURIComponent(snapshotProjectID)+'/snapshots/'+encodeURIComponent(id)+'/activate',{method:'POST'});if(!response.ok){const body=await response.json().catch(()=>({}));throw new Error(body.error||'Could not activate snapshot')}location.reload()}catch(error){status.textContent=error.message}}</script></section>`))

var wasmBuildTemplate = template.Must(template.New("wasm-build").Parse(`<section style="font:16px system-ui;background:#101827;color:#e6edf7;max-width:900px;margin:0 auto 3rem;padding:1rem"><h2>Build WebAssembly</h2><p>Compile this imported snapshot in an isolated, network-disabled builder.</p><label>Target <select id="wasm-target">{{if eq .Language "Go"}}<option value="wasip1-wasm">wasip1-wasm (Go/WASI)</option>{{else}}<option value="wasm32-unknown-unknown">wasm32-unknown-unknown</option><option value="wasm32-wasip1">wasm32-wasip1</option>{{end}}</select></label><button type="button" onclick="buildWASM()">Build WASM</button><p id="wasm-status" role="status"></p><pre id="wasm-log" style="white-space:pre-wrap;overflow-wrap:anywhere"></pre><a id="wasm-download" hidden>Download WASM artifacts</a><script id="wasm-build-actions" data-project-id="{{.ID}}">const wasmProjectID=document.getElementById('wasm-build-actions').dataset.projectId;async function buildWASM(){const status=document.getElementById('wasm-status'),log=document.getElementById('wasm-log'),download=document.getElementById('wasm-download');download.hidden=true;log.textContent='';try{const response=await fetch('/api/projects/'+encodeURIComponent(wasmProjectID)+'/wasm-builds',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({target:document.getElementById('wasm-target').value})});const queued=await response.json().catch(()=>({}));if(!response.ok)throw new Error(queued.error||'Could not start WASM build');status.textContent='WASM build queued…';let job;do{await new Promise(resolve=>setTimeout(resolve,1000));const current=await fetch('/api/wasm-builds/'+encodeURIComponent(queued.job_id));job=await current.json();if(!current.ok)throw new Error(job.error||'Could not read build status');status.textContent='WASM build '+job.status+' ('+job.progress+'%)';log.textContent=job.log||''}while(job.status==='queued'||job.status==='building');if(job.status==='complete'){download.href='/api/wasm-builds/'+encodeURIComponent(job.id)+'/artifact';download.textContent='Download '+(job.artifact_name||'WASM artifact');download.hidden=false}else{status.textContent=job.error||'WASM build failed'}}catch(error){status.textContent=error.message}}</script></section>`))

func formatMB(bytes int64) string {
	return fmt.Sprintf("%.2f MB", float64(bytes)/1_000_000)
}

var projectsListTemplate = template.Must(template.New("projects").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Projects · Janus</title><style>body{font:16px system-ui;background:#101827;color:#e6edf7;max-width:900px;margin:3rem auto;padding:0 1rem}a{color:#83b7ff}li{padding:.6rem}</style><h1>Your projects</h1><p>{{if .GitHubConnected}}GitHub connected{{else if .GitHubEnabled}}<a href="/auth/github/connect">Connect GitHub</a> to import repositories.{{else}}GitHub import is not configured.{{end}}</p><p><a href="/projects/new">Create blank project</a>{{if .GitHubConnected}} · <a href="/projects/import">Create from GitHub</a>{{end}} · <a href="/">Home</a></p><ul>{{range .Projects}}<li><a href="/projects/{{.ID}}">{{.Name}}</a>{{if .Repository}} — {{.Repository}} ({{.Branch}} @ {{printf "%.7s" .Commit}}){{end}}</li>{{else}}<li>No projects yet.</li>{{end}}</ul>{{if .GitHubConnected}}<form method="post" action="/auth/github/disconnect"><button>Disconnect GitHub</button></form>{{end}}</html>`))
var newProjectTemplate = template.Must(template.New("new-project").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>New project · Janus</title><style>body{font:16px system-ui;background:#101827;color:#e6edf7;max-width:700px;margin:3rem auto;padding:0 1rem}input,textarea,button{display:block;width:100%;box-sizing:border-box;margin:.7rem 0;padding:.7rem;background:#182438;color:inherit;border:1px solid #48607e;border-radius:.4rem}a{color:#83b7ff}</style><h1>Create project</h1><form method="post" action="/projects"><label>Name<input name="name" required maxlength="120"></label><label>Description<textarea name="description" maxlength="2000"></textarea></label><button>Create</button></form><p><a href="/projects">Cancel</a></p></html>`))

var repoImportTemplate = template.Must(template.New("repo-import").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Create from GitHub · Janus</title><style>body{font:16px system-ui;background:#101827;color:#e6edf7;max-width:800px;margin:3rem auto;padding:0 1rem}a{color:#83b7ff}input,select,button{display:block;width:100%;box-sizing:border-box;margin:.7rem 0;padding:.7rem;background:#182438;color:inherit;border:1px solid #48607e;border-radius:.4rem}small{color:#aab8cc}</style><h1>Create project from GitHub</h1><form id="create"><label>Project name<input id="name" required maxlength="120"></label><label>Description<input id="description" maxlength="2000"></label><label>Repository<select id="repo"></select></label><label>Branch<select id="branch"></select></label><label>Commit (latest first)<select id="commit"></select></label><button>Create project and import</button></form><p id="status" role="status"></p><p><a href="/projects">Cancel</a></p><script>let repositories=[];const status=document.getElementById('status');async function request(url,options){const response=await fetch(url,options);const body=await response.json().catch(()=>({}));if(!response.ok)throw new Error(body.error||'Request failed');return body}async function loadCommits(){const repo=document.getElementById('repo').value,branch=document.getElementById('branch').value;const data=await request('/api/github/commits?repository='+encodeURIComponent(repo)+'&branch='+encodeURIComponent(branch));const select=document.getElementById('commit');select.innerHTML='';for(const c of data){const o=document.createElement('option');o.value=c.sha;o.textContent=c.sha.slice(0,7)+' · '+c.message.split('\n')[0];select.appendChild(o)}}async function loadBranches(){const repo=document.getElementById('repo').value;const data=await request('/api/github/branches?repository='+encodeURIComponent(repo));const select=document.getElementById('branch');select.innerHTML='';for(const b of data){const o=document.createElement('option');o.value=b.name;o.textContent=b.name;select.appendChild(o)}const selected=repositories.find(r=>r.full_name===repo);if(selected&&Array.from(select.options).some(o=>o.value===selected.default_branch))select.value=selected.default_branch;await loadCommits()}async function loadRepos(){const data=await request('/api/github/repos');repositories=data.repositories;const select=document.getElementById('repo');select.innerHTML='';for(const r of repositories){const o=document.createElement('option');o.value=r.full_name;o.textContent=r.full_name+(r.private?' · private':'');select.appendChild(o)}const preferred=repositories.find(r=>r.default_branch);if(preferred)select.value=preferred.full_name;if(select.options.length)await loadBranches()}document.getElementById('repo').addEventListener('change',()=>loadBranches().catch(e=>status.textContent=e.message));document.getElementById('branch').addEventListener('change',()=>loadCommits().catch(e=>status.textContent=e.message));document.getElementById('create').addEventListener('submit',async event=>{event.preventDefault();try{status.textContent='Creating project…';const project=await request('/api/projects',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:document.getElementById('name').value,description:document.getElementById('description').value})});const job=await request('/api/projects/'+project.id+'/imports',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({repository:document.getElementById('repo').value,branch:document.getElementById('branch').value,commit:document.getElementById('commit').value})});status.textContent='Import queued…';let state;do{await new Promise(resolve=>setTimeout(resolve,1000));state=await request('/api/imports/'+job.job_id);status.textContent='Import '+state.status+' ('+state.progress+'%)'}while(state.status!=='complete'&&state.status!=='failed');if(state.status==='complete'){location.href='/projects/'+project.id}else{status.textContent=(state.error||'Import failed')+' Project created: '+project.name+' ('+project.id+')'}}catch(error){status.textContent=error.message}});loadRepos().catch(error=>status.textContent=error.message)</script></html>`))

var projectTemplate = template.Must(template.New("project").Funcs(template.FuncMap{"formatMB": formatMB}).Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>{{.Project.Name}} · Janus</title><style>
:root{color-scheme:dark}*{box-sizing:border-box}body{font:16px/1.55 system-ui,sans-serif;background:radial-gradient(ellipse at 50% -20%,#233d61 0,transparent 55%),#0b1220;color:#e6edf7;max-width:980px;margin:2.5rem auto;padding:0 1.25rem}a{color:#8dc1ff}h1{font-size:clamp(1.8rem,4vw,2.6rem);margin:.4rem 0}.project-content{display:grid;gap:1rem;margin:1.5rem 0 3rem}.project-card{padding:1.4rem;background:linear-gradient(145deg,#17243a,#111a2a);border:1px solid #344b69;border-radius:1rem;box-shadow:0 16px 36px #03081266}.project-card h2{font-size:1.2rem;margin:.1rem 0 .75rem}.project-card p{margin:.5rem 0 1rem}.card-kicker{margin:0!important;color:#91a8c4;font-size:.75rem;font-weight:750;letter-spacing:.1em;text-transform:uppercase}input,select,button{padding:.65rem .8rem;background:#1a2a41;color:inherit;border:1px solid #496383;border-radius:.55rem;margin:.3rem}button{cursor:pointer;font-weight:650}button:hover{background:#263c5a}label{display:inline-block;margin:.25rem .5rem .25rem 0}.snapshot-list{padding-left:1.3rem}.snapshot-list li{padding:.7rem .2rem;border-bottom:1px solid #2b3a50}.snapshot-list li:last-child{border-bottom:0}.progress-panel{margin:1rem 0;padding:1rem;background:#0c1422;border:1px solid #2e435e;border-radius:.75rem}.progress-heading{display:flex;justify-content:space-between;gap:1rem;align-items:center}.progress-track{height:.65rem;background:#25344a;border-radius:99px;overflow:hidden;margin:.75rem 0}.progress-fill{height:100%;width:0;background:linear-gradient(90deg,#3b82f6,#54d6a0);transition:width .35s ease}.progress-meta{color:#a9bad0;font-size:.9rem}.build-log{max-height:18rem;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;padding:.9rem;background:#070c14;border:1px solid #24354b;border-radius:.55rem;color:#c8d7e8;font: .83rem/1.5 ui-monospace,monospace}.entrypoint{margin-top:1rem;padding-top:1rem;border-top:1px solid #2b3a50}small{color:#aab8cc}[hidden]{display:none!important}
.build-failure{margin:.75rem 0;padding:.9rem 1rem;border:1px solid #9e4b58;border-left:4px solid #ef6576;border-radius:.55rem;background:#321a25;color:#ffdce0}.build-help{margin:.75rem 0;padding:.85rem 1rem;border:1px solid #3b506d;border-radius:.55rem;background:#142033;color:#c9d8eb}.build-help ul{margin:.4rem 0;padding-left:1.3rem}.build-empty{color:#9fb0c5;font-style:italic}.build-controls{display:flex;flex-wrap:wrap;gap:.5rem;align-items:center}.build-controls button{margin-left:0}.build-log-wrap{margin-top:.8rem}.build-log-wrap summary{cursor:pointer;color:#aecdF0;font-weight:650}.build-log-empty{color:#92a5bc;font: .85rem/1.5 ui-monospace,monospace;padding:.8rem;background:#070c14;border-radius:.55rem}\n</style><p><a href="/projects">← Projects</a></p><p class="card-kicker">Project workspace</p><h1>{{.Project.Name}}</h1><p>{{.Project.Description}}</p><main class="project-content">
{{if .GitHubEnabled}}<section class="project-card"><p class="card-kicker">01 · Source</p><h2>Import from GitHub</h2>{{if .GitHubConnected}}<p>Select a repository revision to import into this project.</p><label>Repository <select id="repo"></select></label><label>Branch <select id="branch"></select></label><label>Commit <select id="commit"></select></label><p><button type="button" onclick="startImport()">Import selected commit</button></p><div id="import-progress" class="progress-panel" hidden><div class="progress-heading"><strong id="import-status" role="status">Preparing import…</strong><span id="import-percent">0%</span></div><div class="progress-track"><div id="import-progress-fill" class="progress-fill"></div></div><p id="import-detail" class="progress-meta">Waiting for the import worker</p><pre id="import-log" class="build-log" hidden></pre></div>{{else}}<p><a href="/auth/github/connect">Connect GitHub</a> to choose a repository.</p>{{end}}</section>{{end}}
{{if .Project.Repository}}<section class="project-card"><p class="card-kicker">02 · Current revision</p><h2>Imported repository</h2><p>{{.Project.Repository}} · {{.Project.Branch}} @ {{printf "%.7s" .Project.Commit}}</p><p>{{.Project.FileCount}} files · {{formatMB .Project.TotalBytes}} · {{.Project.Language}}{{if .Project.Runtime}} ({{.Project.Runtime}}){{end}} · {{.Project.Stars}} stars · {{.Project.Forks}} forks</p><p>Entrypoint: {{if .Project.EntryPoint}}{{.Project.EntryPoint}}{{else}}not selected{{end}}</p>{{if .Candidates}}<div class="entrypoint"><label>Confirm entrypoint <select id="entrypoint"><option value="">No entrypoint</option>{{range .Candidates}}<option value="{{.Path}}" {{if eq $.Project.EntryPoint .Path}}selected{{end}}>{{.Path}} — {{.Reason}}</option>{{end}}</select></label><button type="button" onclick="saveEntryPoint()">Save entrypoint</button></div>{{end}}</section>{{else}}<section class="project-card"><p class="card-kicker">02 · Current revision</p><h2>Imported repository</h2><p>No repository has been imported into this project yet.</p></section>{{end}}
<section class="project-card"><p class="card-kicker">03 · History</p><h2>Repository snapshots</h2><p>The five most recent successful imports are kept. Activating a snapshot switches the project back to that saved revision.</p><ol class="snapshot-list">{{range .Snapshots}}<li>{{.Repository}} · {{.Branch}} @ {{printf "%.7s" .Commit}} — {{.FileCount}} files, {{formatMB .TotalBytes}}{{if .Active}} · <strong>Active</strong>{{else}} <button type="button" onclick="activateSnapshot('{{.ID}}')">Use this snapshot</button>{{end}}</li>{{else}}<li>No imported snapshots yet.</li>{{end}}</ol><p id="snapshot-status" role="status"></p></section>
{{if and .CanBuildWASM .Project.SnapshotID}}<section class="project-card"><p class="card-kicker">04 · Compile</p><h2>Build WebAssembly</h2><p>Build the active snapshot in an isolated, network-disabled builder.</p><div class="build-controls"><label>Target <select id="wasm-target">{{range .BuildTargets}}<option value="{{.}}">{{.}}</option>{{end}}</select></label><button id="build-button" type="button" onclick="buildWASM()">Build WASM</button></div><div id="build-progress" class="progress-panel" hidden><div class="progress-heading"><strong id="wasm-status" role="status">Waiting for build…</strong><span id="wasm-percent">0%</span></div><div class="progress-track"><div id="wasm-progress-fill" class="progress-fill"></div></div><p id="wasm-detail" class="progress-meta">The container build will report its current stage here.</p><div id="build-failure" class="build-failure" role="alert" hidden></div><div id="build-help" class="build-help" hidden></div><details class="build-log-wrap" open><summary>Build output</summary><pre id="wasm-log" class="build-log"></pre><p id="wasm-log-empty" class="build-log-empty" hidden>No compiler output was captured. Check the error details above for setup or Docker availability issues.</p></details><a id="wasm-download" hidden>Download WASM artifacts</a></div></section>{{end}}
</main><script id="project-actions" data-project-id="{{.Project.ID}}" data-snapshot-id="{{.Project.SnapshotID}}">const projectData=document.getElementById('project-actions').dataset;const projectID=projectData.projectId;const snapshotID=projectData.snapshotId;const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));async function requestJSON(url,options){const response=await fetch(url,options);const body=await response.json().catch(()=>({}));if(!response.ok)throw new Error(body.error||'Request failed');return body}async function loadRepos(){const result=await requestJSON('/api/github/repos');const select=document.getElementById('repo');for(const repo of result.repositories){const option=document.createElement('option');option.value=repo.full_name;option.textContent=repo.full_name+(repo.private?' · private':'');select.appendChild(option)}if(select.options.length)await loadBranches()}async function loadBranches(){const repo=document.getElementById('repo').value;const branches=await requestJSON('/api/github/branches?repository='+encodeURIComponent(repo));const select=document.getElementById('branch');select.innerHTML='';for(const branch of branches){const option=document.createElement('option');option.value=branch.name;option.textContent=branch.name;select.appendChild(option)}await loadCommits()}async function loadCommits(){const repo=document.getElementById('repo').value,branch=document.getElementById('branch').value;const commits=await requestJSON('/api/github/commits?repository='+encodeURIComponent(repo)+'&branch='+encodeURIComponent(branch));const select=document.getElementById('commit');select.innerHTML='';for(const commit of commits){const option=document.createElement('option');option.value=commit.sha;option.textContent=commit.sha.slice(0,7)+' · '+commit.message.split('\n')[0];select.appendChild(option)}}async function startImport(){const panel=document.getElementById('import-progress'),status=document.getElementById('import-status'),percent=document.getElementById('import-percent'),fill=document.getElementById('import-progress-fill'),detail=document.getElementById('import-detail');panel.hidden=false;status.textContent='Queueing import…';detail.textContent=document.getElementById('repo').value+' · '+document.getElementById('branch').value;try{const queued=await requestJSON('/api/projects/'+encodeURIComponent(projectID)+'/imports',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({repository:document.getElementById('repo').value,branch:document.getElementById('branch').value,commit:document.getElementById('commit').value})});let job;do{job=await requestJSON('/api/imports/'+encodeURIComponent(queued.job_id));const progress=Math.max(0,Math.min(100,Number(job.progress)||0));status.textContent='Import '+job.status;percent.textContent=progress+'%';fill.style.width=progress+'%';detail.textContent=job.error||({queued:'Waiting for an import worker',cloning:'Fetching repository data from GitHub',scanning:'Scanning repository files and detecting entrypoints',storing:'Saving files and creating snapshot',complete:'Snapshot is ready',failed:'Import failed'}[job.status]||'Processing repository');await pause(900)}while(job.status!=='complete'&&job.status!=='failed');if(job.status==='complete'){fill.style.width='100%';percent.textContent='100%';detail.textContent='Import complete. Loading the imported repository…';await pause(500);location.reload()}else detail.textContent=job.error||'Import failed'}catch(error){status.textContent='Import failed';detail.textContent=error.message}}
async function activateSnapshot(id){const status=document.getElementById('snapshot-status');status.textContent='Switching snapshot…';try{await requestJSON('/api/projects/'+encodeURIComponent(projectID)+'/snapshots/'+encodeURIComponent(id)+'/activate',{method:'POST'});location.reload()}catch(error){status.textContent=error.message}}async function saveEntryPoint(){const entry=document.getElementById('entrypoint').value;try{await requestJSON('/api/projects/'+encodeURIComponent(projectID)+'/entrypoint',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({snapshot_id:snapshotID,entry_point:entry})});location.reload()}catch(error){alert(error.message)}}function buildFailureGuidance(error,target){const message=(error||'').toLowerCase();const checks=[];if(message.includes('require a cargo.toml'))checks.push('This snapshot is missing Cargo.toml at its repository root. Import a Rust repository with a root Cargo.toml, or select a snapshot that has one.');if(message.includes('require a go.mod'))checks.push('This snapshot is missing go.mod at its repository root. Import a Go repository with a root go.mod.');if(message.includes('configure a trusted, pre-provisioned wasm builder image'))checks.push('Janus could not find a configured Go WASM builder image, so it stopped before starting a compiler container. Set JANUS_GO_WASM_BUILDER_IMAGE=janus-go-wasm-builder:local, build the image with: docker build -f build/Dockerfile.wasm-go -t janus-go-wasm-builder:local . Then restart Janus. The log confirms the Docker daemon itself was ready.');else if(message.includes('builder is not configured')||message.includes('builder unavailable'))checks.push('The server has no configured builder for this language. Configure the matching JANUS_WASM_BUILDER_IMAGE or JANUS_GO_WASM_BUILDER_IMAGE and ensure the image is available to Docker.');if(message.includes('no such file')||message.includes('cannot find')||message.includes('not found'))checks.push('A compiler, target, or build tool may be missing from the builder image. Check that the configured builder image was built from the matching build/Dockerfile and includes its target toolchain.');if(message.includes('network')||message.includes('offline')||message.includes('package')||message.includes('dependency')||message.includes('failed to get'))checks.push('Build containers have no network and dependency lookup is offline. Vendor dependencies or add them to the trusted builder image cache, then rebuild.');if(message.includes('docker')||message.includes('daemon')||message.includes('container'))checks.push('Check that Docker Desktop is running and that the configured builder image is present locally. Builds run in an isolated container with no network.');if(message.includes('timed out')||message.includes('deadline'))checks.push('The build exceeded its 10 minute limit. Reduce build work or dependencies and retry.');if(message.includes('128 mib')||message.includes('artifact size'))checks.push('The generated artifact exceeded the 128 MiB output limit. Reduce the build output size and retry.');if(checks.length===0)checks.push('Review the full build output below. Confirm the '+target+' target is supported by this repository and that its dependencies are available offline in the builder.');return checks}function showBuildFailure(error,target){const box=document.getElementById('build-failure'),help=document.getElementById('build-help');box.textContent=error||'The build stopped without a detailed error message.';box.hidden=false;help.replaceChildren();const title=document.createElement('strong');title.textContent='Things to check';const list=document.createElement('ul');for(const item of buildFailureGuidance(error,target)){const li=document.createElement('li');li.textContent=item;list.appendChild(li)}help.append(title,list);help.hidden=false}async function buildWASM(){const panel=document.getElementById('build-progress'),status=document.getElementById('wasm-status'),percent=document.getElementById('wasm-percent'),fill=document.getElementById('wasm-progress-fill'),detail=document.getElementById('wasm-detail'),log=document.getElementById('wasm-log'),logEmpty=document.getElementById('wasm-log-empty'),failure=document.getElementById('build-failure'),help=document.getElementById('build-help'),download=document.getElementById('wasm-download'),button=document.getElementById('build-button'),target=document.getElementById('wasm-target').value;panel.hidden=false;failure.hidden=true;help.hidden=true;log.textContent='';logEmpty.hidden=true;download.hidden=true;button.disabled=true;status.textContent='Starting container build…';detail.textContent='Preparing the active repository snapshot';fill.style.width='0%';percent.textContent='0%';try{const queued=await requestJSON('/api/projects/'+encodeURIComponent(projectID)+'/wasm-builds',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({target})});let job;do{await pause(900);job=await requestJSON('/api/wasm-builds/'+encodeURIComponent(queued.job_id));const progress=Math.max(0,Math.min(100,Number(job.progress)||0));status.textContent=job.status==='failed'?'Build stopped during '+(job.target||target):'Build '+job.status;percent.textContent=progress+'%';fill.style.width=progress+'%';detail.textContent=job.status==='queued'?'Waiting for a build worker':job.status==='building'?'Docker is compiling the active snapshot':job.status==='complete'?'Build completed successfully':'Build stopped; see the error and output below';log.textContent=job.log||'';log.hidden=!job.log;logEmpty.hidden=Boolean(job.log);log.scrollTop=log.scrollHeight;if(job.status==='failed')showBuildFailure(job.error,target);await pause(0)}while(job.status==='queued'||job.status==='building');if(job.status==='complete'){fill.style.width='100%';percent.textContent='100%';download.href='/api/wasm-builds/'+encodeURIComponent(job.id)+'/artifact';download.textContent='Download '+(job.artifact_name||'WASM artifact');download.hidden=false;detail.textContent='Build complete. Artifact: '+(job.artifact_name||'WASM artifact')}else if(job.status!=='failed'){status.textContent='Build ended with status: '+job.status;detail.textContent=job.error||'The server returned an unexpected build status.';showBuildFailure(job.error||('Unexpected build status: '+job.status),target)}}catch(error){status.textContent='Could not complete build request';detail.textContent='The build status could not be retrieved. The job may still be running; retry after checking the server connection.';showBuildFailure(error.message,target)}finally{button.disabled=false}}const repoSelect=document.getElementById('repo');if(repoSelect){loadRepos().catch(error=>{const panel=document.getElementById('import-progress');panel.hidden=false;document.getElementById('import-status').textContent='Could not load repositories';document.getElementById('import-detail').textContent=error.message});repoSelect.addEventListener('change',()=>loadBranches().catch(error=>{document.getElementById('import-progress').hidden=false;document.getElementById('import-detail').textContent=error.message}));document.getElementById('branch').addEventListener('change',()=>loadCommits().catch(error=>{document.getElementById('import-progress').hidden=false;document.getElementById('import-detail').textContent=error.message}))}</script></html>`))

func htmlPage(t *template.Template, name string, data any) web.Response {
	var out strings.Builder
	if err := t.Execute(&out, data); err != nil {
		return internalError()
	}
	return web.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: out.String()}
}
func (a *Actor) authorizedSubject(c *web.Context, action string, resource map[string]any) (string, bool) {
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

var _ = fmt.Sprintf
var _ = json.Valid
