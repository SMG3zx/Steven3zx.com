// Package hosttest holds test adapters for app.Source and app.GitHost and the
// contract suites every adapter of those ports, real or test, must pass.
package hosttest

import (
	"context"
	"crypto/sha1" //nolint:gosec // a Git blob hash is SHA-1 by definition
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"janus/internal/app"
)

// DirSource is an app.Source that serves a local directory as one commit. It
// ignores the repository, branch and token of a request and fails for any
// commit but its own.
type DirSource struct {
	dir, commit string
}

var _ app.Source = (*DirSource)(nil)

// NewDirSource returns a Source whose only commit is commit and whose tree is
// the regular files under dir.
func NewDirSource(dir, commit string) *DirSource { return &DirSource{dir: dir, commit: commit} }

// Open returns the directory's tree when request names the Source's commit.
func (s *DirSource) Open(_ context.Context, request app.SourceRequest) (app.SourceTree, error) {
	if request.Commit != s.commit {
		return nil, &app.SourceError{Message: "selected commit was not found in the fetched history", Err: fmt.Errorf("commit %s", request.Commit)}
	}
	return dirTree{dir: s.dir}, nil
}

type dirTree struct{ dir string }

func (dirTree) Close() error { return nil }

// Files reads the regular files of the directory, as Git records them: mode
// 0100644 and the hash of the content as a Git blob.
func (t dirTree) Files(want map[string]bool) ([]app.SnapshotFile, error) {
	files := make([]app.SnapshotFile, 0)
	err := filepath.WalkDir(t.dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(t.dir, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if want != nil && !want[rel] {
			return nil
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		files = append(files, app.SnapshotFile{Path: rel, Mode: "0100644", Hash: BlobHash(content), Content: content})
		return nil
	})
	return files, err
}

// BlobHash returns the hash Git gives a blob with this content.
func BlobHash(content []byte) string {
	h := sha1.New() //nolint:gosec // see the import
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(content))
	_, _ = h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

var errNotRepository = errors.New("repository not found")

// splitName reports whether fullName is "owner/name".
func splitName(fullName string) bool {
	owner, repo, ok := strings.Cut(fullName, "/")
	return ok && owner != "" && repo != "" && !strings.Contains(repo, "/")
}
