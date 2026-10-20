package buildertest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"janus/internal/app"
	"janus/internal/domain/artifact"
)

// Run runs the Builder contract against adapters made by newBuilder. It builds
// Go workspaces, which every Builder covers, so an adapter's factory must be
// able to build one. Each case gets a fresh Builder.
func Run(t *testing.T, newBuilder func(t *testing.T) app.Builder) {
	t.Helper()
	for _, c := range []struct {
		name string
		test func(t *testing.T, b app.Builder)
	}{
		{"BuildsAnArtifactHoldingTheModule", testBuildsAnArtifact},
		{"MatchesTheLanguageWithoutRegardToCase", testLanguageCase},
		{"ReportsAMissingManifest", testMissingManifest},
		{"RefusesATargetTheTemplateDoesNotCover", testRefusesTarget},
		{"RefusesALanguageNoTemplateCovers", testRefusesLanguage},
	} {
		t.Run(c.name, func(t *testing.T) { c.test(t, newBuilder(t)) })
	}
}

const goTarget = "wasip1-wasm"

// goWorkspace returns a workspace that a Go build accepts.
func goWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"go.mod": "module example\n", "main.go": "package main\nfunc main() {}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func testBuildsAnArtifact(t *testing.T, b app.Builder) {
	got, err := b.Build(context.Background(), "go", app.BuildRequest{Workspace: goWorkspace(t), Target: goTarget})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name == "" || len(got.Artifact) == 0 {
		t.Fatalf("result = %q with %d artifact bytes", got.Name, len(got.Artifact))
	}
	module, _, err := artifact.Module(got.Artifact, got.Name)
	if err != nil {
		t.Fatalf("artifact cannot be opened: %v", err)
	}
	if len(module) == 0 {
		t.Fatalf("artifact module = %q", module)
	}
}

func testLanguageCase(t *testing.T, b app.Builder) {
	if _, err := b.Build(context.Background(), "GO", app.BuildRequest{Workspace: goWorkspace(t), Target: goTarget}); err != nil {
		t.Fatal(err)
	}
}

func testMissingManifest(t *testing.T, b app.Builder) {
	_, err := b.Build(context.Background(), "go", app.BuildRequest{Workspace: t.TempDir(), Target: goTarget})
	var manifest *app.ManifestError
	if !errors.As(err, &manifest) || manifest.Message == "" {
		t.Fatalf("error = %v, want a *app.ManifestError with a message", err)
	}
}

func testRefusesTarget(t *testing.T, b app.Builder) {
	if _, err := b.Build(context.Background(), "go", app.BuildRequest{Workspace: goWorkspace(t), Target: "wasm32-wasip1"}); err == nil {
		t.Fatal("a Go build for a Rust Target succeeded")
	}
}

func testRefusesLanguage(t *testing.T, b app.Builder) {
	if _, err := b.Build(context.Background(), "cobol", app.BuildRequest{Workspace: goWorkspace(t), Target: goTarget}); err == nil {
		t.Fatal("a build for an unknown Language succeeded")
	}
}
