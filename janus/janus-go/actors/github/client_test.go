package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientListsReposBranchesAndRecentCommits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.URL.Path == "/user/repos":
			fmt.Fprint(w, `[{"id":7,"full_name":"alice/private","private":true,"default_branch":"main","size":20}]`)
		case r.URL.Path == "/repos/alice/private/branches":
			fmt.Fprint(w, `[{"name":"main","commit":{"sha":"abc"}}]`)
		case r.URL.Path == "/repos/alice/private/commits" && r.URL.Query().Get("sha") == "main":
			fmt.Fprint(w, `[{"sha":"deadbeef","commit":{"message":"initial commit","author":{"date":"2026-10-17T12:00:00Z"}}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient()
	client.BaseURL = server.URL
	repos, err := client.Repositories(context.Background(), "test-token")
	if err != nil || len(repos) != 1 || repos[0].ID != 7 || !repos[0].Private {
		t.Fatalf("repositories %#v, err %v", repos, err)
	}
	branches, err := client.Branches(context.Background(), "test-token", "alice/private")
	if err != nil || len(branches) != 1 || branches[0].Name != "main" {
		t.Fatalf("branches %#v, err %v", branches, err)
	}
	commits, err := client.Commits(context.Background(), "test-token", "alice/private", "main")
	if err != nil || len(commits) != 1 || commits[0].SHA != "deadbeef" {
		t.Fatalf("commits %#v, err %v", commits, err)
	}
}

func TestClientDoesNotLeakTokenInAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "invalid token")
	}))
	defer server.Close()
	client := NewClient()
	client.BaseURL = server.URL
	_, err := client.Repositories(context.Background(), "sensitive-token-value")
	if err == nil || strings.Contains(err.Error(), "sensitive-token-value") {
		t.Fatalf("unsafe API error: %v", err)
	}
}
