package jsbuilder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workspace(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestResolveEntry(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		confirmed string
		want      string
		wantErr   string
	}{
		{"confirmed entry wins", map[string]string{"package.json": "{}", "app/run.ts": "", "index.js": ""}, "app/run.ts", "app/run.ts", ""},
		{"leading ./ is accepted", map[string]string{"package.json": "{}", "run.js": ""}, "./run.js", "run.js", ""},
		{"package.json is not a script", map[string]string{"package.json": `{"main":"lib/x.js"}`, "lib/x.js": ""}, "package.json", "lib/x.js", ""},
		{"main field", map[string]string{"package.json": `{"main":"./lib/x.js"}`, "lib/x.js": "", "index.js": ""}, "", "lib/x.js", ""},
		{"main field names a missing file", map[string]string{"package.json": `{"main":"gone.js"}`, "src/index.ts": ""}, "", "src/index.ts", ""},
		{"usual index file", map[string]string{"package.json": "{}", "index.js": "", "index.ts": ""}, "", "index.ts", ""},
		{"confirmed file is missing", map[string]string{"package.json": "{}", "index.js": ""}, "nope.js", "index.js", ""},
		{"escaping entry is ignored", map[string]string{"package.json": "{}", "index.js": ""}, "../../etc/passwd.js", "index.js", ""},
		{"shell characters are ignored", map[string]string{"package.json": "{}", "a b.js": "", "index.js": ""}, "a b.js", "index.js", ""},
		{"no package.json", map[string]string{"index.js": ""}, "", "", "require a package.json"},
		{"no entry file", map[string]string{"package.json": "{}", "README.md": ""}, "", "", "no entry file found"},
	}
	for _, tc := range tests {
		got, err := ResolveEntry(workspace(t, tc.files), tc.confirmed)
		switch {
		case tc.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err = %v, want %q", tc.name, err, tc.wantErr)
			}
		case err != nil || got != tc.want:
			t.Errorf("%s: got %q, %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestCommandBundlesThenCompilesAndStreamsTheModule(t *testing.T) {
	command := Command("src/index.ts")
	for _, part := range []string{"bun build ./src/index.ts", "--target=browser", "javy build /workspace/out/bundle.js", "tar -cf - main.wasm"} {
		if !strings.Contains(command, part) {
			t.Errorf("command is missing %q: %s", part, command)
		}
	}
}
