// Package scan reads a repository snapshot and reports what it contains: the
// recognised Languages, the JavaScript runtime and the entrypoint candidates a
// user may pick from. It works on files already in memory; fetching them is an
// adapter concern.
package scan

import (
	"encoding/json"
	"path"
	"strings"

	"janus/internal/domain/goanalysis"
)

// File is one file of a repository snapshot.
type File struct {
	Path    string
	Content []byte
}

// Candidate is a detected project entrypoint for a user to review.
type Candidate struct {
	Path      string `json:"path"`
	Ecosystem string `json:"ecosystem"`
	Reason    string `json:"reason"`
}

// Result summarizes recognized languages and entrypoint candidates in a
// repository snapshot.
type Result struct {
	Candidates []Candidate    `json:"entrypoint_candidates"`
	Languages  map[string]int `json:"languages"`
	// Runtime is "Node" or "Bun" for a JavaScript project, or "" when neither is detected.
	Runtime string `json:"runtime"`
	// Go reports which Go main packages can be built for wasip1 and why the others
	// cannot. It is nil when the snapshot has no go.mod.
	Go *goanalysis.Report `json:"go,omitempty"`
}

// detectRuntime labels a repository's JavaScript runtime from root-level files
// only. A Bun signal wins over everything else. Without a root package.json there
// is no runtime, so version pins such as .nvmrc are ignored.
func detectRuntime(files []File) string {
	var packageJSON []byte
	hasPackageJSON, hasBunLockfile := false, false
	for _, file := range files {
		name := path.Clean(strings.TrimPrefix(file.Path, "./"))
		if path.Dir(name) != "." {
			continue
		}
		switch name {
		case "package.json":
			hasPackageJSON, packageJSON = true, file.Content
		case "bun.lock", "bun.lockb":
			hasBunLockfile = true
		}
	}
	if !hasPackageJSON {
		return ""
	}
	var manifest struct {
		PackageManager string `json:"packageManager"`
	}
	_ = json.Unmarshal(packageJSON, &manifest)
	if hasBunLockfile || strings.HasPrefix(manifest.PackageManager, "bun@") {
		return "Bun"
	}
	return "Node"
}

// Files scans the files of a repository snapshot.
func Files(files []File) Result {
	result := Result{Candidates: make([]Candidate, 0), Languages: make(map[string]int), Runtime: detectRuntime(files)}
	seen := make(map[string]struct{})
	tracked := make(map[string]struct{}, len(files))
	for _, file := range files {
		tracked[path.Clean(strings.TrimPrefix(file.Path, "./"))] = struct{}{}
	}
	add := func(name, ecosystem, reason string) {
		name = path.Clean(strings.TrimPrefix(name, "./"))
		if name == "." || strings.HasPrefix(name, "../") {
			return
		}
		if _, exists := tracked[name]; !exists {
			return
		}
		if _, exists := seen[name]; exists {
			return
		}
		seen[name] = struct{}{}
		result.Candidates = append(result.Candidates, Candidate{Path: name, Ecosystem: ecosystem, Reason: reason})
	}
	for _, file := range files {
		ext := strings.ToLower(path.Ext(file.Path))
		switch ext {
		case ".go":
			result.Languages["Go"]++
		case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx":
			result.Languages["JavaScript/TypeScript"]++
		case ".py":
			result.Languages["Python"]++
		case ".rs":
			result.Languages["Rust"]++
		}
		base := path.Base(file.Path)
		switch {
		case base == "go.mod":
			if path.Dir(path.Clean(file.Path)) != "." {
				continue
			}
			inputs := make([]goanalysis.File, 0, len(files))
			for _, candidate := range files {
				inputs = append(inputs, goanalysis.File{Path: candidate.Path, Content: candidate.Content})
			}
			report := goanalysis.Analyze(inputs)
			result.Go = &report
			for _, pkg := range report.Packages {
				if pkg.Buildable {
					add(pkg.Entry, "Go", "main package that builds for wasip1")
				}
			}
		case base == "Cargo.toml":
			for _, candidate := range files {
				if path.Base(candidate.Path) == "main.rs" {
					add(candidate.Path, "Rust", "Rust binary entrypoint")
				}
			}
		case base == "pyproject.toml" || base == "requirements.txt":
			for _, candidate := range files {
				if path.Base(candidate.Path) == "__main__.py" {
					add(candidate.Path, "Python", "Python module entrypoint")
				}
			}
		case base == "package.json":
			var packageJSON struct {
				Main    string            `json:"main"`
				Bin     json.RawMessage   `json:"bin"`
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal(file.Content, &packageJSON) == nil {
				if packageJSON.Main != "" {
					add(packageJSON.Main, "Node", "package.json main field")
				}
				if start := packageJSON.Scripts["start"]; start != "" {
					add("package.json", "Node", "package.json start script: "+start)
				}
			}
		}
	}
	return result
}
