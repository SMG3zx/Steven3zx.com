package routes

import (
	"context"
	"encoding/json"
	"janus/internal/adapters/sqlstore"
	"janus/internal/app"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"
	"go.uber.org/zap"

	"janus/actors/accounting"
	authactor "janus/actors/authentication"
	authzactor "janus/actors/authorization"
	"janus/actors/github"
	"janus/actors/gitsource"
	projectactor "janus/actors/projects"
	wasmactor "janus/actors/wasm"
)

func newLifecycleApp(t *testing.T) (http.Handler, app.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	system, err := actor.NewActorSystem("routes-lifecycle-test", actor.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = system.Stop(stopCtx)
	})
	accountingPID, err := system.Spawn(ctx, "accounting-lifecycle", accounting.New(zap.NewNop()))
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlstore.Open(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	gh := github.NewService(store, github.NewClient())
	projectsPID, err := system.Spawn(ctx, "projects-lifecycle", projectactor.New(store, gh, gitsource.New(), accounting.NewAudit(accountingPID)))
	if err != nil {
		t.Fatal(err)
	}
	wasmPID, err := system.Spawn(ctx, "wasm-lifecycle", wasmactor.New(store, accounting.NewAudit(accountingPID), nil))
	if err != nil {
		t.Fatal(err)
	}
	authentication := authactor.New([]byte(strings.Repeat("k", 32)), "janus-test", 30*time.Minute)
	authorization, err := authzactor.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	routesPID, err := system.Spawn(ctx, "routes-lifecycle", New(Dependencies{
		System: system, Audit: accounting.NewAudit(accountingPID), Store: store, GitHub: gh, Projects: projectsPID, WASMBuilds: wasmPID,
		Authentication: authentication, Authorization: authorization, CookieSecure: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	built, err := actor.Ask(ctx, routesPID, &Build{}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return built.(*Built).App, store
}

func TestAccountDeletionListsActiveJobsCancelsOnRequestAndEndsSessions(t *testing.T) {
	handler, store := newLifecycleApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	register := perform(handler, http.MethodPost, "/auth/register", `{"email":"dana@example.com","password":"correct horse battery staple"}`, nil)
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", register.Code, register.Body.String())
	}
	session := register.Result().Cookies()[0]
	projectResponse := perform(handler, http.MethodPost, "/api/projects", `{"name":"Doomed","description":""}`, session)
	var project app.Project
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil || project.ID == "" {
		t.Fatalf("project create = %d %s", projectResponse.Code, projectResponse.Body.String())
	}
	if err := store.CreateImportJob(ctx, app.CreateImportJob{ID: "running-import", OwnerID: project.OwnerID, ProjectID: project.ID, Repository: "dana/repo", Branch: "main", Commit: strings.Repeat("d", 40)}); err != nil {
		t.Fatal(err)
	}

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
