// Package githubtest is a GitHub stand-in for tests of the projects service:
// a REST API on an httptest server and real Git repositories on disk that the
// service clones. Point the service's GitHubAPI at APIURL and its GitBase at
// GitBase.
package githubtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Token is the only token the API accepts.
const Token = "githubtest-token"

// GitHub serves repositories added with Commit.
type GitHub struct {
	root   string
	server *httptest.Server

	mu    sync.Mutex
	repos map[string]*repo
	// Calls counts requests by path, for tests that check what was asked.
	calls map[string]int
}

type repo struct {
	id      int64
	commits []commit // newest first
}

type commit struct {
	sha, message string
	when         time.Time
}

// New starts a GitHub with no repositories, stopped when the test ends.
func New(t testing.TB) *GitHub {
	t.Helper()
	g := &GitHub{root: t.TempDir(), repos: map[string]*repo{}, calls: map[string]int{}}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

// APIURL is the base of the REST API.
func (g *GitHub) APIURL() string { return g.server.URL }

// GitBase is what the service prepends to a repository's full name to clone it.
func (g *GitHub) GitBase() string { return filepath.ToSlash(g.root) + "/" }

// Calls returns how many requests were made for path.
func (g *GitHub) Calls(path string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[path]
}

// Commit writes files to fullName's main branch as a new commit, creating the
// repository on first use, and returns the commit's SHA.
func (g *GitHub) Commit(t testing.TB, fullName string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(g.root, filepath.FromSlash(fullName)+".git")
	repository, err := git.PlainOpen(dir)
	if err != nil {
		if err = os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if repository, err = git.PlainInit(dir, false); err != nil {
			t.Fatal(err)
		}
		if err = repository.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
			t.Fatal(err)
		}
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte(files[name]), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err = worktree.Add(name); err != nil {
			t.Fatal(err)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	r := g.repos[fullName]
	if r == nil {
		r = &repo{id: int64(len(g.repos) + 1)}
		g.repos[fullName] = r
	}
	when := time.Date(2026, 1, 1, 0, 0, len(r.commits), 0, time.UTC)
	message := fmt.Sprintf("commit %d", len(r.commits)+1)
	hash, err := worktree.Commit(message, &git.CommitOptions{AllowEmptyCommits: true, Author: &object.Signature{Name: "Test", Email: "test@example.com", When: when}})
	if err != nil {
		t.Fatal(err)
	}
	r.commits = append([]commit{{sha: hash.String(), message: message, when: when}}, r.commits...)
	return hash.String()
}

func (g *GitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls[r.URL.Path]++
	if r.Header.Get("Authorization") != "Bearer "+Token {
		http.Error(w, "bad credentials", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/user/repos" {
		names := make([]string, 0, len(g.repos))
		for name := range g.repos {
			names = append(names, name)
		}
		sort.Strings(names)
		list := make([]any, 0, len(names))
		for _, name := range names {
			list = append(list, g.repository(name))
		}
		writeJSON(w, list)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/repos/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	name := parts[0] + "/" + parts[1]
	found := g.repos[name]
	if found == nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case len(parts) == 2:
		writeJSON(w, g.repository(name))
	case len(parts) == 3 && parts[2] == "branches":
		writeJSON(w, []any{map[string]any{"name": "main", "commit": map[string]string{"sha": found.commits[0].sha}}})
	case len(parts) == 3 && parts[2] == "commits" && r.URL.Query().Get("sha") == "main":
		list := make([]any, 0, len(found.commits))
		for _, c := range found.commits {
			list = append(list, map[string]any{"sha": c.sha, "commit": map[string]any{"message": c.message, "author": map[string]any{"date": c.when}}})
		}
		writeJSON(w, list)
	default:
		http.NotFound(w, r)
	}
}

func (g *GitHub) repository(name string) map[string]any {
	return map[string]any{"id": g.repos[name].id, "full_name": name, "default_branch": "main", "language": "Go", "stargazers_count": 3, "forks_count": 1, "private": false}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
