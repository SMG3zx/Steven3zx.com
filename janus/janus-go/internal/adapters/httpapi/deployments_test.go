package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"janus/internal/adapters/audittest"
	"janus/internal/adapters/buildertest"
	"janus/internal/adapters/checktest"
	"janus/internal/adapters/githubapi"
	"janus/internal/adapters/gitsource"
	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

// agentDeployer answers every Deploy with err, so a test can make the Agent
// refuse a Deployment or accept it.
type agentDeployer struct{ err error }

func (d agentDeployer) Ready(context.Context, string, string) error { return nil }

func (d agentDeployer) Deploy(context.Context, string, string, string, string) error { return d.err }

// appCheckFailed is the Check of a WASM build whose tests failed.
func appCheckFailed() app.CheckResult {
	return app.CheckResult{Passed: false, Log: []byte("test failed\n")}
}

type deployApp struct {
	handler     http.Handler
	store       app.Store
	checks      *checktest.Fake
	audit       *audittest.Recorder
	deployments *app.Deployments
	builds      *app.Builds
	session     *http.Cookie
	intruder    *http.Cookie
	project     app.Project
}

// newDeployApp serves the API with one owner who has a project whose WASM build
// is complete, and an enrolled Agent of that owner.
func newDeployApp(t *testing.T, deployer agentDeployer) *deployApp {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	store := storetest.NewMemory()
	gh := app.NewGitHub(store, githubapi.NewClient())
	audit := audittest.NewRecorder()
	imports := app.NewImports(store, gh.API, gitsource.New(), audit)
	checks := checktest.NewFake()
	builds := app.NewBuilds(store, buildertest.NewFake(), checks, audit)
	deployments := app.NewDeployments(store, deployer, audit)
	authorization, err := app.NewPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Config{
		Audit: audit, Identity: app.NewIdentity(store), Projects: app.NewProjects(store), GitHub: gh, Imports: imports, Builds: builds,
		Accounts: app.NewAccounts(store, imports, builds), Deployments: deployments,
		Authentication: app.NewAuthentication([]byte(strings.Repeat("k", 32)), "janus-test", 30*time.Minute), Authorization: authorization, CookieSecure: true,
	}).Handler()

	register := perform(handler, http.MethodPost, "/auth/register", `{"email":"owen@example.com","password":"correct horse battery staple"}`, nil)
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", register.Code, register.Body.String())
	}
	intruder := perform(handler, http.MethodPost, "/auth/register", `{"email":"ivy@example.com","password":"correct horse battery staple"}`, nil)
	if intruder.Code != http.StatusCreated {
		t.Fatalf("intruder register status = %d", intruder.Code)
	}
	env := &deployApp{handler: handler, store: store, checks: checks, audit: audit, deployments: deployments, builds: builds,
		session: register.Result().Cookies()[0], intruder: intruder.Result().Cookies()[0]}
	projectResponse := perform(handler, http.MethodPost, "/api/projects", `{"name":"Shipped","description":""}`, env.session)
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &env.project); err != nil || env.project.ID == "" {
		t.Fatalf("project create = %d %s", projectResponse.Code, projectResponse.Body.String())
	}
	completeRustSnapshot(t, store, env.project)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.CreateAgent(ctx, app2Agent(env.project.OwnerID)))
	return env
}

// app2Agent is the enrolled Agent of owner.
func app2Agent(owner string) app.CreateAgent {
	return app.CreateAgent{ID: "laptop", OwnerID: owner, Name: "laptop", Tag: "tag:acct-" + owner, KeyID: "key-" + owner}
}

