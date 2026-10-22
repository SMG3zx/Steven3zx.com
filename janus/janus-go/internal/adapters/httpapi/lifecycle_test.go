package httpapi

import (
	"context"
	"encoding/json"
	"janus/internal/adapters/buildstest"
	"janus/internal/adapters/catalogtest"
	"janus/internal/domain/authn"
	"janus/internal/domain/authz"
	"net/http"
	"strings"
	"testing"
	"time"

	"janus/cmd/janus/identityfixture"
	"janus/internal/adapters/audittest"
	contractid "janus/internal/contract/identity"
	contractprojects "janus/internal/contract/projects"
)

// deletingAccounts is the identity service as the Account deletion test needs it.
// With unfinished jobs it refuses to delete the Account unless the request cancels
// them; once a deletion has started the User is gone and the deletion reports it
// has finished, as identity does after projects, builds and fleet confirm. Those
// services are not running here, so the flow across them is tested in the identity
// service.
type deletingAccounts struct {
	Identity
	jobs    []contractid.ActiveJob
	started bool
}

func (u *deletingAccounts) DeleteAccount(_ context.Context, _ string, cancelActiveJobs bool) (contractid.AccountDeletion, error) {
	if len(u.jobs) > 0 && !cancelActiveJobs {
		return contractid.AccountDeletion{Status: contractid.AccountRefused, ActiveJobs: u.jobs}, nil
	}
	if u.started {
		return contractid.AccountDeletion{Status: contractid.AccountMissing}, nil
	}
	u.started = true
	return contractid.AccountDeletion{Status: contractid.AccountDeleting}, nil
}

func (u *deletingAccounts) FindUser(ctx context.Context, id string) (contractid.User, error) {
	if u.started {
		return contractid.User{}, nil
	}
	return u.Identity.FindUser(ctx, id)
}

func newLifecycleApp(t *testing.T) (http.Handler, *deletingAccounts) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	identities := &deletingAccounts{Identity: identityfixture.Start(t)}
	catalog := catalogtest.NewMemory()
	audit := audittest.NewRecorder()
	builds := buildstest.NewFake()
	authorization, err := authz.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{
		Audit: audit, Identity: identities, Catalog: catalog, Builds: builds,
		Authentication: authn.New([]byte(strings.Repeat("k", 32)), "janus-test", 30*time.Minute), Authorization: authorization, CookieSecure: true,
	}).Handler(), identities
}

func TestAccountDeletionListsActiveJobsCancelsOnRequestAndEndsSessions(t *testing.T) {
	handler, identities := newLifecycleApp(t)

	register := perform(handler, http.MethodPost, "/auth/register", `{"email":"dana@example.com","password":"correct horse battery staple"}`, nil)
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", register.Code, register.Body.String())
	}
	session := register.Result().Cookies()[0]
	projectResponse := perform(handler, http.MethodPost, "/api/projects", `{"name":"Doomed","description":""}`, session)
	var project contractprojects.Project
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil || project.ID == "" {
		t.Fatalf("project create = %d %s", projectResponse.Code, projectResponse.Body.String())
	}
	identities.jobs = []contractid.ActiveJob{{Kind: "import", ID: "running-import", ProjectID: project.ID, Status: "cloning"}}

	blocked := perform(handler, http.MethodDelete, "/api/account", "", session)
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "running-import") {
		t.Fatalf("blocked deletion = %d %s, want 409 listing running-import", blocked.Code, blocked.Body.String())
	}
	profile := perform(handler, http.MethodGet, "/api/me", "", session)
	if profile.Code != http.StatusOK {
		t.Fatalf("a blocked deletion must leave the account signed in, /api/me = %d", profile.Code)
	}

	deleted := perform(handler, http.MethodDelete, "/api/account?cancel_active_jobs=true", "", session)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("cancelled deletion = %d %s, want 204", deleted.Code, deleted.Body.String())
	}
	cleared := deleted.Result().Cookies()
	if len(cleared) == 0 || cleared[0].MaxAge >= 0 {
		t.Fatalf("deletion did not clear the session cookie: %#v", cleared)
	}
	stale := perform(handler, http.MethodGet, "/api/me", "", session)
	if stale.Code == http.StatusOK {
		t.Fatal("a session for a deleted account still authenticates")
	}
}

func TestRetryRoutesReportMissingAndNotFailedJobs(t *testing.T) {
	handler, _ := newLifecycleApp(t)
	register := perform(handler, http.MethodPost, "/auth/register", `{"email":"eli@example.com","password":"correct horse battery staple"}`, nil)
	session := register.Result().Cookies()[0]

	missing := perform(handler, http.MethodPost, "/api/imports/no-such-job/retry", "", session)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("retry of an unknown import = %d, want 404", missing.Code)
	}
	missingBuild := perform(handler, http.MethodPost, "/api/wasm-builds/no-such-build/retry", "", session)
	if missingBuild.Code != http.StatusNotFound {
		t.Fatalf("retry of an unknown build = %d, want 404", missingBuild.Code)
	}
	anonymous := perform(handler, http.MethodDelete, "/api/account", "", nil)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous account deletion = %d, want 401", anonymous.Code)
	}
}
