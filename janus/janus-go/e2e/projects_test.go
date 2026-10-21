package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"janus/internal/adapters/audittest"
	"janus/internal/adapters/githubapi"
	"janus/internal/adapters/gitsource"
	"janus/internal/adapters/httpapi"
	"janus/internal/adapters/identitynats"
	"janus/internal/adapters/sqlstore"
	"janus/internal/app"
	"janus/internal/platform/natstest"
	"janus/internal/services/identity"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

const (
	jwtSecret      = "playwright-test-secret-that-is-long-enough"
	importCommit   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	secondCommit   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	entrypointPath = "cmd/server/main.go"
)

// e2eWASMBuilder builds any Rust workspace into a fixed Artifact.
type e2eWASMBuilder struct{}

func (e2eWASMBuilder) Build(_ context.Context, _ string, request app.BuildRequest) (app.BuildResult, error) {
	if request.Target != "wasm32-unknown-unknown" {
		return app.BuildResult{}, fmt.Errorf("unexpected target %q", request.Target)
	}
	if _, err := os.Stat(path.Join(request.Workspace, "Cargo.toml")); err != nil {
		return app.BuildResult{}, err
	}
	return app.BuildResult{Name: "wasm-artifacts.zip", Artifact: []byte("playwright-wasm-archive"), Log: []byte("Finished release build")}, nil
}

