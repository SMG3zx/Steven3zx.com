package headscale

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testAPIKey = "api-key-that-must-never-leak"

// recorded is one request the stub Headscale server saw.
type recorded struct {
	method, path, auth string
	body               map[string]any
}

// stub answers like Headscale's REST API for the calls the adapter makes, and
// records them. Live tests against a real Headscale prove the replies are real.
type stub struct {
	mu       sync.Mutex
	requests []recorded
	users    []string
	status   map[string]int // forced status by "METHOD path"
}

func (s *stub) seen() []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recorded(nil), s.requests...)
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	entry := recorded{method: r.Method, path: r.URL.RequestURI(), auth: r.Header.Get("Authorization")}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &entry.body)
	}
	s.mu.Lock()
	s.requests = append(s.requests, entry)
	forced := s.status[r.Method+" "+r.URL.Path]
	s.mu.Unlock()
	if forced != 0 {
		w.WriteHeader(forced)
		_, _ = io.WriteString(w, `{"code":5,"message":"node not found"}`)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user":
		if len(s.users) == 0 {
			_, _ = io.WriteString(w, `{"users":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"users":[{"id":"7","name":"janus"}]}`)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/user":
		s.users = append(s.users, "janus")
		_, _ = io.WriteString(w, `{"user":{"id":"7","name":"janus"}}`)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/policy":
		_, _ = io.WriteString(w, `{"policy":"{}"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/preauthkey":
		_, _ = io.WriteString(w, `{"preAuthKey":{"id":"42","key":"hskey-auth-secret","expiration":"2026-10-20T13:00:00Z"}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/node":
		_, _ = io.WriteString(w, `{"nodes":[
			{"id":"3","name":"laptop","ipAddresses":["100.64.0.3","fd7a:115c:a1e0::3"],"tags":["tag:acct-alice"],"online":true,"lastSeen":"2026-10-20T12:30:00.123456789Z","preAuthKey":{"id":"42"}},
			{"id":"4","name":"ctrl","ipAddresses":["100.64.0.4"],"tags":["tag:ctrl"],"online":false,"lastSeen":null}]}`)
	default:
		_, _ = io.WriteString(w, `{}`)
	}
}

func newStubbed(t *testing.T) (*Client, *stub) {
	t.Helper()
	s := &stub{status: map[string]int{}}
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	return New(Config{URL: server.URL + "/", APIKey: testAPIKey}), s
}

func TestPrepareCreatesTheUserAndInstallsADefaultDenyPolicy(t *testing.T) {
	client, s := newStubbed(t)
	if err := client.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	var created, policy *recorded
	for _, r := range s.seen() {
		r := r
		if r.auth != "Bearer "+testAPIKey {
			t.Fatalf("%s %s carried auth %q, want the API key as a bearer token", r.method, r.path, r.auth)
		}
		switch {
		case r.method == http.MethodPost && r.path == "/api/v1/user":
			created = &r
		case r.method == http.MethodPut && r.path == "/api/v1/policy":
			policy = &r
		}
	}
	if created == nil || created.body["name"] != "janus" {
		t.Fatalf("user creation = %#v, want a user named janus", created)
	}
	if policy == nil {
		t.Fatal("no policy was installed")
	}
	var document struct {
		TagOwners map[string][]string `json:"tagOwners"`
		ACLs      []struct {
			Action string   `json:"action"`
			Src    []string `json:"src"`
			Dst    []string `json:"dst"`
		} `json:"acls"`
	}
	if err := json.Unmarshal([]byte(policy.body["policy"].(string)), &document); err != nil {
		t.Fatalf("the policy is not JSON: %v", err)
	}
	if len(document.ACLs) != 2 || document.ACLs[0].Action != "accept" ||
		strings.Join(document.ACLs[0].Src, ",") != "tag:ctrl" || strings.Join(document.ACLs[0].Dst, ",") != "autogroup:tagged:*" ||
		strings.Join(document.ACLs[1].Src, ",") != "autogroup:tagged" || strings.Join(document.ACLs[1].Dst, ",") != "tag:ctrl:7102" {
		t.Fatalf("policy rules = %#v, want tag:ctrl to every tagged node and every tagged node to the control plane's leaf port", document.ACLs)
	}
	if len(document.TagOwners["tag:ctrl"]) != 1 {
		t.Fatalf("tag owners = %#v, want tag:ctrl owned by the Janus user", document.TagOwners)
	}
}

func TestACreatedKeyIsSingleUseAndCarriesOneTag(t *testing.T) {
	client, s := newStubbed(t)
	expires := time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC)
	key, err := client.CreatePreAuthKey(context.Background(), "tag:acct-alice", expires)
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != "42" || key.Key != "hskey-auth-secret" || key.Tag != "tag:acct-alice" || !key.Expires.Equal(expires) {
		t.Fatalf("key = %#v", key)
	}
	var body map[string]any
	for _, r := range s.seen() {
		if r.method == http.MethodPost && r.path == "/api/v1/preauthkey" {
			body = r.body
		}
	}
	tags, _ := body["acl_tags"].([]any)
	if body["reusable"] != false || body["ephemeral"] != false || len(tags) != 1 || tags[0] != "tag:acct-alice" ||
		body["expiration"] != "2026-10-20T13:00:00Z" || body["user"] != "7" {
		t.Fatalf("key request = %#v, want single-use, not ephemeral, one tag, the expiry and the Janus user", body)
	}
}

func TestNodesCarryTheKeyTheyJoinedWith(t *testing.T) {
	client, _ := newStubbed(t)
	nodes, err := client.Nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %#v", nodes)
	}
	laptop, control := nodes[0], nodes[1]
	if laptop.ID != "3" || laptop.KeyID != "42" || laptop.Name != "laptop" || !laptop.Online || laptop.Tags[0] != "tag:acct-alice" ||
		len(laptop.Addresses) != 2 || !laptop.LastSeen.Equal(time.Date(2026, 10, 20, 12, 30, 0, 123456789, time.UTC)) {
		t.Fatalf("laptop = %#v", laptop)
	}
	if control.KeyID != "" || control.Online || !control.LastSeen.IsZero() {
		t.Fatalf("control = %#v, want no key, offline, no last seen", control)
	}
}

