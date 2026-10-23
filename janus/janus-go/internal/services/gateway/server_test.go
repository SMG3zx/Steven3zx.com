package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"janus/internal/domain/authn"
	"janus/internal/domain/authz"
	"janus/internal/services/gateway/projectstest"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"janus/cmd/janus/identityfixture"
	contractid "janus/internal/contract/identity"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/services/gateway/audittest"
)

func TestLocalAuthenticationAuthorizationAndLogout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	identities := identityfixture.Start(t)
	catalog := projectstest.NewMemory()
	authorization, err := authz.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Config{
		Audit: audittest.NewRecorder(), Identity: identities, Catalog: catalog,
		Authentication: authn.New([]byte(strings.Repeat("k", 32)), "janus-test", 30*time.Minute), Authorization: authorization, CookieSecure: true,
	}).Handler()

	register := perform(handler, http.MethodPost, "/auth/register", `{"email":"alice@example.com","password":"correct horse battery staple"}`, nil)
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", register.Code, register.Body.String())
	}
	session := register.Result().Cookies()
	if len(session) == 0 || !session[0].HttpOnly || !session[0].Secure || session[0].SameSite != http.SameSiteLaxMode || session[0].MaxAge <= 0 {
		t.Fatalf("registration did not issue a hardened session cookie: %#v", session)
	}

	profile := perform(handler, http.MethodGet, "/api/me", "", session[0])
	if profile.Code != http.StatusOK || !bytes.Contains(profile.Body.Bytes(), []byte(`"email":"alice@example.com"`)) {
		t.Fatalf("profile status = %d, body = %s", profile.Code, profile.Body.String())
	}
	projectResponse := perform(handler, http.MethodPost, "/api/projects", `{"name":"Repo Import","description":"test project"}`, session[0])
	if projectResponse.Code != http.StatusCreated {
		t.Fatalf("project create status = %d, body = %s", projectResponse.Code, projectResponse.Body.String())
	}
	var createdProject contractprojects.Project
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &createdProject); err != nil {
		t.Fatal(err)
	}
	if createdProject.OwnerID == "" || createdProject.Name != "Repo Import" {
		t.Fatalf("project response = %#v", createdProject)
	}
	const entrypointSnapshotID = "entrypoint-snapshot"
	const entrypointCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	catalog.SeedSnapshot(createdProject.OwnerID, createdProject.ID, projectstest.Seed{ID: entrypointSnapshotID, Repository: "alice/repo", Commit: entrypointCommit, TotalBytes: 10_591_486,
		MetadataJSON: `{"scan":{"entrypoint_candidates":[{"path":"cmd/server/main.go","ecosystem":"Go","reason":"fixture"}]}}`,
		Files:        []projectstest.File{{Path: "cmd/server/main.go", Mode: "100644", Content: []byte("package main")}}})
	listedProjects := perform(handler, http.MethodGet, "/api/projects", "", session[0])
	if listedProjects.Code != http.StatusOK || !bytes.Contains(listedProjects.Body.Bytes(), []byte(createdProject.ID)) {
		t.Fatalf("project list status = %d body=%s", listedProjects.Code, listedProjects.Body.String())
	}
	entrypoint := perform(handler, http.MethodPost, "/api/projects/"+createdProject.ID+"/entrypoint", `{"snapshot_id":"`+entrypointSnapshotID+`","entry_point":"cmd/server/main.go"}`, session[0])
	if entrypoint.Code != http.StatusOK || !strings.Contains(entrypoint.Body.String(), "cmd/server/main.go") {
		t.Fatalf("save imported entrypoint status=%d body=%s", entrypoint.Code, entrypoint.Body.String())
	}
	importRequest := perform(handler, http.MethodPost, "/api/projects/"+createdProject.ID+"/imports", `{"repository":"alice/repo","branch":"main","commit":"`+entrypointCommit+`"}`, session[0])
	if importRequest.Code != http.StatusBadRequest || !strings.Contains(importRequest.Body.String(), "connect GitHub before importing") {
		t.Fatalf("import request did not resolve the route project before checking GitHub: status=%d body=%s", importRequest.Code, importRequest.Body.String())
	}
	updatedProject := perform(handler, http.MethodPut, "/api/projects/"+createdProject.ID, `{"name":"Updated repo","description":"updated"}`, session[0])
	if updatedProject.Code != http.StatusOK || !bytes.Contains(updatedProject.Body.Bytes(), []byte("Updated repo")) {
		t.Fatalf("project update status = %d body=%s", updatedProject.Code, updatedProject.Body.String())
	}
	if response := perform(handler, http.MethodGet, "/api/projects/"+createdProject.ID, "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous project read status = %d", response.Code)
	}
	deletedProject := perform(handler, http.MethodDelete, "/api/projects/"+createdProject.ID, "", session[0])
	if deletedProject.Code != http.StatusNoContent {
		t.Fatalf("project delete status=%d body=%s", deletedProject.Code, deletedProject.Body.String())
	}
	if allowed, err := authorization.Allowed(ctx, "different-user", "profile.read", map[string]any{"subject": "alice"}); err != nil || allowed {
		t.Fatalf("OPA permitted a user to read another subject's profile: allowed=%v err=%v", allowed, err)
	}
	unauthorized := perform(handler, http.MethodGet, "/api/me", "", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated profile status = %d", unauthorized.Code)
	}
	otherUser := perform(handler, http.MethodGet, "/api/me", "", &http.Cookie{Name: sessionCookieName, Value: "invalid"})
	if otherUser.Code != http.StatusUnauthorized {
		t.Fatalf("invalid-token profile status = %d", otherUser.Code)
	}
	if disabledGoogle := perform(handler, http.MethodGet, "/auth/google", "", nil); disabledGoogle.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured Google status = %d", disabledGoogle.Code)
	}

	firstIdentity, err := identities.ResolveGoogleIdentity(ctx, contractid.ResolveIdentity{Subject: "google-subject", Email: "alice@example.com", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	secondIdentity, err := identities.ResolveGoogleIdentity(ctx, contractid.ResolveIdentity{Subject: "google-subject", Email: "updated@example.com", DisplayName: "Alice Updated"})
	if err != nil {
		t.Fatal(err)
	}
	if firstIdentity.ID != secondIdentity.ID {
		t.Fatalf("Google subject resolved to different users: %q and %q", firstIdentity.ID, secondIdentity.ID)
	}
	if firstIdentity.ID == "" || firstIdentity.ID == "alice@example.com" {
		t.Fatalf("Google account unexpectedly linked by email: %q", firstIdentity.ID)
	}

	login := perform(handler, http.MethodPost, "/auth/login", `{"email":"alice@example.com","password":"correct horse battery staple"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body.String())
	}
	badLogin := perform(handler, http.MethodPost, "/auth/login", `{"email":"alice@example.com","password":"wrong password"}`, nil)
	if badLogin.Code != http.StatusUnauthorized {
		t.Fatalf("invalid login status = %d", badLogin.Code)
	}

	duplicate := perform(handler, http.MethodPost, "/auth/register", `{"email":"alice@example.com","password":"different correct password"}`, nil)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate registration status = %d, body = %s", duplicate.Code, duplicate.Body.String())
	}

	logout := perform(handler, http.MethodPost, "/auth/logout", "", session[0])
	if logout.Code != http.StatusSeeOther || logout.Header().Get("Location") != "/" {
		t.Fatalf("logout status = %d", logout.Code)
	}
	cleared := logout.Result().Cookies()
	if len(cleared) == 0 || cleared[0].MaxAge >= 0 || cleared[0].Value != "" {
		t.Fatalf("logout did not clear session cookie: %#v", cleared)
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
