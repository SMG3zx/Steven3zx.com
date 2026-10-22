package httpapi

import (
	"net/http"
)

// registerLifecycleRoutes adds the routes for retrying failed jobs and deleting
// an Account.
func (a *Server) registerLifecycleRoutes(app *mux) {
	app.Post("/api/imports/:id/retry", a.retryImportAPI)
	app.Post("/api/wasm-builds/:id/retry", a.retryWASMBuildAPI)
	app.Delete("/api/account", a.deleteAccountAPI)
}

func (a *Server) retryImportAPI(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.import", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	jobID, err := a.dependencies.Catalog.RetryImport(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		return jsonResponse(importErrorStatus(err), map[string]string{"error": err.Error()})
	}
	return jsonResponse(http.StatusAccepted, map[string]string{"job_id": jobID})
}

func (a *Server) retryWASMBuildAPI(c *call) any {
	owner, ok := a.authorizedSubject(c, "project.build", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	retried, err := a.dependencies.Builds.Retry(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		return jsonResponse(retryBuildErrorStatus(err), map[string]string{"error": err.Error()})
	}
	jobID := retried.ID
	return jsonResponse(http.StatusAccepted, map[string]string{"job_id": jobID})
}

// deleteAccountAPI deletes the signed-in Account. If it still has unfinished
// imports or builds, nothing is deleted and the response lists them. Sending
// cancel_active_jobs=true cancels them first.
func (a *Server) deleteAccountAPI(c *call) any {
	owner, ok := a.authorizedSubject(c, "account.delete", nil)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	cancel := c.Request.URL.Query().Get("cancel_active_jobs") == "true"
	result, err := a.dependencies.Accounts.Delete(c.Request.Context(), owner, cancel)
	if err != nil {
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
	return reply{StatusCode: http.StatusNoContent, Header: http.Header{
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