func TestProjectBrowserActions(t *testing.T) {
	pw, err := playwright.Run()
	if err != nil {
		if testingOnlyE2E() {
			t.Fatalf("start Playwright: %v", err)
		}
		t.Skipf("Playwright driver is not installed; run `go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 install chromium`: %v", err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		if testingOnlyE2E() {
			t.Fatalf("launch Chromium: %v", err)
		}
		t.Skipf("Playwright Chromium is not installed; run `go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 install chromium`: %v", err)
	}
	defer browser.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	store, identities, handler := startJanus(t, ctx)
	server := httptest.NewServer(handler)
	defer server.Close()
	userID, sessionCookie := registerUser(t, server.URL)
	connectGitHub(t, ctx, identities, userID)

	browserContext, err := browser.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	defer browserContext.Close()
	if err := browserContext.AddCookies([]playwright.OptionalCookie{{
		Name: "janus_session", Value: sessionCookie, URL: playwright.String(server.URL),
		HttpOnly: playwright.Bool(true), Secure: playwright.Bool(false),
	}}); err != nil {
		t.Fatal(err)
	}
	page, err := browserContext.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	page.SetDefaultTimeout(5000)

	var importAttempts atomic.Int32
	if err := page.Route("**/api/github/repos", func(route playwright.Route) {
		fulfillJSON(t, route, http.StatusOK, map[string]any{"repositories": []map[string]any{{
			"id": 42, "full_name": "SMG3zx/demo", "private": false, "default_branch": "main", "language": "Rust", "stargazers_count": 3, "forks_count": 1,
		}}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := page.Route("**/api/github/branches**", func(route playwright.Route) {
		fulfillJSON(t, route, http.StatusOK, []map[string]any{{"name": "main", "commit": map[string]string{"sha": importCommit}}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := page.Route("**/api/github/commits**", func(route playwright.Route) {
		fulfillJSON(t, route, http.StatusOK, []map[string]any{{"sha": importCommit, "message": "Add service entrypoint"}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := page.Route("**/api/projects/*/imports", func(route playwright.Route) {
		var request struct {
			Repository string `json:"repository"`
			Branch     string `json:"branch"`
			Commit     string `json:"commit"`
		}
		body, readErr := route.Request().PostData()
		if readErr != nil {
			t.Errorf("read import request: %v", readErr)
			_ = route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(http.StatusBadRequest), Body: "{}"})
			return
		}
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Errorf("decode import request %q: %v", body, err)
			_ = route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(http.StatusBadRequest), Body: "{}"})
			return
		}
		if request.Repository != "SMG3zx/demo" || request.Branch != "main" || request.Commit != importCommit {
			t.Errorf("import request = %#v; want selected repository, branch, and commit", request)
			_ = route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(http.StatusBadRequest), Body: `{"error":"unexpected import selection"}`, ContentType: playwright.String("application/json")})
			return
		}
		requestURL, err := url.Parse(route.Request().URL())
		if err != nil {
			t.Errorf("parse import request URL %q: %v", route.Request().URL(), err)
			_ = route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(http.StatusBadRequest), Body: `{}`})
			return
		}
		projectID := path.Base(path.Dir(requestURL.Path))
		attempt := importAttempts.Add(1)
		jobID := fmt.Sprintf("playwright-import-%d", attempt)
		snapshotID := fmt.Sprintf("playwright-snapshot-%d", attempt)
		if err := seedSnapshot(ctx, store, userID, projectID, jobID, snapshotID, importCommit, `{"scan":{"entrypoint_candidates":[{"path":"cmd/server/main.go","ecosystem":"Go","reason":"Go service entrypoint"}]}}`); err != nil {
			t.Errorf("seed imported snapshot for owner=%q project=%q URL=%s: %v", userID, projectID, requestURL.Path, err)
			_ = route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(http.StatusInternalServerError), Body: `{}`})
			return
		}
		fulfillJSON(t, route, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "queued"})
	}); err != nil {
		t.Fatal(err)
	}

	// Blank-project creation is a browser form action backed by the real routes and SQLite actor.
	if _, err := page.Goto(server.URL + "/projects"); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleLink, playwright.PageGetByRoleOptions{Name: "Create blank project"}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`input[name="name"]`).Fill("Browser project"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`textarea[name="description"]`).Fill("Created by Playwright-Go"); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Create", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleHeading, playwright.PageGetByRoleOptions{Name: "Browser project", Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		t.Fatalf("created project detail did not load: %v", err)
	}
	projectURL := page.URL()
	projectID := path.Base(projectURL)

	// Project listing exposes the created project and the connected GitHub import action.
	if _, err := page.Goto(server.URL + "/projects"); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleLink, playwright.PageGetByRoleOptions{Name: "Browser project", Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		t.Fatalf("project was not listed: %v", err)
	}
	if err := page.GetByRole(*playwright.AriaRoleLink, playwright.PageGetByRoleOptions{Name: "Create from GitHub", Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		t.Fatalf("connected GitHub import action is missing: %v", err)
	}

	// Repository, branch, commit selection, import submission, and job polling run in the real UI.
	if _, err := page.Goto(projectURL); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`#commit option[value="` + importCommit + `"]`).WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateAttached}); err != nil {
		t.Fatalf("repository metadata was not loaded: %v", err)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Import selected commit"}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByText("Imported repository", playwright.PageGetByTextOptions{Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		t.Fatalf("completed import was not rendered: %v", err)
	}
	bodyText, err := page.Locator("body").TextContent()
	if err != nil || !strings.Contains(bodyText, "1.23 MB") {
		t.Fatalf("imported snapshot statistics were not displayed in MB (body=%q, err=%v)", bodyText, err)
	}

	// The user can confirm a detected entrypoint and see it persisted after reload.
	if _, err := page.Locator("#entrypoint").SelectOption(playwright.SelectOptionValues{Values: &[]string{entrypointPath}}); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Save entrypoint"}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByText("Entrypoint: cmd/server/main.go", playwright.PageGetByTextOptions{Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		t.Fatalf("entrypoint was not saved: %v", err)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Build WASM", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatalf("could not start WASM build: %v", err)
	}
	downloadLink := page.GetByRole(*playwright.AriaRoleLink, playwright.PageGetByRoleOptions{Name: "Download wasm-artifacts.zip", Exact: playwright.Bool(true)})
	if err := downloadLink.WaitFor(); err != nil {
		t.Fatalf("successful WASM artifact was not made available: %v", err)
	}
	wasmLink, err := downloadLink.GetAttribute("href")
	if err != nil {
		t.Fatal(err)
	}
	artifactRequest, err := http.NewRequest(http.MethodGet, server.URL+wasmLink, nil)
	if err != nil {
		t.Fatal(err)
	}
	artifactRequest.AddCookie(&http.Cookie{Name: "janus_session", Value: sessionCookie})
	artifactResponse, err := http.DefaultClient.Do(artifactRequest)
	if err != nil {
		t.Fatal(err)
	}
	artifactBody, readErr := io.ReadAll(artifactResponse.Body)
	_ = artifactResponse.Body.Close()
	if readErr != nil || artifactResponse.StatusCode != http.StatusOK || string(artifactBody) != "playwright-wasm-archive" {
		t.Fatalf("WASM artifact response status=%d body=%q err=%v", artifactResponse.StatusCode, artifactBody, readErr)
	}

	// Snapshot activation switches the project's active repository revision.
	if err := seedSnapshot(ctx, store, userID, projectID, "older-snapshot-job", "older-snapshot", secondCommit, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := page.Reload(); err != nil {
		t.Fatal(err)
	}
	activate := page.Locator(`button[onclick="activateSnapshot('playwright-snapshot-1')"]`)
	if err := activate.WaitFor(); err != nil {
		t.Fatalf("previous successful snapshot is not available to activate: %v", err)
	}
	if err := activate.Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByText("@ aaaaaaa", playwright.PageGetByTextOptions{Exact: playwright.Bool(false)}).WaitFor(); err != nil {
		t.Fatalf("activating the older snapshot did not restore its commit: %v", err)
	}

	// The dedicated GitHub creation page creates a new project and waits for the same import job to complete.
	if _, err := page.Goto(server.URL + "/projects"); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleLink, playwright.PageGetByRoleOptions{Name: "Create from GitHub", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`#commit option[value="` + importCommit + `"]`).WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateAttached}); err != nil {
		t.Fatalf("GitHub project wizard did not load repository metadata: %v", err)
	}
	if err := page.Locator("#name").Fill("Imported browser project"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("#description").Fill("Created from a selected GitHub revision"); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Create project and import", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleHeading, playwright.PageGetByRoleOptions{Name: "Imported browser project", Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		t.Fatalf("GitHub-created project did not open after import: %v", err)
	}
	importedPageText, err := page.Locator("body").TextContent()
	if err != nil || !strings.Contains(importedPageText, "SMG3zx/demo · main @ aaaaaaa") {
		t.Fatalf("GitHub-created project does not show its imported revision (body=%q, err=%v)", importedPageText, err)
	}

	// GitHub disconnect is a real POST action and removes import access from the projects page.
	if _, err := page.Goto(server.URL + "/projects"); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Disconnect GitHub", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByText("Connect GitHub", playwright.PageGetByTextOptions{Exact: playwright.Bool(false)}).WaitFor(); err != nil {
		t.Fatalf("GitHub disconnect did not update the projects page: %v", err)
	}
	if got, err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Disconnect GitHub", Exact: playwright.Bool(true)}).Count(); err != nil || got != 0 {
		t.Fatalf("disconnect control remains visible (count=%d, err=%v)", got, err)
	}

	// Project ownership is enforced at the UI edge: an anonymous browser is redirected home.
	anonymous, err := browser.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	defer anonymous.Close()
	if _, err := anonymous.Goto(server.URL + "/projects"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(anonymous.URL(), "/") {
		t.Fatalf("anonymous /projects navigation ended at %q; want home", anonymous.URL())
	}
	unauthorizedBuild, err := http.Post(server.URL+"/api/projects/"+projectID+"/wasm-builds", "application/json", strings.NewReader(`{"target":"wasm32-unknown-unknown"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorizedBuild.Body.Close()
	if unauthorizedBuild.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous WASM build request status=%d; want 401", unauthorizedBuild.StatusCode)
	}
}

func startJanus(t *testing.T, ctx context.Context) (app.Store, app.Identities, http.Handler) {
	t.Helper()
	store, identities, _, handler := startJanusWith(t, ctx, e2eWASMBuilder{})
	return store, identities, handler
}

// startJanusWith starts the application in-process with b as its WASM builder,
// and the identity service on an embedded NATS server. It returns the Store,
// the Identities, the Builds use case and the HTTP handler.
func startJanusWith(t *testing.T, ctx context.Context, b app.Builder) (app.Store, app.Identities, *app.Builds, http.Handler) {
	t.Helper()
	store, err := sqlstore.Open(path.Join(t.TempDir(), "e2e.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hub := natstest.Start(t)
	identityService := identity.New(identity.Config{DatabasePath: path.Join(t.TempDir(), "identity.db"), TokenSecret: jwtSecret})
	if err := identityService.Start(ctx, hub.Conn()); err != nil {
		t.Fatalf("start the identity service: %v", err)
	}
	t.Cleanup(func() { _ = identityService.Stop(context.Background()) })
	identities := identitynats.New(hub.Conn())
	authentication := app.NewAuthentication([]byte(jwtSecret), "janus-e2e", 30*time.Minute)
	authorization, err := app.NewPolicy(ctx)
	if err != nil {
		t.Fatalf("prepare authorization: %v", err)
	}
	gh := app.NewGitHub(identities, githubapi.NewClient())
	audit := audittest.NewRecorder()
	imports := app.NewImports(store, identities, gh.API, gitsource.New(), audit)
	builds := app.NewBuilds(store, b, nil, audit)
	server := httpapi.New(httpapi.Config{
		Audit: audit, Identity: app.NewIdentity(store, identities), Projects: app.NewProjects(store), GitHub: gh, Imports: imports, Builds: builds, Accounts: app.NewAccounts(store, identities, imports, builds),
		Authentication: authentication, Authorization: authorization,
		JWTSecret: jwtSecret, GitHubEnabled: true, GitHubClientID: "test-client", GitHubSecret: "test-secret",
		GitHubCallback: "http://localhost/auth/github/callback", CookieSecure: false,
	})
	return store, identities, builds, server.Handler()
}

func registerUser(t *testing.T, baseURL string) (string, string) {
	t.Helper()
	response, err := http.Post(baseURL+"/auth/register", "application/json", strings.NewReader(`{"email":"playwright@example.com","password":"correct horse battery staple"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register status=%d", response.StatusCode)
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&user); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "janus_session" {
			return user.ID, cookie.Value
		}
	}
	t.Fatal("registration did not return the Janus session cookie")
	return "", ""
}

func connectGitHub(t *testing.T, ctx context.Context, identities app.Identities, userID string) {
	t.Helper()
	if err := identities.ConnectGitHub(ctx, app.ConnectGitHub{UserID: userID, Subject: "github-user-1", Email: "playwright@example.com", DisplayName: "Playwright", Token: "fake-oauth-token"}); err != nil {
		t.Fatalf("connect GitHub fixture: %v", err)
	}
}

func seedSnapshot(ctx context.Context, store app.Store, ownerID, projectID, jobID, snapshotID, commit, metadata string) error {
	return seedLanguageSnapshot(ctx, store, ownerID, projectID, jobID, snapshotID, commit, metadata, "Rust", "Cargo.toml", "[package]\nname='fixture'\nversion='0.1.0'\nedition='2021'\n")
}

// seedLanguageSnapshot stores a completed Snapshot of language whose manifest
// file is manifest with the given content.
func seedLanguageSnapshot(ctx context.Context, store app.Store, ownerID, projectID, jobID, snapshotID, commit, metadata, language, manifest, manifestContent string) error {
	if err := store.CreateImportJob(ctx, app.CreateImportJob{ID: jobID, OwnerID: ownerID, ProjectID: projectID, Repository: "SMG3zx/demo", Branch: "main", Commit: commit}); err != nil {
		return fmt.Errorf("create import job: %w", err)
	}
	if err := store.BeginSnapshot(ctx, app.BeginSnapshot{ID: snapshotID, JobID: jobID, OwnerID: ownerID, ProjectID: projectID, RepositoryID: 42, Repository: "SMG3zx/demo", Branch: "main", Commit: commit, DefaultBranch: "main", Language: language, Stars: 3, Forks: 1, MetadataJSON: metadata}); err != nil {
		return fmt.Errorf("begin snapshot: %w", err)
	}
	if err := store.AddSnapshotFiles(ctx, snapshotID, []app.SnapshotFile{
		{Path: entrypointPath, Mode: "100644", Hash: strings.Repeat("c", 40), Content: []byte("package main\n")},
		{Path: manifest, Mode: "100644", Hash: strings.Repeat("d", 40), Content: []byte(manifestContent)},
	}); err != nil {
		return fmt.Errorf("add snapshot files: %w", err)
	}
	from := "queued"
	for _, step := range []struct {
		status   string
		progress int
	}{{"cloning", 20}, {"scanning", 50}, {"storing", 80}} {
		if err := store.TransitionImportJob(ctx, app.JobTransition{ID: jobID, From: from, To: step.status, Progress: step.progress}); err != nil {
			return fmt.Errorf("move import job to %s: %w", step.status, err)
		}
		from = step.status
	}
	if err := store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: snapshotID, JobID: jobID, FileCount: 2, TotalBytes: 1_234_567}); err != nil {
		return fmt.Errorf("complete snapshot: %w", err)
	}
	return nil
}

func fulfillJSON(t *testing.T, route playwright.Route, status int, body any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Errorf("encode mocked browser response: %v", err)
		_ = route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(http.StatusInternalServerError), Body: `{}`})
		return
	}
	if err := route.Fulfill(playwright.RouteFulfillOptions{Status: playwright.Int(status), ContentType: playwright.String("application/json"), Body: string(encoded)}); err != nil {
		t.Errorf("fulfill mocked browser response: %v", err)
	}
}

func testingOnlyE2E() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("JANUS_PLAYWRIGHT_REQUIRED")), "1")
}
