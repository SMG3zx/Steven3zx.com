package httpapi

import (
	"context"
	"encoding/json"
	"janus/internal/domain/authn"
	"janus/internal/domain/authz"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"janus/cmd/janus/fleetfixture"
	"janus/cmd/janus/identityfixture"
	"janus/internal/adapters/audittest"
	"janus/internal/adapters/catalogtest"
	contractaudit "janus/internal/contract/audit"
	"janus/internal/services/fleet/tailnettest"
)

// agentApp is the HTTP adapter over the Agents use case with test adapters.
type agentApp struct {
	handler http.Handler
	tailnet *tailnettest.Fake
	audit   *audittest.Recorder
	// fixture runs the fleet service when Agents are configured; nil otherwise.
	fixture *fleetfixture.Fixture
}

func newAgentApp(t *testing.T, withAgents bool) *agentApp {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	audit := audittest.NewRecorder()
	identities := identityfixture.Start(t)
	catalog := catalogtest.NewMemory()
	authorization, err := authz.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		Audit: audit, Identity: identities, Catalog: catalog,
		Authentication: authn.New([]byte(strings.Repeat("k", 32)), "janus-test", 30*time.Minute), Authorization: authorization,
	}
	a := &agentApp{tailnet: tailnettest.NewFake(), audit: audit}
	if withAgents {
		a.fixture = fleetfixture.Start(t, fleetfixture.Options{})
		config.Fleet = a.fixture.Fleet
		a.tailnet = a.fixture.Tailnet
	}
	a.handler = New(config).Handler()
	return a
}

// auditEvents returns the Audit Events the HTTP adapter and the fleet service wrote.
func (a *agentApp) auditEvents() []contractaudit.Event {
	events := a.audit.Events()
	if a.fixture != nil {
		for _, event := range a.fixture.AuditEvents() {
			events = append(events, contractaudit.Event{Level: event.Level, Name: event.Name, Fields: event.Fields})
		}
	}
	return events
}

func (a *agentApp) register(t *testing.T, email string) *http.Cookie {
	t.Helper()
	response := perform(a.handler, http.MethodPost, "/auth/register", `{"email":"`+email+`","password":"correct horse battery staple"}`, nil)
	if response.Code != http.StatusCreated || len(response.Result().Cookies()) == 0 {
		t.Fatalf("register %s = %d: %s", email, response.Code, response.Body.String())
	}
	return response.Result().Cookies()[0]
}

func decode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatalf("response %q is not the expected JSON: %v", response.Body.String(), err)
	}
	return out
}

type issuedBody struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type enrolledBody struct {
	AgentID    string `json:"agent_id"`
	AuthKey    string `json:"auth_key"`
	Tag        string `json:"tag"`
	ControlURL string `json:"control_url"`
}

