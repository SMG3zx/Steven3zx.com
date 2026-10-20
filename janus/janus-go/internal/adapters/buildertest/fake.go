// Package buildertest holds an in-memory app.Builder and the contract suite
// every app.Builder adapter, real or test, must pass.
package buildertest

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"janus/internal/app"
	"janus/internal/domain/artifact"
)

// Module is the WASM module every Fake build produces, so a test can check the
// Artifact it gets back.
var Module = []byte("\x00asm\x01\x00\x00\x00")

// Fake is an app.Builder that compiles nothing: it applies each Language's
// manifest and Target rules to the workspace and returns an Artifact holding
// Module. It is safe for concurrent use.
type Fake struct {
	mu       sync.Mutex
	requests []app.BuildRequest
}

var _ app.Builder = (*Fake)(nil)

// NewFake returns a Fake builder for Rust, Go and JavaScript/TypeScript.
func NewFake() *Fake { return &Fake{} }

// rule is what a Language's Template requires of a workspace.
type rule struct {
	manifest, message string
	targets           []string
}

var rules = map[string]rule{
	"rust":                  {"Cargo.toml", "Rust WASM builds require a Cargo.toml at the repository root", []string{"wasm32-unknown-unknown", "wasm32-wasip1"}},
	"go":                    {"go.mod", "Go WASM builds require a go.mod at the repository root", []string{"wasip1-wasm"}},
	"javascript/typescript": {"package.json", "JavaScript/TypeScript WASM builds require a package.json at the repository root", []string{"wasm32-wasip1"}},
}

// Build checks the workspace against the Language's rules and returns the Artifact.
func (f *Fake) Build(ctx context.Context, language string, request app.BuildRequest) (app.BuildResult, error) {
	f.mu.Lock()
	f.requests = append(f.requests, request)
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return app.BuildResult{Log: []byte("stopped")}, err
	}
	r, ok := rules[strings.ToLower(language)]
	if !ok {
		return app.BuildResult{}, errors.New("no WASM builder for " + language)
	}
	if !slices.Contains(r.targets, request.Target) {
		return app.BuildResult{}, os.ErrInvalid
	}
	if _, err := os.Stat(filepath.Join(request.Workspace, r.manifest)); err != nil {
		return app.BuildResult{}, &app.ManifestError{Message: r.message, Err: err}
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("main.wasm")
	if err == nil {
		_, err = file.Write(Module)
	}
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		return app.BuildResult{}, err
	}
	compressed, err := artifact.Compress(ctx, archive.Bytes())
	if err != nil {
		return app.BuildResult{}, err
	}
	return app.BuildResult{Name: "wasm-artifacts.zip" + artifact.ZstdExtension, Artifact: compressed.Data, Log: []byte("built " + language)}, nil
}

// Requests returns the build requests received so far, oldest first.
func (f *Fake) Requests() []app.BuildRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}
