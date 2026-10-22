package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"janus/internal/adapters/identityowners"
	"janus/internal/domain/agent"
	"janus/internal/domain/authn"
	"janus/internal/domain/authz"
	"net/http"
	"strings"
	"testing"
	"time"

	"janus/cmd/janus/fleetfixture"
	"janus/internal/adapters/audittest"
	"janus/internal/adapters/buildsnats"
	"janus/internal/adapters/catalogtest"
	"janus/internal/adapters/identitytest"
	"janus/internal/adapters/localstatetest"
	"janus/internal/adapters/runnertest"
	"janus/internal/adapters/storetest"
	"janus/internal/app"
	contractbuilds "janus/internal/contract/builds"
)

// brokenModule is a WebAssembly module the test Agent has no program for, so its
// probe fails and the Deployment is refused.
var brokenModule = []byte("\x00asm\x01\x00\x00\x00broken")

// deployApp serves the API with one owner who has a project, an enrolled Agent
// that is connected, and the fleet service, whose builds the test adds.
type deployApp struct {
	handler  http.Handler
	fixture  *fleetfixture.Fixture
	session  *http.Cookie
	intruder *http.Cookie
	project  app.Project
	agent    string
	builds   int
}

// newDeployApp starts the API with the fleet service in process. Owen is the
// owner of the project and of the Agent; Ivy is another Account.
func newDeployApp(t *testing.T) *deployApp {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	fixture := fleetfixture.Start(t, fleetfixture.Options{})
	store := storetest.NewMemory()
	identities := identitytest.NewMemory()
	catalog := catalogtest.NewMemory()
	audit := audittest.NewRecorder()
	service, err := buildsnats.New(ctx, fixture.Conn())
	if err != nil {
		t.Fatal(err)
	}
	builds := app.NewBuilds(catalog, service)
	authorization, err := authz.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Config{
		Audit: audit, Identity: identityowners.New(store, identitytest.NewDirectory(identities)), Catalog: catalogtest.NewGateway(catalog), Builds: builds,
		Accounts: app.NewAccounts(store, identities, catalog), Fleet: fixture.Fleet,
		Authentication: authn.New([]byte(strings.Repeat("k", 32)), "janus-test", 30*time.Minute), Authorization: authorization, CookieSecure: true,
	}).Handler()

	register := perform(handler, http.MethodPost, "/auth/register", `{"email":"owen@example.com","password":"correct horse battery staple"}`, nil)
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", register.Code, register.Body.String())
	}
	intruder := perform(handler, http.MethodPost, "/auth/register", `{"email":"ivy@example.com","password":"correct horse battery staple"}`, nil)
	if intruder.Code != http.StatusCreated {
		t.Fatalf("intruder register status = %d", intruder.Code)
	}
	env := &deployApp{handler: handler, fixture: fixture, session: register.Result().Cookies()[0], intruder: intruder.Result().Cookies()[0]}
	projectResponse := perform(handler, http.MethodPost, "/api/projects", `{"name":"Shipped","description":""}`, env.session)
	if err := json.Unmarshal(projectResponse.Body.Bytes(), &env.project); err != nil || env.project.ID == "" {
		t.Fatalf("project create = %d %s", projectResponse.Code, projectResponse.Body.String())
	}
	enrollment := fixture.Enroll(env.project.OwnerID, "laptop")
	fixture.Attach(env.project.OwnerID, enrollment, "laptop", pageAgent())
	env.agent = enrollment.AgentID
	return env
}

// pageAgent is an Agent that serves pageModule, the module of every passing build.
func pageAgent() *agent.Runtime {
	runner := runnertest.NewFake()
	runner.Handle(pageModule, func(app.RunInput) (app.RunResult, error) {
		return app.RunResult{Stdout: []byte("Content-Type: text/plain\n\nok")}, nil
	})
	return agent.NewRuntime(runner, localstatetest.NewMemory(), "", time.Now)
}

// addBuild records a complete WASI build of the project, whose checks have the
// given status, and returns its ID.
func (a *deployApp) addBuild(checkStatus string, module []byte) string {
	a.builds++
	id := fmt.Sprintf("build-%d", a.builds)
	build := contractbuilds.Build{ID: id, ProjectID: a.project.ID, SnapshotID: "snapshot", Target: "wasm32-wasip1", Status: contractbuilds.StatusComplete}
	a.fixture.Builds.Add(a.project.OwnerID, build, &contractbuilds.Check{BuildID: id, Status: checkStatus}, module)
	return id
}

// waitState waits until the Deployment, read through the API, is in state.
func (a *deployApp) waitState(t *testing.T, id, state string) {
	t.Helper()
	a.fixture.Eventually("deployment "+id+" to be "+state, func() bool {
		response := perform(a.handler, http.MethodGet, "/api/deployments/"+id, "", a.session)
		return response.Code == http.StatusOK && strings.Contains(response.Body.String(), `"state":"`+state+`"`)
	})
}

func (a *deployApp) deployPath() string {
	return "/api/projects/" + a.project.ID + "/deployments"
}

func TestDeployQueuesAndReadsTheDeploymentThroughTheAPI(t *testing.T) {
	env := newDeployApp(t)
	build := env.addBuild("passed", pageModule)

	response := perform(env.handler, http.MethodPost, env.deployPath(), `{"build_id":"`+build+`","agent_id":"`+env.agent+`"}`, env.session)
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
	env.waitState(t, queued.ID, "healthy")

	read := perform(env.handler, http.MethodGet, "/api/deployments/"+queued.ID, "", env.session)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"status":"passed"`) {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	if other := perform(env.handler, http.MethodGet, "/api/deployments/"+queued.ID, "", env.intruder); other.Code != http.StatusNotFound {
		t.Fatalf("another Account read the Deployment: %d %s", other.Code, other.Body.String())
	}
}

func TestDeployRefusesFailingChecksAndKeepsTheAgentsError(t *testing.T) {
	env := newDeployApp(t)
	build := env.addBuild("failed", brokenModule)

	refused := perform(env.handler, http.MethodPost, env.deployPath(), `{"build_id":"`+build+`","agent_id":"`+env.agent+`"}`, env.session)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "deploy anyway") {
		t.Fatalf("failing checks = %d %s, want a 409 that offers deploy anyway", refused.Code, refused.Body.String())
	}

	response := perform(env.handler, http.MethodPost, env.deployPath(), `{"build_id":"`+build+`","agent_id":"`+env.agent+`","deploy_anyway":true}`, env.session)
	if response.Code != http.StatusAccepted {
		t.Fatalf("deploy anyway = %d %s", response.Code, response.Body.String())
	}
	var queued struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	env.waitState(t, queued.ID, "failed")
	read := perform(env.handler, http.MethodGet, "/api/deployments/"+queued.ID, "", env.session)
	if !strings.Contains(read.Body.String(), "probe failed") || !strings.Contains(read.Body.String(), `"deploy_anyway":true`) {
		t.Fatalf("failed deployment = %s", read.Body.String())
	}
}

func TestDeployIsRefusedForAnotherAccountsBuild(t *testing.T) {
	env := newDeployApp(t)
	build := env.addBuild("passed", pageModule)

	response := perform(env.handler, http.MethodPost, env.deployPath(), `{"build_id":"`+build+`","agent_id":"`+env.agent+`"}`, env.intruder)
	if response.Code != http.StatusNotFound {
		t.Fatalf("another Account deployed: %d %s", response.Code, response.Body.String())
	}
}
