package hosttest

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"janus/internal/app"
)

// RunSource runs the Source contract against adapters made by newSource. The
// factory serves a repository whose latest commit holds exactly files (path to
// content) and returns the Source with the request that fetches that commit.
// Each case gets a fresh Source.
func RunSource(t *testing.T, newSource func(t *testing.T, files map[string]string) (app.Source, app.SourceRequest)) {
	t.Helper()
	files := map[string]string{
		"Cargo.toml":     "[package]\nname = \"demo\"\n",
		"src/lib.rs":     "pub fn demo() {}\n",
		"docs/a b.txt":   "a name with a space\n",
		"empty":          "",
		"deep/er/er.txt": "nested\n",
	}
	for _, c := range []struct {
		name string
		test func(t *testing.T, source app.Source, request app.SourceRequest, files map[string]string)
	}{
		{"ReadsEveryFileOfTheCommit", testReadsEveryFile},
		{"ReadsOnlyTheWantedFiles", testReadsWantedFiles},
		{"RefusesACommitItDoesNotHave", testRefusesUnknownCommit},
	} {
		t.Run(c.name, func(t *testing.T) {
			source, request := newSource(t, maps.Clone(files))
			c.test(t, source, request, files)
		})
	}
}

func openTree(t *testing.T, source app.Source, request app.SourceRequest) app.SourceTree {
	t.Helper()
	tree, err := source.Open(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tree.Close(); err != nil {
			t.Error(err)
		}
	})
	return tree
}

func testReadsEveryFile(t *testing.T, source app.Source, request app.SourceRequest, files map[string]string) {
	got, err := openTree(t, source, request).Files(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(files) {
		t.Fatalf("read %d files, want %d", len(got), len(files))
	}
	for _, file := range got {
		content, ok := files[file.Path]
		if !ok {
			t.Errorf("unexpected file %q", file.Path)
			continue
		}
		if string(file.Content) != content {
			t.Errorf("%s content = %q, want %q", file.Path, file.Content, content)
		}
		if want := BlobHash([]byte(content)); file.Hash != want {
			t.Errorf("%s hash = %q, want the Git blob hash %q", file.Path, file.Hash, want)
		}
		if file.Mode != "0100644" {
			t.Errorf("%s mode = %q, want 0100644", file.Path, file.Mode)
		}
		if strings.HasPrefix(file.Path, "/") || strings.Contains(file.Path, "\\") || strings.Contains(file.Path, "..") {
			t.Errorf("path %q is not clean and relative", file.Path)
		}
	}
}

func testReadsWantedFiles(t *testing.T, source app.Source, request app.SourceRequest, _ map[string]string) {
	want := map[string]bool{"Cargo.toml": true, "deep/er/er.txt": true, "missing.txt": true}
	got, err := openTree(t, source, request).Files(want)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, file := range got {
		paths = append(paths, file.Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"Cargo.toml", "deep/er/er.txt"}) {
		t.Fatalf("paths = %q", paths)
	}
}

func testRefusesUnknownCommit(t *testing.T, source app.Source, request app.SourceRequest, _ map[string]string) {
	request.Commit = strings.Repeat("f", 40)
	tree, err := source.Open(context.Background(), request)
	if err == nil {
		_ = tree.Close()
		t.Fatal("opened a commit the repository does not have")
	}
}
