package projects

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func commitFixture(t *testing.T, contents []byte) *object.Tree {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "main.go")
	if err = os.WriteFile(file, contents, 0600); err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worktree.Add("main.go"); err != nil {
		t.Fatal(err)
	}
	_, err = worktree.Commit("fixture", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestReadSnapshotTreeFromLocalGitFixture(t *testing.T) {
	tree := commitFixture(t, []byte("package main\nfunc main() {}\n"))
	files, total, err := readSnapshotTree(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].path != "main.go" || string(files[0].content) != "package main\nfunc main() {}\n" || total != int64(len(files[0].content)) {
		t.Fatalf("unexpected imported tree: files=%#v total=%d", files, total)
	}
	if files[0].hash == "" {
		t.Fatal("blob hash is empty")
	}
}

func TestReadSnapshotTreeEnforcesPerFileLimitBeforeReadingContent(t *testing.T) {
	tree := commitFixture(t, make([]byte, maxFileBytes+1))
	_, _, err := readSnapshotTree(tree)
	if err == nil || !strings.Contains(err.Error(), "16 MiB per-file limit") {
		t.Fatalf("expected per-file limit error, got %v", err)
	}
}
