package gitsource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"janus/internal/adapters/hosttest"
	"janus/internal/app"
)

// commitRepository writes files to <root>/alice/repo.git as one commit on main
// and returns the commit's hash.
func commitRepository(t *testing.T, root string, files map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(root, "alice", "repo.git")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err = worktree.Add(name); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := worktree.Commit("fixture", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	return hash.String()
}

func localSource(root string) *Git { return &Git{Base: filepath.ToSlash(root) + "/"} }

func TestGitPassesTheSourceContract(t *testing.T) {
	hosttest.RunSource(t, func(t *testing.T, files map[string]string) (app.Source, app.SourceRequest) {
		root := t.TempDir()
		contents := make(map[string][]byte, len(files))
		for name, content := range files {
			contents[name] = []byte(content)
		}
		commit := commitRepository(t, root, contents)
		return localSource(root), app.SourceRequest{Repository: "alice/repo", Branch: "main", Commit: commit, Token: "token"}
	})
}

func TestFilesEnforcesThePerFileLimitBeforeReadingContent(t *testing.T) {
	root := t.TempDir()
	commit := commitRepository(t, root, map[string][]byte{"big.bin": make([]byte, maxFileBytes+1)})
	tree, err := localSource(root).Open(context.Background(), app.SourceRequest{Repository: "alice/repo", Branch: "main", Commit: commit})
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	if _, err = tree.Files(nil); err == nil || !strings.Contains(err.Error(), "16 MiB per-file limit") {
		t.Fatalf("expected per-file limit error, got %v", err)
	}
}

func TestOpenReportsTheSentenceAnAccountSees(t *testing.T) {
	root := t.TempDir()
	commit := commitRepository(t, root, map[string][]byte{"a": []byte("a")})
	request := app.SourceRequest{Repository: "alice/repo", Branch: "main", Commit: strings.Repeat("e", 40)}
	_, err := localSource(root).Open(context.Background(), request)
	if se, ok := err.(*app.SourceError); !ok || se.Message != "selected commit was not found in the fetched history" {
		t.Fatalf("unknown commit: %v", err)
	}
	request.Commit, request.Repository = commit, "alice/missing"
	_, err = localSource(root).Open(context.Background(), request)
	if se, ok := err.(*app.SourceError); !ok || se.Message != "repository clone failed" {
		t.Fatalf("unknown repository: %v", err)
	}
}

func TestCloseDeletesTheClone(t *testing.T) {
	root := t.TempDir()
	commit := commitRepository(t, root, map[string][]byte{"a": []byte("a")})
	tree, err := localSource(root).Open(context.Background(), app.SourceRequest{Repository: "alice/repo", Branch: "main", Commit: commit})
	if err != nil {
		t.Fatal(err)
	}
	dir := tree.(*treeFiles).dir
	if _, err = os.Stat(dir); err != nil {
		t.Fatal(err)
	}
	if err = tree.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("clone still exists: %v", err)
	}
}
