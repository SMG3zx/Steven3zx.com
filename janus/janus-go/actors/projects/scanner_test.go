package projects

import (
	"strings"
	"testing"
)

func TestScanFilesFindsCommonEntrypointsAndLanguageCounts(t *testing.T) {
	files := []fileData{
		{path: "go.mod", content: []byte("module example.com/app")},
		{path: "cmd/server/main.go", content: []byte("package main\nfunc main() {}")},
		{path: "package.json", content: []byte(`{"main":"src/index.js","scripts":{"start":"node server.js"}}`)},
		{path: "src/index.js"},
		{path: "pyproject.toml"},
		{path: "app/__main__.py"},
		{path: "Cargo.toml"},
		{path: "src/main.rs"},
	}
	result := scanFiles(files)
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
		files []fileData
		want  string
	}{
		{"package.json alone is Node", []fileData{{path: "package.json", content: []byte(`{}`)}}, "Node"},
		{"bun.lock is Bun", []fileData{{path: "package.json", content: []byte(`{}`)}, {path: "bun.lock"}}, "Bun"},
		{"bun.lockb is Bun", []fileData{{path: "package.json", content: []byte(`{}`)}, {path: "bun.lockb"}}, "Bun"},
		{"packageManager bun@ is Bun without a lockfile", []fileData{{path: "package.json", content: []byte(`{"packageManager":"bun@1.2.0"}`)}}, "Bun"},
		{"Bun lockfile wins over an npm lockfile", []fileData{{path: "package.json", content: []byte(`{}`)}, {path: "package-lock.json"}, {path: "bun.lock"}}, "Bun"},
		{"npm lockfile alone stays Node", []fileData{{path: "package.json", content: []byte(`{}`)}, {path: "package-lock.json"}}, "Node"},
		{"packageManager pnpm stays Node", []fileData{{path: "package.json", content: []byte(`{"packageManager":"pnpm@9.0.0"}`)}}, "Node"},
		{"bunfig.toml is ignored", []fileData{{path: "package.json", content: []byte(`{}`)}, {path: "bunfig.toml"}}, "Node"},
		{"version pin without package.json has no runtime", []fileData{{path: ".nvmrc"}}, ""},
		{"bunfig.toml without package.json has no runtime", []fileData{{path: "bunfig.toml"}}, ""},
		{"nested package.json is ignored", []fileData{{path: "apps/web/package.json", content: []byte(`{}`)}}, ""},
		{"nested bun.lock is ignored", []fileData{{path: "package.json", content: []byte(`{}`)}, {path: "apps/api/bun.lock"}}, "Node"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := scanFiles(test.files)
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
	result := scanFiles([]fileData{{path: "go.mod"}, {path: "../escape.go", content: []byte("package main")}})
	for _, candidate := range result.Candidates {
		if strings.HasPrefix(candidate.Path, "../") {
			t.Fatalf("unsafe candidate escaped repository: %q", candidate.Path)
		}
	}
}