func TestRemovingSomethingThatIsAlreadyGoneIsNotAnError(t *testing.T) {
	client, s := newStubbed(t)
	s.status["DELETE /api/v1/node/9"] = http.StatusNotFound
	s.status["POST /api/v1/preauthkey/expire"] = http.StatusNotFound
	if err := client.DeleteNode(context.Background(), "9"); err != nil {
		t.Fatalf("deleting a missing node = %v, want no error", err)
	}
	if err := client.ExpirePreAuthKey(context.Background(), "9"); err != nil {
		t.Fatalf("expiring a missing key = %v, want no error", err)
	}
}

func TestAnIDThatCannotBeHeadscalesIsAlreadyGoneAndCostsNoCall(t *testing.T) {
	client, s := newStubbed(t)
	for _, id := range []string{"", "no-such-node", "12x", "-3"} {
		if err := client.DeleteNode(context.Background(), id); err != nil {
			t.Fatalf("deleting node %q = %v, want no error", id, err)
		}
		if err := client.ExpirePreAuthKey(context.Background(), id); err != nil {
			t.Fatalf("expiring key %q = %v, want no error", id, err)
		}
	}
	if calls := s.seen(); len(calls) != 0 {
		t.Fatalf("made %d calls for IDs Headscale cannot have: %#v", len(calls), calls)
	}
}

func TestAFailureNeverCarriesTheAPIKey(t *testing.T) {
	client, s := newStubbed(t)
	s.status["GET /api/v1/node"] = http.StatusInternalServerError
	_, err := client.Nodes(context.Background())
	if err == nil {
		t.Fatal("a server error was ignored")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("error %q carries the API key", err)
	}

	gone := New(Config{URL: "http://127.0.0.1:1", APIKey: testAPIKey, HTTPClient: &http.Client{Timeout: time.Second}})
	_, err = gone.Nodes(context.Background())
	if err == nil || strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("an unreachable server = %v, want an error without the key", err)
	}
}