type agentBody struct {
	Connected bool   `json:"connected"`
	Paused    bool   `json:"paused"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Tag       string `json:"tag"`
	Status    string `json:"status"`
}

// enrollAgent runs the Owner's side and the Agent's side of an enrollment and
// has the Agent join the tailnet, and returns what the Agent received.
func (a *agentApp) enrollAgent(t *testing.T, owner *http.Cookie, name string) enrolledBody {
	t.Helper()
	issued := decode[issuedBody](t, perform(a.handler, http.MethodPost, "/api/agents/enrollment-codes", "", owner))
	response := perform(a.handler, http.MethodPost, "/api/agents/enroll", `{"code":"`+issued.Code+`","name":"`+name+`"}`, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("enroll = %d: %s", response.Code, response.Body.String())
	}
	enrolled := decode[enrolledBody](t, response)
	if _, err := a.tailnet.Join(enrolled.AuthKey, name); err != nil {
		t.Fatal(err)
	}
	return enrolled
}

func TestAnOwnerIssuesACodeAndTheAgentTradesItForAKeyWithoutASession(t *testing.T) {
	a := newAgentApp(t, true)
	alice := a.register(t, "alice@example.com")

	if denied := perform(a.handler, http.MethodPost, "/api/agents/enrollment-codes", "", nil); denied.Code != http.StatusUnauthorized {
		t.Fatalf("issuing a code without a session = %d, want 401", denied.Code)
	}
	issuedResponse := perform(a.handler, http.MethodPost, "/api/agents/enrollment-codes", "", alice)
	if issuedResponse.Code != http.StatusCreated || issuedResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("issuing a code = %d, cache-control %q: %s", issuedResponse.Code, issuedResponse.Header().Get("Cache-Control"), issuedResponse.Body.String())
	}
	issued := decode[issuedBody](t, issuedResponse)
	if issued.Code == "" || !issued.ExpiresAt.After(time.Now()) {
		t.Fatalf("issued = %#v, want a code that expires in the future", issued)
	}

	enrollResponse := perform(a.handler, http.MethodPost, "/api/agents/enroll", `{"code":"`+issued.Code+`","name":"laptop"}`, nil)
	if enrollResponse.Code != http.StatusCreated || enrollResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("enroll = %d, cache-control %q: %s", enrollResponse.Code, enrollResponse.Header().Get("Cache-Control"), enrollResponse.Body.String())
	}
	enrolled := decode[enrolledBody](t, enrollResponse)
	if enrolled.AuthKey == "" || enrolled.AgentID == "" || enrolled.ControlURL != "https://tailnet.example.test" || !strings.HasPrefix(enrolled.Tag, "tag:acct-") {
		t.Fatalf("enrolled = %#v", enrolled)
	}

	if replay := perform(a.handler, http.MethodPost, "/api/agents/enroll", `{"code":"`+issued.Code+`","name":"again"}`, nil); replay.Code != http.StatusBadRequest {
		t.Fatalf("replaying a code = %d, want 400", replay.Code)
	}
	for _, body := range []string{`{"code":"nonsense","name":"laptop"}`, `{"code":"` + issued.Code + `","name":""}`, `{"name":"laptop"}`, `{"code":"x","name":"y","extra":1}`, `not json`} {
		if bad := perform(a.handler, http.MethodPost, "/api/agents/enroll", body, nil); bad.Code != http.StatusBadRequest {
			t.Fatalf("enrolling with %q = %d, want 400", body, bad.Code)
		}
	}
}

func TestAnOwnerSeesOnlyTheirAgentsAndNeverTheirKeys(t *testing.T) {
	a := newAgentApp(t, true)
	alice, bob := a.register(t, "alice@example.com"), a.register(t, "bob@example.com")
	enrolled := a.enrollAgent(t, alice, "laptop")

	listed := perform(a.handler, http.MethodGet, "/api/agents", "", alice)
	if listed.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", listed.Code, listed.Body.String())
	}
	agents := decode[[]agentBody](t, listed)
	if len(agents) != 1 || agents[0].ID != enrolled.AgentID || agents[0].Name != "laptop" || agents[0].Status != "online" || agents[0].Tag != enrolled.Tag {
		t.Fatalf("alice's Agents = %#v, want her laptop, online", agents)
	}
	if strings.Contains(listed.Body.String(), enrolled.AuthKey) {
		t.Fatal("the Agent list carries the Agent's key")
	}
	if bobs := decode[[]agentBody](t, perform(a.handler, http.MethodGet, "/api/agents", "", bob)); len(bobs) != 0 {
		t.Fatalf("bob sees alice's Agents: %#v", bobs)
	}
	if anonymous := perform(a.handler, http.MethodGet, "/api/agents", "", nil); anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("listing without a session = %d, want 401", anonymous.Code)
	}
}

func TestOnlyTheOwnerRemovesAnAgentAndItsNodeGoesWithIt(t *testing.T) {
	a := newAgentApp(t, true)
	alice, bob := a.register(t, "alice@example.com"), a.register(t, "bob@example.com")
	enrolled := a.enrollAgent(t, alice, "laptop")
	path := "/api/agents/" + enrolled.AgentID

	if denied := perform(a.handler, http.MethodDelete, path, "", nil); denied.Code != http.StatusUnauthorized {
		t.Fatalf("removing without a session = %d, want 401", denied.Code)
	}
	if foreign := perform(a.handler, http.MethodDelete, path, "", bob); foreign.Code != http.StatusNotFound {
		t.Fatalf("bob removing alice's Agent = %d, want 404", foreign.Code)
	}
	if nodes, _ := a.tailnet.Nodes(context.Background()); len(nodes) != 1 {
		t.Fatal("bob's attempt removed alice's node")
	}
	if removed := perform(a.handler, http.MethodDelete, path, "", alice); removed.Code != http.StatusNoContent {
		t.Fatalf("alice removing her Agent = %d: %s", removed.Code, removed.Body.String())
	}
	if nodes, _ := a.tailnet.Nodes(context.Background()); len(nodes) != 0 {
		t.Fatalf("the node survived its Agent: %#v", nodes)
	}
	if again := perform(a.handler, http.MethodDelete, path, "", alice); again.Code != http.StatusNotFound {
		t.Fatalf("removing twice = %d, want 404", again.Code)
	}
}

func TestAgentEndpointsAreUnavailableWhenNoTailnetIsConfigured(t *testing.T) {
	a := newAgentApp(t, false)
	alice := a.register(t, "alice@example.com")
	for _, call := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/agents/enrollment-codes", ""},
		{http.MethodPost, "/api/agents/enroll", `{"code":"x","name":"y"}`},
		{http.MethodGet, "/api/agents", ""},
		{http.MethodDelete, "/api/agents/anything", ""},
	} {
		if response := perform(a.handler, call.method, call.path, call.body, alice); response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s = %d, want 503", call.method, call.path, response.Code)
		}
	}
}

func TestAgentAuditEventsNeverCarryACodeOrAKey(t *testing.T) {
	a := newAgentApp(t, true)
	alice := a.register(t, "alice@example.com")
	issued := decode[issuedBody](t, perform(a.handler, http.MethodPost, "/api/agents/enrollment-codes", "", alice))
	enrolled := decode[enrolledBody](t, perform(a.handler, http.MethodPost, "/api/agents/enroll", `{"code":"`+issued.Code+`","name":"laptop"}`, nil))
	perform(a.handler, http.MethodDelete, "/api/agents/"+enrolled.AgentID, "", alice)

	var names []string
	for _, event := range a.auditEvents() {
		names = append(names, event.Name)
		text := event.Name + " " + strings.Join(mapValues(event.Fields), " ")
		if strings.Contains(text, issued.Code) || strings.Contains(text, enrolled.AuthKey) {
			t.Fatalf("audit event %q carries a code or a key: %v", event.Name, event.Fields)
		}
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"agent.code.issued", "agent.enrolled", "agent.removed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("audit events %v lack %s", names, want)
		}
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
