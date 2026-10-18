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

func TestScanFilesRejectsUnsafeCandidatePaths(t *testing.T) {
	result := scanFiles([]fileData{{path: "go.mod"}, {path: "../escape.go", content: []byte("package main")}})
	for _, candidate := range result.Candidates {
		if strings.HasPrefix(candidate.Path, "../") {
			t.Fatalf("unsafe candidate escaped repository: %q", candidate.Path)
		}
	}
}
