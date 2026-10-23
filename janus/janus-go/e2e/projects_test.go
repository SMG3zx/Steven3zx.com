package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	contractid "janus/internal/contract/identity"
	"janus/internal/domain/authn"
	"janus/internal/domain/authz"
	"janus/internal/platform/natstest"
	"janus/internal/services/gateway"
	"janus/internal/services/gateway/audittest"
	"janus/internal/services/gateway/buildstest"
	"janus/internal/services/gateway/identityclient"
	"janus/internal/services/gateway/projectstest"
	"janus/internal/services/identity"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

const (
	jwtSecret      = "playwright-test-secret-that-is-long-enough"
	secondCommit   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	entrypointPath = "cmd/server/main.go"
	repository     = "SMG3zx/demo"
)

// TestProjectBrowserActions drives the project flows through the web frontend in
// Chromium. The browser reaches the gateway, which sends pages to the Qwik app,
// which asks the gateway for its data. The repository is published to the
// in-memory catalog, so the imports run for real without a GitHub call.
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

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	webAddress := freeAddress(t)
	catalog, identities, _, handler := startJanusWithWeb(t, ctx, "http://"+webAddress)
	server := httptest.NewServer(handler)
	defer server.Close()
	startFrontend(t, webAddress, server.URL, server.URL)

	userID, sessionCookie := registerUser(t, server.URL)
	connectGitHub(t, ctx, identities, userID)
	catalog.Connect(userID)
	importCommit := catalog.Publish(repository, map[string]string{
		entrypointPath: "package main\n\nfunc main() {}\n",
		"go.mod":       "module demo\n\ngo 1.22\n",
	})

	// Anonymous visitors are sent to sign in, and keep the page they asked for.
	anonymous, err := browser.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	defer anonymous.Close()
	if _, err := anonymous.Goto(server.URL + "/projects"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(anonymous.URL(), "/sign-in") {
		t.Fatalf("anonymous /projects navigation ended at %q; want the sign-in page", anonymous.URL())
	}

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
	page.SetDefaultTimeout(10000)

	// A blank project is created from the new-project form and opens its page.
	if _, err := page.Goto(server.URL + "/projects/new"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`input[name="name"]`).Fill("Browser project"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`input[name="description"]`).Fill("Created by Playwright-Go"); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Create project", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleHeading, playwright.PageGetByRoleOptions{Name: "Browser project", Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		body, _ := page.Locator("body").TextContent()
		t.Fatalf("created project page did not load: %v (page at %s says %q)", err, page.URL(), body)
	}
	projectURL := page.URL()
	projectID := path.Base(projectURL)

	// The projects list shows the project.
	if _, err := page.Goto(server.URL + "/projects"); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleLink, playwright.PageGetByRoleOptions{Name: "Browser project", Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		t.Fatalf("project was not listed: %v", err)
	}

	// The project page imports a chosen commit: repository, branch and commit are picked, then imported.
	if _, err := page.Goto(projectURL + "?repository=" + repository + "&branch=main"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`#commit option[value="` + importCommit + `"]`).WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateAttached}); err != nil {
		body, _ := page.Locator("body").TextContent()
		t.Fatalf("repository choices were not loaded: %v (page says %q)", err, body)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Import selected commit"}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleLink, playwright.PageGetByRoleOptions{Name: "Open project", Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		t.Fatalf("import did not finish: %v", err)
	}
	if _, err := page.Goto(projectURL); err != nil {
		t.Fatal(err)
	}

	// The user confirms the detected entrypoint, and it is shown after the save.
	if _, err := page.Locator("#entry_point").SelectOption(playwright.SelectOptionValues{Values: &[]string{entrypointPath}}); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Save entrypoint"}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByText("entrypoint "+entrypointPath, playwright.PageGetByTextOptions{Exact: playwright.Bool(false)}).WaitFor(); err != nil {
		t.Fatalf("entrypoint was not saved: %v", err)
	}

	// A WASM build runs, and its artifact downloads through the gateway.
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Build WASM", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatalf("could not start WASM build: %v", err)
	}
	downloadLink := page.Locator(`a[href*="/artifact"]`)
	if err := downloadLink.WaitFor(playwright.LocatorWaitForOptions{Timeout: playwright.Float(20000)}); err != nil {
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

	// Activating an earlier revision makes its commit the current one.
	if err := seedSnapshot(catalog, userID, projectID, "older-snapshot-job", "older-snapshot", secondCommit, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := page.Reload(); err != nil {
		t.Fatal(err)
	}
	activate := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Activate", Exact: playwright.Bool(true)}).First()
	if err := activate.WaitFor(); err != nil {
		t.Fatalf("previous revision is not available to activate: %v", err)
	}
	if err := activate.Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(fmt.Sprintf(`li:has-text(%q) >> .badge-ok`, "@ "+importCommit[:7])).WaitFor(); err != nil {
		t.Fatalf("activating the earlier revision did not make its commit current: %v", err)
	}

	// The new-project page creates a project from a chosen commit and imports it.
	if _, err := page.Goto(server.URL + "/projects/new?repository=" + repository + "&branch=main"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`#commit option[value="` + importCommit + `"]`).WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateAttached}); err != nil {
		t.Fatalf("GitHub project form did not load repository choices: %v", err)
	}
	if err := page.Locator(`input[name="name"]`).Fill("Imported browser project"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`input[name="description"]`).Fill("Created from a selected GitHub revision"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`input[name="intent"][value="import"]`).Check(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Create project", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleLink, playwright.PageGetByRoleOptions{Name: "Open project", Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		t.Fatalf("GitHub-created project did not import: %v", err)
	}
	if err := page.GetByRole(*playwright.AriaRoleLink, playwright.PageGetByRoleOptions{Name: "Open project", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleHeading, playwright.PageGetByRoleOptions{Name: "Imported browser project", Exact: playwright.Bool(true)}).WaitFor(); err != nil {
		t.Fatalf("GitHub-created project did not open after import: %v", err)
	}
	importedPageText, err := page.Locator("body").TextContent()
	if err != nil || !strings.Contains(importedPageText, repository) {
		t.Fatalf("GitHub-created project does not show its repository (body=%q, err=%v)", importedPageText, err)
	}

	// Disconnecting GitHub in the account settings removes import access.
	if _, err := page.Goto(server.URL + "/account"); err != nil {
		t.Fatal(err)
	}
	if err := page.GetByRole(*playwright.AriaRoleButton, playwright.PageGetByRoleOptions{Name: "Disconnect GitHub", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatal(err)
	}
	// The Connection lives in the identity service, which the projects service reads
	// for its import access. The in-memory catalog does not mirror it, so the test
	// asks identity directly.
	if !eventually(5*time.Second, func() bool {
		token, err := identities.FindGitHubToken(ctx, userID)
		return err == nil && !token.Connected
	}) {
		t.Fatal("GitHub disconnect did not remove the GitHub Connection")
	}

	// Project ownership is enforced by the gateway: an anonymous build request is refused.
	unauthorizedBuild, err := http.Post(server.URL+"/api/projects/"+projectID+"/wasm-builds", "application/json", strings.NewReader(`{"target":"wasm32-unknown-unknown"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorizedBuild.Body.Close()
	if unauthorizedBuild.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous WASM build request status=%d; want 401", unauthorizedBuild.StatusCode)
	}
}

func startJanus(t *testing.T, ctx context.Context) (*projectstest.Memory, *identityclient.Gateway, http.Handler) {
	t.Helper()
	catalog, identities, _, handler := startJanusWith(t, ctx)
	return catalog, identities, handler
}

// startJanusWith starts the application in-process with b as its WASM builder,
// and the identity service on an embedded NATS server. Projects live in an
// in-memory Catalog, so a test can seed the Snapshot an Import would make;
// the real projects service is tested through its adapter. It returns the
// Catalog, the Identities, the Builds use case and the HTTP handler.
func startJanusWith(t *testing.T, ctx context.Context) (*projectstest.Memory, *identityclient.Gateway, *buildstest.Fake, http.Handler) {
	t.Helper()
	return startJanusWithWeb(t, ctx, "")
}

// startJanusWithWeb is startJanusWith, with the gateway sending its pages to the
// web frontend at webUpstream. An empty webUpstream keeps the gateway's own pages.
func startJanusWithWeb(t *testing.T, ctx context.Context, webUpstream string) (*projectstest.Memory, *identityclient.Gateway, *buildstest.Fake, http.Handler) {
	t.Helper()
	hub := natstest.Start(t)
	identityService := identity.New(identity.Config{DatabasePath: path.Join(t.TempDir(), "identity.db"), TokenSecret: jwtSecret})
	if err := identityService.Start(ctx, hub.Conn()); err != nil {
		t.Fatalf("start the identity service: %v", err)
	}
	t.Cleanup(func() { _ = identityService.Stop(context.Background()) })
	identities := identityclient.NewGateway(hub.Conn())
	authentication := authn.New([]byte(jwtSecret), "janus-e2e", 30*time.Minute)
	authorization, err := authz.New(ctx)
	if err != nil {
		t.Fatalf("prepare authorization: %v", err)
	}
	catalog := projectstest.NewMemory()
	audit := audittest.NewRecorder()
	builds := buildstest.NewFake()
	builds.Produce([]byte("playwright-wasm-archive"))
	server := gateway.New(gateway.Config{
		Audit: audit, Identity: identities, Catalog: catalog, Builds: builds,
		Authentication: authentication, Authorization: authorization,
		JWTSecret: jwtSecret, GitHubEnabled: true, GitHubClientID: "test-client", GitHubSecret: "test-secret",
		GitHubCallback: "http://localhost/auth/github/callback", CookieSecure: false,
		WebUpstream: webUpstream,
	})
	return catalog, identities, builds, server.Handler()
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

func connectGitHub(t *testing.T, ctx context.Context, identities *identityclient.Gateway, userID string) {
	t.Helper()
	if err := identities.ConnectGitHub(ctx, contractid.ConnectGitHub{UserID: userID, Subject: "github-user-1", Email: "playwright@example.com", DisplayName: "Playwright", Token: "fake-oauth-token"}); err != nil {
		t.Fatalf("connect GitHub fixture: %v", err)
	}
}

func seedSnapshot(catalog *projectstest.Memory, ownerID, projectID, jobID, snapshotID, commit, metadata string) error {
	return seedLanguageSnapshot(catalog, ownerID, projectID, jobID, snapshotID, commit, metadata, "Rust", "Cargo.toml", "[package]\nname='fixture'\nversion='0.1.0'\nedition='2021'\n")
}

// seedLanguageSnapshot records a finished Import and the Snapshot it made, of
// language, whose manifest file is manifest with the given content.
func seedLanguageSnapshot(catalog *projectstest.Memory, ownerID, projectID, jobID, snapshotID, commit, metadata, language, manifest, manifestContent string) error {
	catalog.SeedImport(ownerID, projectID, jobID, "complete")
	catalog.SeedSnapshot(ownerID, projectID, projectstest.Seed{ID: snapshotID, Repository: repository, Commit: commit, Language: language, MetadataJSON: metadata, TotalBytes: 1_234_567,
		Files: []projectstest.File{
			{Path: entrypointPath, Mode: "100644", Hash: strings.Repeat("c", 40), Content: []byte("package main\n")},
			{Path: manifest, Mode: "100644", Hash: strings.Repeat("d", 40), Content: []byte(manifestContent)},
		}})
	return nil
}

// eventually reports whether check passes within timeout, asking every 50 milliseconds.
func eventually(timeout time.Duration, check func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if check() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func testingOnlyE2E() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("JANUS_PLAYWRIGHT_REQUIRED")), "1")
}
