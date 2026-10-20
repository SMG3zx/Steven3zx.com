package scan

import (
	"strings"
	"testing"
)

func TestScanFilesFindsCommonEntrypointsAndLanguageCounts(t *testing.T) {
	files := []File{
		{Path: "go.mod", Content: []byte("module example.com/app")},
		{Path: "cmd/server/main.go", Content: []byte("package main\nfunc main() {}")},
		{Path: "package.json", Content: []byte(`{"main":"src/index.js","scripts":{"start":"node server.js"}}`)},
		{Path: "src/index.js"},
		{Path: "pyproject.toml"},
		{Path: "app/__main__.py"},
		{Path: "Cargo.toml"},
		{Path: "src/main.rs"},
	}
	result := Files(files)
	want := []string{"cmd/server/main.go", "src/main.rs", "app/__main__.py", "src/index.js", "package.json"}
	got := make(map[string]bool)
	for _, candidate := range result.Candidates {
		got[candidate.Path] = true
	}
	for _, candidate := range want {
		if !got[candidate] {
			t.Errorf("missing candidate %q in %#v", candidate, result.Candidates)
		}
	}
	for _, language := range []string{"Go", "Rust", "Python", "JavaScript/TypeScript"} {
		if result.Languages[language] == 0 {
			t.Errorf("missing language count for %s", language)
		}
	}
}

func TestScanFilesDetectsJavaScriptRuntime(t *testing.T) {
	tests := []struct {
		name  string
		files []File
		want  string
	}{
		{"package.json alone is Node", []File{{Path: "package.json", Content: []byte(`{}`)}}, "Node"},
		{"bun.lock is Bun", []File{{Path: "package.json", Content: []byte(`{}`)}, {Path: "bun.lock"}}, "Bun"},
		{"bun.lockb is Bun", []File{{Path: "package.json", Content: []byte(`{}`)}, {Path: "bun.lockb"}}, "Bun"},
		{"packageManager bun@ is Bun without a lockfile", []File{{Path: "package.json", Content: []byte(`{"packageManager":"bun@1.2.0"}`)}}, "Bun"},
		{"Bun lockfile wins over an npm lockfile", []File{{Path: "package.json", Content: []byte(`{}`)}, {Path: "package-lock.json"}, {Path: "bun.lock"}}, "Bun"},
		{"npm lockfile alone stays Node", []File{{Path: "package.json", Content: []byte(`{}`)}, {Path: "package-lock.json"}}, "Node"},
		{"packageManager pnpm stays Node", []File{{Path: "package.json", Content: []byte(`{"packageManager":"pnpm@9.0.0"}`)}}, "Node"},
		{"bunfig.toml is ignored", []File{{Path: "package.json", Content: []byte(`{}`)}, {Path: "bunfig.toml"}}, "Node"},
		{"version pin without package.json has no runtime", []File{{Path: ".nvmrc"}}, ""},
		{"bunfig.toml without package.json has no runtime", []File{{Path: "bunfig.toml"}}, ""},
		{"nested package.json is ignored", []File{{Path: "apps/web/package.json", Content: []byte(`{}`)}}, ""},
		{"nested bun.lock is ignored", []File{{Path: "package.json", Content: []byte(`{}`)}, {Path: "apps/api/bun.lock"}}, "Node"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Files(test.files)
			if result.Runtime != test.want {
				t.Fatalf("runtime = %q, want %q", result.Runtime, test.want)
			}
			if _, leaked := result.Languages[test.want]; leaked {
				t.Fatalf("runtime %q leaked into the language counts: %#v", test.want, result.Languages)
			}
		})
	}
}

func TestScanFilesRejectsUnsafeCandidatePaths(t *testing.T) {
	result := Files([]File{{Path: "go.mod"}, {Path: "../escape.go", Content: []byte("package main")}})
	for _, candidate := range result.Candidates {
		if strings.HasPrefix(candidate.Path, "../") {
			t.Fatalf("unsafe candidate escaped repository: %q", candidate.Path)
		}
	}
}
