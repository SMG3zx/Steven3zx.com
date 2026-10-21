// Package builder compiles a source workspace to an Artifact. One adapter per
// Template owns that Language's manifest check, Target rule and build command.
// The Template module stays the source of truth for Languages and Targets.
//
// Builds are plain calls, not actors: each runs on its caller's goroutine, so
// builds of any Language overlap, up to the limit the job runner sets.
package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"janus/internal/domain/artifact"
	"janus/internal/services/builds"
)

const (
	// MaxLog bounds the build log kept for an Account.
	MaxLog = 64 << 10
	// artifactName is the name of the ZIP a build produces.
	artifactName = "wasm-artifacts.zip"
)

var validImage = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)

type (
	Request       = builds.CompileRequest
	Result        = builds.CompileResult
	ManifestError = builds.ManifestError
)

// Executor runs a container. Docker is the real one.
type Executor func(ctx context.Context, run Run) Ran

// adapter is how one Template is built: it checks the workspace, applies the
// Target rule and returns the shell command to run in the builder image.
type adapter struct {
	image   string
	command func(Request) (string, error)
}

// Docker builds in sandboxed containers, one trusted image per Language.
type Docker struct {
	adapters map[string]adapter
	execute  Executor
}

// Images names the trusted builder image of each Language.
type Images struct{ Rust, Go, JavaScript string }

// NewDocker returns a Builder that runs the adapters in images through Execute.
func NewDocker(images Images) *Docker { return newDocker(images, Execute) }

func newDocker(images Images, execute Executor) *Docker {
	return &Docker{execute: execute, adapters: map[string]adapter{
		"rust":                  {image: strings.TrimSpace(images.Rust), command: rustCommand},
		"go":                    {image: strings.TrimSpace(images.Go), command: goCommand},
		"javascript/typescript": {image: strings.TrimSpace(images.JavaScript), command: javaScriptCommand},
	}}
}

// Build compiles the workspace and returns the compressed Artifact.
func (d *Docker) Build(ctx context.Context, language string, request Request) (Result, error) {
	a, ok := d.adapters[strings.ToLower(language)]
	if !ok {
		return Result{}, errors.New("no WASM builder for " + language)
	}
	command, err := a.command(request)
	if err != nil {
		return Result{}, err
	}
	if !validImage.MatchString(a.image) {
		return Result{}, errors.New("configure a trusted, pre-provisioned WASM builder image")
	}
	ran := d.execute(ctx, Run{Image: a.image, Workspace: request.Workspace, Command: command, MaxLog: MaxLog, MaxOutput: artifact.MaxBytes + (1 << 20)})
	if ran.Err != nil {
		return Result{Log: ran.Stderr}, ran.Err
	}
	archive, err := artifact.ZipFromTar(ran.Stdout)
	if err != nil {
		return Result{Log: ran.Stderr}, err
	}
	compressed, err := artifact.Compress(ctx, archive)
	if err != nil {
		return Result{Log: ran.Stderr}, errors.New("WASM artifact compression failed: " + err.Error())
	}
	log := append(append([]byte{}, ran.Stderr...), "\nArtifact compression: "+compressed.Report...)
	if len(log) > MaxLog {
		log = log[:MaxLog]
	}
	return Result{Name: artifactName + artifact.ZstdExtension, Artifact: compressed.Data, Log: log}, nil
}

func requireManifest(workspace, manifest, message string) error {
	if _, err := os.Stat(filepath.Join(workspace, manifest)); err != nil {
		return &ManifestError{Message: message, Err: err}
	}
	return nil
}

func rustCommand(r Request) (string, error) {
	if r.Target != "wasm32-unknown-unknown" && r.Target != "wasm32-wasip1" {
		return "", os.ErrInvalid
	}
	if err := requireManifest(r.Workspace, "Cargo.toml", "Rust WASM builds require a Cargo.toml at the repository root"); err != nil {
		return "", err
	}
	return "cp -a /src/. /workspace/ && cargo build --locked --offline --release --target " + r.Target + " >&2 && cd /workspace/target/" + r.Target + "/release && find . -type f -name '*.wasm' -print0 | tar --null -T - -cf -", nil
}

func goCommand(r Request) (string, error) {
	if r.Target != "wasip1-wasm" {
		return "", os.ErrInvalid
	}
	if err := requireManifest(r.Workspace, "go.mod", "Go WASM builds require a go.mod at the repository root"); err != nil {
		return "", err
	}
	return "cp -a /src/. /workspace/ && mkdir -p /workspace/out && cd /workspace && GOOS=wasip1 GOARCH=wasm GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build -trimpath -buildvcs=false -o /workspace/out/main.wasm . >&2 && cd /workspace/out && tar -cf - main.wasm", nil
}

func javaScriptCommand(r Request) (string, error) {
	if r.Target != JavaScriptTarget {
		return "", errors.New("JavaScript/TypeScript WASM builds require target " + JavaScriptTarget)
	}
	entry, err := ResolveEntry(r.Workspace, r.Entrypoint)
	if err != nil {
		return "", err
	}
	return JavaScriptCommand(entry), nil
}
