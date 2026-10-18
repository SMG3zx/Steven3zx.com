package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	"janus/actors/persistence"
	projectactor "janus/actors/projects"
)

func TestLocalAuthenticationAuthorizationAndLogout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	system, err := actor.NewActorSystem("routes-test", actor.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err := system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = system.Stop(stopCtx)
	})

	accountingPID, err := system.Spawn(ctx, "accounting-test", accounting.New(zap.NewNop()))
	if err != nil {
		t.Fatal(err)
	}
	persistencePID, err := system.Spawn(ctx, "persistence-test", persistence.New(filepath.Join(t.TempDir(), "accounts.db")))
	if err != nil {
		t.Fatal(err)
	}
	projectsPID, err := system.Spawn(ctx, "projects-test", projectactor.New(persistencePID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_, _ = actor.Ask(closeCtx, persistencePID, &persistence.CloseDatabase{}, time.Second)
	})
	authenticationPID, err := system.Spawn(ctx, "authentication-test", authactor.New([]byte(strings.Repeat("k", 32)), "janus-test", 30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	authorizationPID, err := system.Spawn(ctx, "authorization-test", authzactor.New())
	if err != nil {
		t.Fatal(err)
	}
	routesPID, err := system.Spawn(ctx, "routes-test-actor", New(Dependencies{
		System: system, Accounting: accountingPID, Persistence: persistencePID, Projects: projectsPID,
		Authentication: authenticationPID, Authorization: authorizationPID, CookieSecure: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	built, err := actor.Ask(ctx, routesPID, &Build{}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	app := built.(*Built).App

	register := perform(app, http.MethodPost, "/auth/register", `{"email":"alice@example.com","password":"correct horse battery staple"}`, nil)
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", register.Code, register.Body.String())
	}
	session := register.Result().Cookies()
	if len(session) == 0 || !session[0].HttpOnly || !session[0].Secure || session[0].SameSite != http.SameSiteLaxMode || session[0].MaxAge <= 0 {
		t.Fatalf("registration did not issue a hardened session cookie: %#v", session)
	}

	profile := perform(app, http.MethodGet, "/api/me", "", session[0])
	if profile.Code != http.StatusOK || !bytes.Contains(profile.Body.Bytes(), []byte(`"email":"alice@example.com"`)) {
		t.Fatalf("profile status = %d, body = %s", profile.Code, profile.Body.String())
	}
	projectResponse := perform(app, http.MethodPost, "/api/projects", `{"name":"Repo Import","description":"test project"}`, session[0])
	if projectResponse.Code != http.StatusCreated {
		t.Fatalf("project create status = %d, body = %s", projectResponse.Code, projectResponse.Body.String())
	}
	var createdProject persistence.Project
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &createdProject); err != nil {
		t.Fatal(err)
	}
	if createdProject.OwnerID == "" || createdProject.Name != "Repo Import" {
		t.Fatalf("project response = %#v", createdProject)
	}
	const entrypointSnapshotID = "entrypoint-snapshot"
	const entrypointCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, message := range []any{
		&persistence.CreateImportJob{ID: "entrypoint-job", OwnerID: createdProject.OwnerID, ProjectID: createdProject.ID, Repository: "alice/repo", Branch: "main", Commit: entrypointCommit},
		&persistence.BeginSnapshot{ID: entrypointSnapshotID, JobID: "entrypoint-job", OwnerID: createdProject.OwnerID, ProjectID: createdProject.ID, RepositoryID: 1, Repository: "alice/repo", Branch: "main", Commit: entrypointCommit, DefaultBranch: "main", MetadataJSON: `{"scan":{"entrypoint_candidates":[{"path":"cmd/server/main.go","ecosystem":"Go","reason":"fixture"}]}}`},
		&persistence.UpdateImportJob{ID: "entrypoint-job", Status: "cloning", Progress: 15},
		&persistence.UpdateImportJob{ID: "entrypoint-job", Status: "scanning", Progress: 45},
		&persistence.UpdateImportJob{ID: "entrypoint-job", Status: "storing", Progress: 70},
		&persistence.CompleteSnapshot{SnapshotID: entrypointSnapshotID, JobID: "entrypoint-job", FileCount: 1, TotalBytes: 10_591_486},
	} {
		if _, err := actor.Ask(ctx, persistencePID, message, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	listedProjects := perform(app, http.MethodGet, "/api/projects", "", session[0])
	if listedProjects.Code != http.StatusOK || !bytes.Contains(listedProjects.Body.Bytes(), []byte(createdProject.ID)) {
		t.Fatalf("project list status = %d body=%s", listedProjects.Code, listedProjects.Body.String())
	}
	projectsPage := perform(app, http.MethodGet, "/projects", "", session[0])
	if projectsPage.Code != http.StatusOK || !strings.Contains(projectsPage.Header().Get("Content-Type"), "text/html") || !strings.Contains(projectsPage.Body.String(), "Your projects") {
		t.Fatalf("projects page status = %d body=%s", projectsPage.Code, projectsPage.Body.String())
	}
	projectDetail := perform(app, http.MethodGet, "/projects/"+createdProject.ID, "", session[0])
	if projectDetail.Code != http.StatusOK || !strings.Contains(projectDetail.Body.String(), "10.59 MB") || strings.Contains(projectDetail.Body.String(), "10591486 bytes") {
		t.Fatalf("project detail should render repository size in MB: status=%d body=%s", projectDetail.Code, projectDetail.Body.String())
	}
	entrypoint := perform(app, http.MethodPost, "/api/projects/"+createdProject.ID+"/entrypoint", `{"snapshot_id":"`+entrypointSnapshotID+`","entry_point":"cmd/server/main.go"}`, session[0])
	if entrypoint.Code != http.StatusOK || !strings.Contains(entrypoint.Body.String(), "cmd/server/main.go") {
		t.Fatalf("save imported entrypoint status=%d body=%s", entrypoint.Code, entrypoint.Body.String())
	}
	importRequest := perform(app, http.MethodPost, "/api/projects/"+createdProject.ID+"/imports", `{"repository":"alice/repo","branch":"main","commit":"`+entrypointCommit+`"}`, session[0])
	if importRequest.Code != http.StatusBadRequest || !strings.Contains(importRequest.Body.String(), "connect GitHub before importing") {
		t.Fatalf("import request did not resolve the route project before checking GitHub: status=%d body=%s", importRequest.Code, importRequest.Body.String())
	}
	updatedProject := perform(app, http.MethodPut, "/api/projects/"+createdProject.ID, `{"name":"Updated repo","description":"updated"}`, session[0])
	if updatedProject.Code != http.StatusOK || !bytes.Contains(updatedProject.Body.Bytes(), []byte("Updated repo")) {
		t.Fatalf("project update status = %d body=%s", updatedProject.Code, updatedProject.Body.String())
	}
	if response := perform(app, http.MethodGet, "/api/projects/"+createdProject.ID, "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous project read status = %d", response.Code)
	}
	deletedProject := perform(app, http.MethodDelete, "/api/projects/"+createdProject.ID, "", session[0])
	if deletedProject.Code != http.StatusNoContent {
		t.Fatalf("project delete status=%d body=%s", deletedProject.Code, deletedProject.Body.String())
	}
	authenticatedHome := perform(app, http.MethodGet, "/", "", session[0])
	if authenticatedHome.Code != http.StatusOK || !strings.Contains(authenticatedHome.Body.String(), "Signed in as") || !strings.Contains(authenticatedHome.Body.String(), "alice@example.com") || !strings.Contains(authenticatedHome.Body.String(), `action="/auth/logout"`) {
		t.Fatalf("authenticated homepage status = %d, body = %s", authenticatedHome.Code, authenticatedHome.Body.String())
	}
	deniedDecision, err := actor.Ask(ctx, authorizationPID, &authzactor.Check{Subject: "different-user", Action: "profile.read", Resource: map[string]any{"subject": "alice"}}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if deniedDecision.(*authzactor.Decision).Allowed {
		t.Fatal("OPA permitted a user to read another subject's profile")
	}
	unauthorized := perform(app, http.MethodGet, "/api/me", "", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated profile status = %d", unauthorized.Code)
	}
	otherUser := perform(app, http.MethodGet, "/api/me", "", &http.Cookie{Name: sessionCookieName, Value: "invalid"})
	if otherUser.Code != http.StatusUnauthorized {
		t.Fatalf("invalid-token profile status = %d", otherUser.Code)
	}
	if disabledGoogle := perform(app, http.MethodGet, "/auth/google", "", nil); disabledGoogle.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured Google status = %d", disabledGoogle.Code)
	}
	home := perform(app, http.MethodGet, "/", "", nil)
	if home.Code != http.StatusOK || !strings.Contains(home.Header().Get("Content-Type"), "text/html") || strings.Contains(home.Body.String(), `href="/auth/google"`) {
		t.Fatalf("unconfigured homepage Google sign-in = status %d, headers %#v, body %s", home.Code, home.Header(), home.Body.String())
	}

	firstIdentityResponse, err := actor.Ask(ctx, persistencePID, &persistence.ResolveGoogleIdentity{Subject: "google-subject", Email: "alice@example.com", DisplayName: "Alice"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	firstIdentity := firstIdentityResponse.(*persistence.GoogleIdentityResolved)
	secondIdentityResponse, err := actor.Ask(ctx, persistencePID, &persistence.ResolveGoogleIdentity{Subject: "google-subject", Email: "updated@example.com", DisplayName: "Alice Updated"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	secondIdentity := secondIdentityResponse.(*persistence.GoogleIdentityResolved)
	if firstIdentity.ID != secondIdentity.ID {
		t.Fatalf("Google subject resolved to different users: %q and %q", firstIdentity.ID, secondIdentity.ID)
	}
	if firstIdentity.ID == "" || firstIdentity.ID == "alice@example.com" {
		t.Fatalf("Google account unexpectedly linked by email: %q", firstIdentity.ID)
	}

	login := perform(app, http.MethodPost, "/auth/login", `{"email":"alice@example.com","password":"correct horse battery staple"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body.String())
	}
	badLogin := perform(app, http.MethodPost, "/auth/login", `{"email":"alice@example.com","password":"wrong password"}`, nil)
	if badLogin.Code != http.StatusUnauthorized {
		t.Fatalf("invalid login status = %d", badLogin.Code)
	}

	duplicate := perform(app, http.MethodPost, "/auth/register", `{"email":"alice@example.com","password":"different correct password"}`, nil)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate registration status = %d, body = %s", duplicate.Code, duplicate.Body.String())
	}

	logout := perform(app, http.MethodPost, "/auth/logout", "", session[0])
	if logout.Code != http.StatusSeeOther || logout.Header().Get("Location") != "/" {
		t.Fatalf("logout status = %d", logout.Code)
	}
	cleared := logout.Result().Cookies()
	if len(cleared) == 0 || cleared[0].MaxAge >= 0 || cleared[0].Value != "" {
		t.Fatalf("logout did not clear session cookie: %#v", cleared)
	}
}

func TestHomePageShowsGoogleSignInWhenConfigured(t *testing.T) {
	page := homePage(true, "")
	if page.StatusCode != http.StatusOK || page.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("homepage response = %#v", page)
	}
	body, ok := page.Body.(string)
	if !ok || !strings.Contains(body, `href="/auth/google"`) || !strings.Contains(body, "Continue with Google") || strings.Contains(body, "Sign out") {
		t.Fatalf("configured homepage is missing Google sign-in link: %v", page.Body)
	}
}

func TestHomePageShowsEscapedAuthenticatedIdentityAndSignOut(t *testing.T) {
	page := homePage(false, `<script>alert("xss")</script>`)
	body, ok := page.Body.(string)
	if !ok || !strings.Contains(body, "Signed in as") || !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, `action="/auth/logout"`) || strings.Contains(body, "Continue with Google") {
		t.Fatalf("authenticated homepage did not safely show identity and sign-out: %v", page.Body)
	}
}

func perform(app http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)
	return recorder
}
