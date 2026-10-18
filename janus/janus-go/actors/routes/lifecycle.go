package routes

import (
	"net/http"

	projectactor "janus/actors/projects"
	wasmactor "janus/actors/wasm"
	"janus/web"
)

// registerLifecycleRoutes adds the routes for retrying failed jobs and deleting
// an Account.
func (a *Actor) registerLifecycleRoutes(app *web.App) {
	app.Post("/api/imports/:id/retry", a.retryImportAPI)
	app.Post("/api/wasm-builds/:id/retry", a.retryWASMBuildAPI)
	app.Delete("/api/account", a.deleteAccountAPI)
}

func (a *Actor) retryImportAPI(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.import", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	response, err := a.ask(c.Request.Context(), a.dependencies.Projects, &projectactor.RetryImport{OwnerID: owner, JobID: c.Param("id")})
	if err != nil {
		return internalError()
	}
	result, ok := response.(*projectactor.ImportRequested)
	if !ok {
		return internalError()
	}
	if result.Err != nil {
		return jsonResponse(importErrorStatus(result.Err), map[string]string{"error": result.Err.Error()})
	}
	return jsonResponse(http.StatusAccepted, map[string]string{"job_id": result.JobID})
}

func (a *Actor) retryWASMBuildAPI(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "project.build", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	response, err := a.ask(c.Request.Context(), a.dependencies.WASMBuilds, &wasmactor.RetryBuild{OwnerID: owner, JobID: c.Param("id")})
	if err != nil {
		return internalError()
	}
	result, ok := response.(*wasmactor.BuildRequested)
	if !ok {
		return internalError()
	}
	if result.Err != nil {
		return jsonResponse(retryBuildErrorStatus(result.Err), map[string]string{"error": result.Err.Error()})
	}
	return jsonResponse(http.StatusAccepted, map[string]string{"job_id": result.JobID})
}

// deleteAccountAPI deletes the signed-in Account. If it still has unfinished
// imports or builds, nothing is deleted and the response lists them. Sending
// cancel_active_jobs=true cancels them first.
func (a *Actor) deleteAccountAPI(c *web.Context) any {
	owner, ok := a.authorizedSubject(c, "account.delete", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	cancel := c.Request.URL.Query().Get("cancel_active_jobs") == "true"
	response, err := a.ask(c.Request.Context(), a.dependencies.Projects, &projectactor.DeleteAccount{OwnerID: owner, CancelActiveJobs: cancel})
	if err != nil {
		return internalError()
	}
	result, ok := response.(*projectactor.AccountDeleted)
	if !ok || result.Err != nil {
		return internalError()
	}
	if !result.Deleted && len(result.ActiveJobs) > 0 {
		return jsonResponse(http.StatusConflict, map[string]any{
			"error":       "this account has unfinished imports or WASM builds; wait for them to finish, or retry with cancel_active_jobs=true to cancel them",
			"active_jobs": result.ActiveJobs,
		})
	}
	if !result.Deleted {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "account not found"})
	}
	a.log(c.Request.Context(), "info", "account.deleted", map[string]string{"user_id": owner})
	return web.Response{StatusCode: http.StatusNoContent, Header: http.Header{
		"Set-Cookie": {a.cookie("", -1).String()},
	}}
}

func importErrorStatus(err error) int {
	if err.Error() == "import job not found" {
		return http.StatusNotFound
	}
	return http.StatusConflict
}

func retryBuildErrorStatus(err error) int {
	if err.Error() == "WASM build not found" {
		return http.StatusNotFound
	}
	return wasmBuildErrorStatus(err)
}
