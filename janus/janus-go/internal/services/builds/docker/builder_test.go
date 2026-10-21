package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"janus/internal/domain/artifact"
)

var testImages = Images{Rust: "rust-builder:local", Go: "go-builder:local", JavaScript: "js-builder:local"}

func wasmTar(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	if err := writer.WriteHeader(&tar.Header{Name: "main.wasm", Mode: 0o600, Size: 4, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write([]byte("wasm"))
	_ = writer.Close()
	return buffer.Bytes()
}

// recorder is an Executor that remembers the run it was given.
type recorder struct {
	got Run
	ran Ran
}

func (r *recorder) execute(_ context.Context, run Run) Ran {
	r.got = run
	return r.ran
}

func TestEachLanguageBuildsInItsOwnImageAndReturnsACompressedArtifact(t *testing.T) {
	for _, tc := range []struct {
		language, manifest, target, image, command string
	}{
		{"Rust", "Cargo.toml", "wasm32-wasip1", "rust-builder:local", "cargo build --locked --offline --release --target wasm32-wasip1"},
		{"Go", "go.mod", "wasip1-wasm", "go-builder:local", "GOOS=wasip1 GOARCH=wasm"},
		{"JavaScript/TypeScript", "package.json", "wasm32-wasip1", "js-builder:local", "javy build"},
	} {
		workspace := t.TempDir()
		files := map[string]string{tc.manifest: "{}", "index.js": ""}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		r := &recorder{ran: Ran{Stdout: wasmTar(t), Stderr: []byte("compiled")}}
		got, err := newDocker(testImages, r.execute).Build(context.Background(), strings.ToLower(tc.language), Request{Workspace: workspace, Target: tc.target})
		if err != nil {
			t.Fatalf("%s: %v", tc.language, err)
		}
		if r.got.Image != tc.image || !strings.Contains(r.got.Command, tc.command) {
			t.Errorf("%s ran %q in %q; want %q in %q", tc.language, r.got.Command, r.got.Image, tc.command, tc.image)
		}
		if got.Name != "wasm-artifacts.zip.zst" || !strings.Contains(string(got.Log), "compiled") || !strings.Contains(string(got.Log), "Artifact compression: zstd") {
			t.Errorf("%s result = %q %q", tc.language, got.Name, got.Log)
		}
		if module, _, err := artifact.Module(got.Artifact, got.Name); err != nil || string(module) != "wasm" {
			t.Errorf("%s artifact module = %q, %v", tc.language, module, err)
		}
	}
}

func TestMissingManifestIsReportedWithTheTemplateMessage(t *testing.T) {
	for language, want := range map[string]struct{ target, message string }{
		"Rust": {"wasm32-unknown-unknown", "Rust WASM builds require a Cargo.toml at the repository root"},
		"Go":   {"wasip1-wasm", "Go WASM builds require a go.mod at the repository root"},
	} {
		_, err := newDocker(testImages, (&recorder{}).execute).Build(context.Background(), language, Request{Workspace: t.TempDir(), Target: want.target})
		var manifest *ManifestError
		if !errors.As(err, &manifest) || manifest.Message != want.message {
			t.Errorf("%s error = %v, want manifest error %q", language, err, want.message)
		}
	}
}

func TestBuildRefusesWhatTheTemplateDoesNotCover(t *testing.T) {
	workspace := t.TempDir()
	d := newDocker(testImages, (&recorder{}).execute)
	if _, err := d.Build(context.Background(), "Haskell", Request{Workspace: workspace}); err == nil || !strings.Contains(err.Error(), "no WASM builder") {
		t.Errorf("unknown language error = %v", err)
	}
	if _, err := d.Build(context.Background(), "JavaScript/TypeScript", Request{Workspace: workspace, Target: "wasip1-wasm"}); err == nil || !strings.Contains(err.Error(), "require target wasm32-wasip1") {
		t.Errorf("wrong JavaScript target error = %v", err)
	}
}

func TestBuildRequiresATrustedImage(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, image := range []string{"", "go; malicious"} {
		_, err := newDocker(Images{Go: image}, (&recorder{}).execute).Build(context.Background(), "Go", Request{Workspace: workspace, Target: "wasip1-wasm"})
		if err == nil || !strings.Contains(err.Error(), "trusted") {
			t.Errorf("image %q error = %v", image, err)
		}
	}
}

func TestFailedBuildKeepsItsLog(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &recorder{ran: Ran{Stderr: []byte("main.go:3: undefined: x"), Err: errors.New("exit status 1")}}
	got, err := newDocker(testImages, r.execute).Build(context.Background(), "Go", Request{Workspace: workspace, Target: "wasip1-wasm"})
	if err == nil || string(got.Log) != "main.go:3: undefined: x" {
		t.Fatalf("failed build = %q, %v", got.Log, err)
	}
}
