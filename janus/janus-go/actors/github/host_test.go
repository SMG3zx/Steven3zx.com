package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"janus/internal/adapters/hosttest"
	"janus/internal/app"
)

func TestClientPassesTheGitHostContract(t *testing.T) {
	hosttest.RunHost(t, func(t *testing.T, world hosttest.World) app.GitHost {
		server := httptest.NewServer(gitHubAPI(world))
		t.Cleanup(server.Close)
		client := NewClient()
		client.BaseURL = server.URL
		return client
	})
}

// gitHubAPI serves world the way api.github.com serves the endpoints Client reads.
func gitHubAPI(world hosttest.World) http.Handler {
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+world.Token {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/user/repos" {
			list := make([]app.Repository, 0, len(world.Repositories))
			for _, repo := range world.Repositories {
				list = append(list, repo.Repository)
			}
			if page, _ := strconv.Atoi(r.URL.Query().Get("page")); page > 1 {
				list = nil
			}
			writeJSON(w, list)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 3 || parts[0] != "repos" {
			http.NotFound(w, r)
			return
		}
		var found *hosttest.Repo
		for i := range world.Repositories {
			if world.Repositories[i].Repository.FullName == parts[1]+"/"+parts[2] {
				found = &world.Repositories[i]
			}
		}
		if found == nil {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 3:
			writeJSON(w, found.Repository)
		case len(parts) == 4 && parts[3] == "branches":
			writeJSON(w, found.Branches)
		case len(parts) == 4 && parts[3] == "commits":
			commits, ok := found.Commits[r.URL.Query().Get("sha")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
			if perPage > 0 && perPage < len(commits) {
				commits = commits[:perPage]
			}
			type author struct {
				Date time.Time `json:"date"`
			}
			type detail struct {
				Message string `json:"message"`
				Author  author `json:"author"`
			}
			type entry struct {
				SHA    string `json:"sha"`
				Commit detail `json:"commit"`
			}
			list := make([]entry, 0, len(commits))
			for _, c := range commits {
				list = append(list, entry{SHA: c.SHA, Commit: detail{Message: c.Message, Author: author{Date: c.Date}}})
			}
			writeJSON(w, list)
		default:
			http.NotFound(w, r)
		}
	})
}
