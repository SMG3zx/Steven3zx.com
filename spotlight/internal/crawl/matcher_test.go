package crawl

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func walkRel(t *testing.T, m *Matcher, root string) []string {
	t.Helper()
	var got []string
	err := m.Walk(func(p string, _ fs.FileInfo) error {
		rel, _ := filepath.Rel(root, p)
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	return got
}

func TestWalkAppliesBuiltinGitignoreAndSecretRules(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":                "ignored_dir/\n*.tmp\n/onlyroot.txt\n!keep.tmp\n",
		"a.go":                      "package a",
		"keep.tmp":                  "re-included by negation",
		"x.tmp":                     "ignored by *.tmp",
		"onlyroot.txt":              "ignored: anchored to root",
		"sub/onlyroot.txt":          "kept: anchored pattern doesn't reach here",
		"ignored_dir/b.go":          "ignored by .gitignore",
		"node_modules/c.js":         "builtin dir",
		"vendor/e.go":               "builtin dir",
		".claude/worktrees/w1/d.go": "builtin path",
		".claude/settings.json":     "kept: only worktrees are excluded",
		"package-lock.json":         "builtin file",
		"app.min.js":                "builtin file glob",
		"GITEA/custom/conf/app.ini": "builtin credentials file",
		"secrets.json":              "secret name",
		"tokens.json":               "secret name",
		"token.go":                  "kept: source file",
		"data/index.db":             "exact file",
		"data/index.db-wal":         "exact file",
		"nested/.gitignore":         "local.txt\n",
		"nested/local.txt":          "ignored by nested .gitignore",
		"nested/other.txt":          "kept",
		"nested/deeper/local.txt":   "ignored: unanchored pattern reaches subdirs",
		"nested/deeper/fine.txt":    "kept",
		"Dockerfile":                "FROM scratch",
	})
	m := NewMatcher(Options{
		Roots:            []string{root},
		ExactFiles:       []string{filepath.Join(root, "data", "index.db"), filepath.Join(root, "data", "index.db-wal")},
		RespectGitignore: true,
	})

	want := []string{
		".claude/settings.json",
		".gitignore",
		"Dockerfile",
		"a.go",
		"keep.tmp",
		"nested/.gitignore",
		"nested/deeper/fine.txt",
		"nested/other.txt",
		"sub/onlyroot.txt",
		"token.go",
	}
	if got := walkRel(t, m, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("walk mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestRespectGitignoreOffKeepsBuiltinRules(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":        "ignored_dir/\n",
		"ignored_dir/b.go":  "now visible",
		"node_modules/c.js": "still excluded",
		"secrets.json":      "still excluded",
	})
	m := NewMatcher(Options{Roots: []string{root}, RespectGitignore: false})
	want := []string{".gitignore", "ignored_dir/b.go"}
	if got := walkRel(t, m, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestForceIncludeOverridesGitignoreButNotBuiltins(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":        "/private/\n/index.md\n/vendor/\n",
		"private/notes.txt": "git-ignored but wanted",
		"index.md":          "git-ignored but wanted",
		"vendor/lib.go":     "builtin exclusion wins over force_include",
		"other.txt":         "plain",
	})
	m := NewMatcher(Options{
		Roots:            []string{root},
		RespectGitignore: true,
		ForceInclude:     []string{"private/", "index.md", "vendor/"},
	})
	want := []string{".gitignore", "index.md", "other.txt", "private/notes.txt"}
	if got := walkRel(t, m, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestIgnoredChecksEveryAncestor(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":            "ignored_dir/\n",
		"ignored_dir/deep/x.go": "x",
		"ok/y.go":               "y",
	})
	m := NewMatcher(Options{Roots: []string{root}, RespectGitignore: true})

	if !m.Ignored(filepath.Join(root, "ignored_dir", "deep", "x.go"), false) {
		t.Error("a file under an ignored directory must be ignored even when checked alone")
	}
	if m.Ignored(filepath.Join(root, "ok", "y.go"), false) {
		t.Error("ordinary file should be eligible")
	}
	if m.Ignored(root, true) {
		t.Error("the root itself is never ignored")
	}
	if !m.Ignored(filepath.Join(filepath.Dir(root), "elsewhere.go"), false) {
		t.Error("paths outside every root must be ignored")
	}
	if !m.Ignored(filepath.Join(root, "node_modules"), true) {
		t.Error("builtin directory names apply to directories")
	}
}

func TestExtraIgnoreOptionsAddToBuiltins(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"keep.go":             "k",
		"scratch/a.go":        "ignored by IgnoreDirs",
		"deep/old/b.go":       "ignored by IgnorePaths",
		"deep/new/c.go":       "kept",
		"notes.draft":         "ignored by IgnoreFiles",
		"node_modules/pkg.js": "builtin still applies",
	})
	m := NewMatcher(Options{
		Roots:       []string{root},
		IgnoreDirs:  []string{"scratch"},
		IgnorePaths: []string{"deep/old"},
		IgnoreFiles: []string{"*.draft"},
	})
	want := []string{"deep/new/c.go", "keep.go"}
	if got := walkRel(t, m, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGitignoreEditsArePickedUp(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.txt": "a", "b.txt": "b"})
	m := NewMatcher(Options{Roots: []string{root}, RespectGitignore: true})
	if m.Ignored(filepath.Join(root, "a.txt"), false) {
		t.Fatal("nothing ignored yet")
	}
	writeTree(t, root, map[string]string{".gitignore": "a.txt\n"})
	// The cache trusts a result for a few seconds; expire it instead of sleeping.
	m.mu.Lock()
	m.cache = make(map[string]*giEntry)
	m.mu.Unlock()
	if !m.Ignored(filepath.Join(root, "a.txt"), false) {
		t.Fatal("new .gitignore rule should apply once the cache is refreshed")
	}
}