// completeRustSnapshot gives project an active, complete Rust Snapshot.
func completeRustSnapshot(t *testing.T, store app.Store, project app.Project) {
	t.Helper()
	ctx := context.Background()
	const commit = "cccccccccccccccccccccccccccccccccccccccc"
	job, snapshot := project.ID+"-import", project.ID+"-snapshot"
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.CreateImportJob(ctx, app.CreateImportJob{ID: job, OwnerID: project.OwnerID, ProjectID: project.ID, Repository: "owen/shipped", Branch: "main", Commit: commit}))
	must(store.BeginSnapshot(ctx, app.BeginSnapshot{ID: snapshot, JobID: job, OwnerID: project.OwnerID, ProjectID: project.ID, Repository: "owen/shipped", Branch: "main", Commit: commit, MetadataJSON: "{}", Language: "Rust"}))
	must(store.AddSnapshotFiles(ctx, snapshot, []app.SnapshotFile{{Path: "Cargo.toml", Content: []byte("[package]\nname='shipped'\n"), Mode: "100644"}}))
	storetest.AdvanceImport(t, store, job, "queued", "cloning", "scanning", "storing")
	must(store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: snapshot, JobID: job, FileCount: 1, TotalBytes: 20}))
}

// buildRust compiles the project's active Snapshot and waits for the Build and its checks.
func (a *deployApp) buildRust(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id, err := a.builds.Request(ctx, app.NewBuild{OwnerID: a.project.OwnerID, ProjectID: a.project.ID, Target: "wasm32-unknown-unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.builds.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *deployApp) waitDeployments(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := a.deployments.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDeployQueuesAndReadsTheDeploymentThroughTheAPI(t *testing.T) {
	env := newDeployApp(t, agentDeployer{})
	build := env.buildRust(t)
	path := "/api/projects/" + env.project.ID + "/deployments"

	response := perform(env.handler, http.MethodPost, path, `{"build_id":"`+build+`","agent_id":"laptop"}`, env.session)
	if response.Code != http.StatusAccepted {
		t.Fatalf("deploy status = %d, body = %s", response.Code, response.Body.String())
	}
	var queued struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil || queued.ID == "" || queued.State != "queued" {
		t.Fatalf("queued = %+v, %v", queued, err)
	}
	env.waitDeployments(t)

	read := perform(env.handler, http.MethodGet, "/api/deployments/"+queued.ID, "", env.session)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"state":"healthy"`) || !strings.Contains(read.Body.String(), `"status":"passed"`) {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	if other := perform(env.handler, http.MethodGet, "/api/deployments/"+queued.ID, "", env.intruder); other.Code != http.StatusNotFound {
		t.Fatalf("another Account read the Deployment: %d %s", other.Code, other.Body.String())
	}
}

func TestDeployRefusesFailingChecksAndKeepsTheAgentsError(t *testing.T) {
	env := newDeployApp(t, agentDeployer{err: errors.New("Agent refused the module")})
	env.checks.Answer(appCheckFailed())
	build := env.buildRust(t)
	path := "/api/projects/" + env.project.ID + "/deployments"

	refused := perform(env.handler, http.MethodPost, path, `{"build_id":"`+build+`","agent_id":"laptop"}`, env.session)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "deploy anyway") {
		t.Fatalf("failing checks = %d %s, want a 409 that offers deploy anyway", refused.Code, refused.Body.String())
	}

	response := perform(env.handler, http.MethodPost, path, `{"build_id":"`+build+`","agent_id":"laptop","deploy_anyway":true}`, env.session)
	if response.Code != http.StatusAccepted {
		t.Fatalf("deploy anyway = %d %s", response.Code, response.Body.String())
	}
	var queued struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	env.waitDeployments(t)
	read := perform(env.handler, http.MethodGet, "/api/deployments/"+queued.ID, "", env.session)
	if !strings.Contains(read.Body.String(), `"state":"failed"`) || !strings.Contains(read.Body.String(), "Agent refused the module") || !strings.Contains(read.Body.String(), `"deploy_anyway":true`) {
		t.Fatalf("failed deployment = %s", read.Body.String())
	}
}

func TestDeployIsRefusedForAnotherAccountsBuild(t *testing.T) {
	env := newDeployApp(t, agentDeployer{})
	build := env.buildRust(t)

	response := perform(env.handler, http.MethodPost, "/api/projects/"+env.project.ID+"/deployments", `{"build_id":"`+build+`","agent_id":"laptop"}`, env.intruder)
	if response.Code != http.StatusNotFound {
		t.Fatalf("another Account deployed: %d %s", response.Code, response.Body.String())
	}
}
