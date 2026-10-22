package gateway

import (
	"context"
	"fmt"
	"net/http"
	"time"

	contractid "janus/internal/contract/identity"
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
	result, err := a.deleteAccount(c.Request.Context(), owner, cancel)
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

// accountPoll is how often deleteAccount asks identity whether a deletion has
// finished.
const accountPoll = 50 * time.Millisecond

// accountDeleted is the outcome of deleting an Account. Deleted is false when
// there was no such Account or when the deletion was refused, in which case
// ActiveJobs lists the unfinished Imports and Builds.
type accountDeleted struct {
	Deleted    bool
	ActiveJobs []contractid.ActiveJob
}

// deleteAccount deletes the Account through identity, which announces the
// deletion, has each service clear what it keeps and deletes the User once all
// have confirmed. It is refused while the Account has unfinished jobs, unless
// cancelActiveJobs is set. It returns once the User is gone. A deletion that
// fails part way is left for identity to resume, and deleting again finishes it.
func (a *Server) deleteAccount(ctx context.Context, owner string, cancelActiveJobs bool) (accountDeleted, error) {
	deletion, err := a.dependencies.Identity.DeleteAccount(ctx, owner, cancelActiveJobs)
	if err != nil {
		return accountDeleted{}, fmt.Errorf("start deleting the Account: %w", err)
	}
	switch deletion.Status {
	case contractid.AccountMissing:
		return accountDeleted{}, nil
	case contractid.AccountRefused:
		return accountDeleted{ActiveJobs: deletion.ActiveJobs}, nil
	}
	for {
		deletion, err = a.dependencies.Identity.DeleteAccount(ctx, owner, cancelActiveJobs)
		if err != nil {
			return accountDeleted{}, fmt.Errorf("wait for the Account's deletion: %w", err)
		}
		if deletion.Status == contractid.AccountMissing {
			return accountDeleted{Deleted: true}, nil
		}
		select {
		case <-ctx.Done():
			return accountDeleted{}, fmt.Errorf("the Account is still being deleted: %w", ctx.Err())
		case <-time.After(accountPoll):
		}
	}
}
