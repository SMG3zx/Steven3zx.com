// Package gitsource fetches a repository at a commit with go-git. It is the
// app.Source for GitHub-style remotes and for any other path go-git can clone.
package gitsource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitHTTP "github.com/go-git/go-git/v5/plumbing/transport/http"

	"janus/internal/app"
)

const (
	maxSnapshotBytes int64 = 256 << 20
	maxFileBytes     int64 = 16 << 20
	maxSnapshotFiles       = 50_000
	cloneDepth             = 50

	// GitHubBase is the remote Base of repositories hosted on GitHub.
	GitHubBase = "https://github.com/"
)

// Git is the app.Source that clones <Base><repository>.git, authenticating
// with the Account's token as GitHub expects.
type Git struct {
	// Base is prepended to the repository's full name. It ends in a slash.
	Base string
}

var _ app.Source = (*Git)(nil)

// New returns a Source that clones from GitHub.
func New() *Git { return &Git{Base: GitHubBase} }

// Open clones the branch's last 50 commits into a temporary directory and
// returns the tree of the requested commit. Failures are *app.SourceError.
func (g *Git) Open(ctx context.Context, request app.SourceRequest) (app.SourceTree, error) {
	dir, err := os.MkdirTemp("", "janus-github-import-")
	if err != nil {
		return nil, &app.SourceError{Message: "could not allocate import workspace", Err: err}
	}
	abort := func(message string, err error) (app.SourceTree, error) {
		_ = os.RemoveAll(dir)
		return nil, &app.SourceError{Message: message, Err: err}
	}
	remote := g.Base + request.Repository + ".git"
	_, err = git.PlainCloneContext(ctx, dir, true, &git.CloneOptions{URL: remote, Auth: &gitHTTP.BasicAuth{Username: "x-access-token", Password: request.Token}, ReferenceName: plumbing.NewBranchReferenceName(request.Branch), SingleBranch: true, Depth: cloneDepth, Tags: git.NoTags})
	if err != nil {
		return abort("repository clone failed", err)
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return abort("cloned repository could not be opened", err)
	}
	commit, err := repo.CommitObject(plumbing.NewHash(request.Commit))
	if err != nil {
		return abort("selected commit was not found in the fetched history", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return abort("selected commit tree could not be read", err)
	}
	return &treeFiles{tree: tree, dir: dir}, nil
}

// treeFiles is the tree of a cloned commit. File contents are read from the
// clone, so it is deleted only by Close.
type treeFiles struct {
	tree *object.Tree
	dir  string
	once sync.Once
}

// Close deletes the clone.
func (t *treeFiles) Close() error {
	t.once.Do(func() { _ = os.RemoveAll(t.dir) })
	return nil
}

// Files reads the files of the tree. When want is not nil, only the files
// whose paths it lists are read and the others are skipped.
var _ app.SourceTree = (*treeFiles)(nil)

func (t *treeFiles) Files(want map[string]bool) ([]app.SnapshotFile, error) {
	files := make([]app.SnapshotFile, 0)
	var total int64
	err := t.tree.Files().ForEach(func(file *object.File) error {
		name := path.Clean(file.Name)
		if name == "." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
			return errors.New("repository contains an unsafe file path")
		}
		if want != nil && !want[name] {
			return nil
		}
		if len(files)+1 > maxSnapshotFiles {
			return errors.New("repository exceeds the 50,000 file snapshot limit")
		}
		if file.Mode == 0o160000 {
			contents := []byte(file.Hash.String())
			total += int64(len(contents))
			if total > maxSnapshotBytes {
				return errors.New("repository exceeds the 256 MiB snapshot limit")
			}
			files = append(files, app.SnapshotFile{Path: name, Mode: file.Mode.String(), Content: contents, Hash: file.Hash.String()})
			return nil
		}
		if file.Size > maxFileBytes {
			return fmt.Errorf("file %s exceeds the 16 MiB per-file limit", name)
		}
		total += file.Size
		if total > maxSnapshotBytes {
			return errors.New("repository exceeds the 256 MiB snapshot limit")
		}
		contents, err := file.Contents()
		if err != nil {
			return err
		}
		files = append(files, app.SnapshotFile{Path: name, Mode: file.Mode.String(), Content: []byte(contents), Hash: file.Hash.String()})
		return nil
	})
	return files, err
}
