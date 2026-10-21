package docker

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// JavaScriptTarget is the only WASM Target the JavaScript templates build.
// Bun bundles the Entrypoint into one file and Javy compiles the bundle together
// with the QuickJS interpreter.
const JavaScriptTarget = "wasm32-wasip1"

// safeEntry limits an Entrypoint to characters that need no shell quoting.
var safeEntry = regexp.MustCompile(`^[A-Za-z0-9_@][A-Za-z0-9_@./-]*$`)

// candidates are the files tried, in order, when no Entrypoint was confirmed
// and package.json names no main file.
var candidates = []string{"index.ts", "index.js", "index.mjs", "src/index.ts", "src/index.js", "src/main.ts", "src/main.js", "main.ts", "main.js"}

// JavaScriptCommand returns the shell command that bundles entry and compiles the bundle.
// entry must come from ResolveEntry.
func JavaScriptCommand(entry string) string {
	return "cp -a /src/. /workspace/ && mkdir -p /workspace/out && cd /workspace && " +
		"bun build ./" + entry + " --outfile /workspace/out/bundle.js --target=browser >&2 && " +
		"javy build /workspace/out/bundle.js -o /workspace/out/main.wasm >&2 && " +
		"cd /workspace/out && tar -cf - main.wasm"
}

// ResolveEntry returns the workspace-relative file to bundle. It uses confirmed
// when that is a script in the workspace, then the main field of package.json,
// then the first of the usual index files that exists. A confirmed Entrypoint
// that is not a script, such as package.json itself, is skipped.
func ResolveEntry(workspace, confirmed string) (string, error) {
	if _, err := os.Stat(filepath.Join(workspace, "package.json")); err != nil {
		return "", errors.New("JavaScript/TypeScript WASM builds require a package.json at the repository root")
	}
	try := []string{confirmed, packageMain(workspace)}
	try = append(try, candidates...)
	for _, name := range try {
		name = path.Clean(strings.TrimPrefix(strings.TrimSpace(name), "./"))
		if name == "." || !isScript(name) || !safeEntry.MatchString(name) || strings.Contains(name, "..") {
			continue
		}
		if info, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(name))); err == nil && info.Mode().IsRegular() {
			return name, nil
		}
	}
	return "", errors.New("no entry file found: confirm an Entrypoint, or set the main field in package.json")
}

func isScript(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts", ".jsx", ".tsx":
		return true
	}
	return false
}
