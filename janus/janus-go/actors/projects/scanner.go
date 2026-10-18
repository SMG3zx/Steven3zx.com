package projects

import (
	"encoding/json"
	"path"
	"strings"
)

// Candidate is a detected project entrypoint for a user to review.
type Candidate struct {
	Path      string `json:"path"`
	Ecosystem string `json:"ecosystem"`
	Reason    string `json:"reason"`
}

// ScanResult summarizes recognized languages and entrypoint candidates in a
// repository snapshot.
type ScanResult struct {
	Candidates []Candidate    `json:"entrypoint_candidates"`
	Languages  map[string]int `json:"languages"`
}

func scanFiles(files []fileData) ScanResult {
	result := ScanResult{Candidates: make([]Candidate, 0), Languages: make(map[string]int)}
	seen := make(map[string]struct{})
	tracked := make(map[string]struct{}, len(files))
	for _, file := range files {
		tracked[path.Clean(strings.TrimPrefix(file.path, "./"))] = struct{}{}
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
		ext := strings.ToLower(path.Ext(file.path))
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
		base := path.Base(file.path)
		switch {
		case base == "go.mod":
			for _, candidate := range files {
				if strings.HasSuffix(candidate.path, ".go") && strings.Contains(string(candidate.content), "package main") {
					add(candidate.path, "Go", "contains a main package")
				}
			}
		case base == "Cargo.toml":
			for _, candidate := range files {
				if path.Base(candidate.path) == "main.rs" {
					add(candidate.path, "Rust", "Rust binary entrypoint")
				}
			}
		case base == "pyproject.toml" || base == "requirements.txt":
			for _, candidate := range files {
				if path.Base(candidate.path) == "__main__.py" {
					add(candidate.path, "Python", "Python module entrypoint")
				}
			}
		case base == "package.json":
			var packageJSON struct {
				Main    string            `json:"main"`
				Bin     json.RawMessage   `json:"bin"`
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal(file.content, &packageJSON) == nil {
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
