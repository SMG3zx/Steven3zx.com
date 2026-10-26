// Package firecracker compiles a source workspace to an Artifact. One adapter per
// Template owns that Language's manifest check, Target rule and build command.
// The Template module stays the source of truth for Languages and Targets.
//
// Builds are plain calls, not actors: each runs on its caller's goroutine, so
// builds of any Language overlap, up to the limit the job runner sets. Each
// build boots its own microVM, so builds share nothing but the read-only root
// filesystems.
package firecracker

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
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

// validRootfs accepts an absolute path to a root filesystem image. Only the path
// shape is checked here; the file is checked once at startup by Ready.
func validRootfs(path string) bool {
	return filepath.IsAbs(path) && !strings.ContainsAny(path, "\x00\n")
}

type (
	Request       = builds.CompileRequest
	Result        = builds.CompileResult
	ManifestError = builds.ManifestError
)

// Executor runs a command in a microVM. Host.Execute is the real one.
type Executor func(ctx context.Context, run Run) Ran

// adapter is how one Template is built: it checks the workspace, applies the
// Target rule and returns the shell command to run in the microVM.
type adapter struct {
	rootfs  string
	command func(Request) (string, error)
}

// Firecracker builds in sandboxed microVMs, one trusted root filesystem per
// Language.
type Firecracker struct {
	adapters map[string]adapter
	execute  Executor
}

// Rootfs names the trusted root filesystem image of each Language.
type Rootfs struct{ Rust, Go, JavaScript string }

// NewFirecracker returns a Builder that runs the adapters in the root filesystems
// of images, each in a microVM booted on host.
func NewFirecracker(images Rootfs, host Host) *Firecracker {
	return newFirecracker(images, host.Execute)
}

func newFirecracker(images Rootfs, execute Executor) *Firecracker {
	return &Firecracker{execute: execute, adapters: map[string]adapter{
		"rust":                  {rootfs: strings.TrimSpace(images.Rust), command: rustCommand},
		"go":                    {rootfs: strings.TrimSpace(images.Go), command: goCommand},
		"javascript/typescript": {rootfs: strings.TrimSpace(images.JavaScript), command: javaScriptCommand},
	}}
}

// Build compiles the workspace and returns the compressed Artifact.
func (d *Firecracker) Build(ctx context.Context, language string, request Request) (Result, error) {
	a, ok := d.adapters[strings.ToLower(language)]
	if !ok {
		return Result{}, errors.New("no WASM builder for " + language)
	}
	command, err := a.command(request)
	if err != nil {
		return Result{}, err
	}
	if !validRootfs(a.rootfs) {
		return Result{}, errors.New("configure a trusted, built WASM root filesystem")
	}
	ran := d.execute(ctx, Run{Rootfs: a.rootfs, Workspace: request.Workspace, Command: command, MaxLog: MaxLog, MaxOutput: artifact.MaxBytes + (1 << 20)})
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
	pkg, err := goPackage(r.Workspace, r.Entrypoint)
	if err != nil {
		return "", err
	}
	return "cp -a /src/. /workspace/ && mkdir -p /workspace/out && cd /workspace && GOOS=wasip1 GOARCH=wasm GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build -trimpath -buildvcs=false -o /workspace/out/main.wasm " + pkg + " >&2 && cd /workspace/out && tar -cf - main.wasm", nil
}

// goPackage returns the package a Go WASM build compiles: the directory of the
// Entrypoint, such as ./cmd/server for cmd/server/main.go. With no Entrypoint it
// is the repository root. The Entrypoint goes into a shell command, so it must be
// a plain .go file inside the repository.
func goPackage(workspace, entrypoint string) (string, error) {
	if strings.TrimSpace(entrypoint) == "" {
		return ".", nil
	}
	entry := path.Clean(strings.TrimPrefix(strings.TrimSpace(entrypoint), "./"))
	if !strings.HasSuffix(entry, ".go") || !safeEntry.MatchString(entry) || strings.Contains(entry, "..") {
		return "", errors.New("the Go Entrypoint must be a .go file path in the repository, got " + entrypoint)
	}
	if info, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(entry))); err != nil || !info.Mode().IsRegular() {
		return "", errors.New("the Go Entrypoint " + entry + " is not a file in the repository")
	}
	if dir := path.Dir(entry); dir != "." {
		return "./" + dir, nil
	}
	return ".", nil
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
