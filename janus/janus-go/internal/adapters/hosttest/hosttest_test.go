package hosttest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"janus/internal/app"
)

func TestHostPassesTheGitHostContract(t *testing.T) {
	RunHost(t, func(_ *testing.T, world World) app.GitHost { return NewHost(world) })
}

func TestDirSourcePassesTheSourceContract(t *testing.T) {
	commit := strings.Repeat("a", 40)
	RunSource(t, func(t *testing.T, files map[string]string) (app.Source, app.SourceRequest) {
		dir := t.TempDir()
		for name, content := range files {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return NewDirSource(dir, commit), app.SourceRequest{Repository: "alice/repo", Branch: "main", Commit: commit}
	})
}
