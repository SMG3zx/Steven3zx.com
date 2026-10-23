// Package extract turns file bytes into plain text and says how large a file of
// each kind may be. Plain text, source code, config and Excalidraw drawings are
// supported; PDF and Office handlers plug in here later.
package extract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrUnsupported means the file type or content can't be turned into text.
var ErrUnsupported = errors.New("unsupported file type")

// ErrGenerated means the file looks machine-generated or minified (a very long
// line), which makes for useless search results. It is an ErrUnsupported.
var ErrGenerated = fmt.Errorf("%w: looks generated or minified", ErrUnsupported)

// Kind groups file types by how they are size-limited and read.
type Kind int

const (
	KindNone       Kind = iota // not indexed
	KindText                   // prose, source code, config
	KindData                   // JSON, CSV, logs and similar: big ones are noise
	KindExcalidraw             // drawings: only the text labels are indexed
)

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

var (
	textExts = set(
		// prose
		".txt", ".md", ".markdown", ".mdx", ".rst", ".org", ".tex", ".bib",
		// source
		".go", ".mod", ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs",
		".vue", ".svelte", ".astro", ".py", ".rs", ".java", ".kt", ".kts", ".scala",
		".swift", ".rb", ".php", ".lua", ".dart", ".ex", ".exs", ".erl", ".hs", ".ml",
		".clj", ".r", ".jl", ".c", ".h", ".cc", ".cpp", ".hpp", ".cs", ".zig", ".nim",
		".sh", ".bash", ".zsh", ".fish", ".ps1", ".psm1", ".psd1", ".bat", ".cmd",
		".sql", ".graphql", ".gql", ".proto", ".tf", ".hcl", ".nix", ".rego",
		".wgsl", ".glsl", ".hlsl", ".vert", ".frag", ".wat", ".http",
		// config and markup
		".toml", ".ini", ".conf", ".cfg", ".properties", ".gradle", ".mk", ".cmake",
		".html", ".htm", ".css", ".scss", ".sass", ".less", ".tour", ".code-snippets",
	)
	dataExts = set(".json", ".jsonc", ".yaml", ".yml", ".xml", ".csv", ".tsv", ".log")

	// Extensionless files worth indexing, matched on the lower-cased base name.
	textNames = set(
		"dockerfile", "containerfile", "makefile", "gnumakefile", "caddyfile",
		"license", "licence", "copying", "notice", "authors", "readme", "codeowners",
		"jenkinsfile", "procfile", "gemfile", "rakefile", "justfile", "vagrantfile", "brewfile",
	)

	// Prose may legitimately have very long lines (a paragraph per line).
	proseExts = set(".txt", ".md", ".markdown", ".mdx", ".rst", ".org", ".tex", ".bib")
)

// KindOf classifies path by its name.
func KindOf(path string) Kind {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case ext == ".excalidraw":
		return KindExcalidraw
	case dataExts[ext]:
		return KindData
	case textExts[ext]:
		return KindText
	case ext == "" && textNames[strings.ToLower(filepath.Base(path))]:
		return KindText
	}
	return KindNone
}

// Text returns the indexable text of data. maxLine, when positive, rejects
// non-prose files with a line longer than that many bytes: minified bundles and
// generated code, whose chunks are noise.
func Text(path string, data []byte, maxLine int) (string, error) {
	switch KindOf(path) {
	case KindNone:
		return "", ErrUnsupported
	case KindExcalidraw:
		return excalidrawText(data)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", ErrUnsupported // binary
	}
	if maxLine > 0 && !proseExts[strings.ToLower(filepath.Ext(path))] && longestLine(data) > maxLine {
		return "", ErrGenerated
	}
	return strings.ToValidUTF8(string(data), "�"), nil
}

func longestLine(data []byte) int {
	longest, cur := 0, 0
	for _, b := range data {
		if b == '\n' {
			longest = max(longest, cur)
			cur = 0
			continue
		}
		cur++
	}
	return max(longest, cur)
}

// excalidrawText returns the text labels of a drawing. Drawings are mostly
// embedded images and coordinates, so this is a small fraction of the file.
func excalidrawText(data []byte) (string, error) {
	var doc struct {
		Elements []struct {
			Text      string `json:"text"`
			IsDeleted bool   `json:"isDeleted"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", ErrUnsupported
	}
	var parts []string
	for _, e := range doc.Elements {
		if t := strings.TrimSpace(e.Text); t != "" && !e.IsDeleted {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "", ErrUnsupported // no text: nothing to search
	}
	return strings.Join(parts, "\n\n"), nil
}
