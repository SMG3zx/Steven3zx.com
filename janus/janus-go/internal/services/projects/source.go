package projects

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
)

// The limits of one Snapshot.
const (
	maxSnapshotBytes int64 = 256 << 20
	maxFileBytes     int64 = 16 << 20
	maxSnapshotFiles       = 50_000
	cloneDepth             = 50
)

// file is one file of a cloned commit.
type file struct {
	Path, Mode, Hash string
	Content          []byte
}

// sourceError is a clone that failed. Message is the sentence the Account sees.
type sourceError struct {
	Message string
	Err     error
}

func (e *sourceError) Error() string { return e.Message + ": " + e.Err.Error() }
func (e *sourceError) Unwrap() error { return e.Err }

// gitSource clones <base><repository>.git, authenticating with the Account's
// token as GitHub expects. Any other base go-git can clone works too, such as
// a local directory in tests.
type gitSource struct{ base string }

// open clones the branch's last 50 commits into a temporary directory and
// returns the tree of the commit. Failures are *sourceError.
func (g *gitSource) open(ctx context.Context, repository, branch, commit, token string) (*tree, error) {
	dir, err := os.MkdirTemp("", "janus-github-import-")
	if err != nil {
		return nil, &sourceError{Message: "could not allocate import workspace", Err: err}
	}
	abort := func(message string, err error) (*tree, error) {
		_ = os.RemoveAll(dir)
		return nil, &sourceError{Message: message, Err: err}
	}
	options := &git.CloneOptions{URL: g.base + repository + ".git", ReferenceName: plumbing.NewBranchReferenceName(branch), SingleBranch: true, Depth: cloneDepth, Tags: git.NoTags}
	if strings.HasPrefix(g.base, "http") {
		options.Auth = &gitHTTP.BasicAuth{Username: "x-access-token", Password: token}
	}
	if _, err = git.PlainCloneContext(ctx, dir, true, options); err != nil {
		return abort("repository clone failed", err)
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return abort("cloned repository could not be opened", err)
	}
	found, err := repo.CommitObject(plumbing.NewHash(commit))
	if err != nil {
		return abort("selected commit was not found in the fetched history", err)
	}
	objects, err := found.Tree()
	if err != nil {
		return abort("selected commit tree could not be read", err)
	}
	return &tree{objects: objects, dir: dir}, nil
}

// tree is the tree of a cloned commit. File contents are read from the clone,
// so it is deleted only by close.
type tree struct {
	objects *object.Tree
	dir     string
	once    sync.Once
}

func (t *tree) close() { t.once.Do(func() { _ = os.RemoveAll(t.dir) }) }

// files reads the files of the tree within the Snapshot limits. When want is
// not nil, only the files whose paths it lists are read.
func (t *tree) files(want map[string]bool) ([]file, error) {
	files := make([]file, 0)
	var total int64
	err := t.objects.Files().ForEach(func(entry *object.File) error {
		name := path.Clean(entry.Name)
		if name == "." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
			return errors.New("repository contains an unsafe file path")
		}
		if want != nil && !want[name] {
			return nil
		}
		if len(files)+1 > maxSnapshotFiles {
			return errors.New("repository exceeds the 50,000 file snapshot limit")
		}
		if entry.Mode == 0o160000 {
			contents := []byte(entry.Hash.String())
			total += int64(len(contents))
			if total > maxSnapshotBytes {
				return errors.New("repository exceeds the 256 MiB snapshot limit")
			}
			files = append(files, file{Path: name, Mode: entry.Mode.String(), Content: contents, Hash: entry.Hash.String()})
			return nil
		}
		if entry.Size > maxFileBytes {
			return fmt.Errorf("file %s exceeds the 16 MiB per-file limit", name)
		}
		total += entry.Size
		if total > maxSnapshotBytes {
			return errors.New("repository exceeds the 256 MiB snapshot limit")
		}
		contents, err := entry.Contents()
		if err != nil {
			return err
		}
		files = append(files, file{Path: name, Mode: entry.Mode.String(), Content: []byte(contents), Hash: entry.Hash.String()})
		return nil
	})
	return files, err
}
