package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

var validImage = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)

// Actor owns Docker health checks, image preparation, and isolated executions.
type Actor struct{}

var _ actor.Actor = (*Actor)(nil)

// New creates a Docker controller actor.
func New() *Actor                            { return &Actor{} }
func (*Actor) PreStart(*actor.Context) error { return nil }

func (*Actor) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *Health:
		ctx.Response(&Healthy{Err: checkEngine(ctx.Context())})
	case *EnsureRunning:
		ctx.Response(&Healthy{Err: ensureRunning(ctx.Context())})
	case *PrepareWASMBuilders:
		built, err := prepareWASMBuilders(ctx.Context(), msg.RustImage, msg.GoImage)
		ctx.Response(&WASMBuildersReady{BuiltImages: built, Err: err})
	case *Run:
		ctx.Response(run(ctx, msg))
	default:
		ctx.Unhandled()
	}
}

func prepareWASMBuilders(ctx context.Context, rustImage, goImage string) ([]string, error) {
	root, err := findProjectRoot()
	if err != nil {
		return nil, err
	}
	built := make([]string, 0, 2)
	for _, builder := range []struct{ name, image, dockerfile string }{
		{name: "Rust", image: rustImage, dockerfile: "Dockerfile.wasm-rust"},
		{name: "Go", image: goImage, dockerfile: "Dockerfile.wasm-go"},
	} {
		path := filepath.Join(root, "build", builder.dockerfile)
		if _, err := os.Stat(path); err != nil {
			return built, fmt.Errorf("locate %s WASM builder Dockerfile %q: %w", builder.name, path, err)
		}
		wasBuilt, err := ensureImage(ctx, builder.image, path)
		if err != nil {
			return built, fmt.Errorf("ensure %s WASM builder image %q: %w", builder.name, builder.image, err)
		}
		if wasBuilt {
			built = append(built, builder.image)
		}
	}
	return built, nil
}

func findProjectRoot() (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "build", "Dockerfile.wasm-go")); err == nil {
			if _, err := os.Stat(filepath.Join(root, "build", "Dockerfile.wasm-rust")); err == nil {
				return root, nil
			}
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", fmt.Errorf("could not find Janus build/Dockerfile.wasm-go and build/Dockerfile.wasm-rust above %q", root)
		}
		root = parent
	}
}

func ensureImage(ctx context.Context, image, dockerfile string) (bool, error) {
	if !validImage.MatchString(image) {
		return false, errors.New("configure a trusted WASM builder image name")
	}
	if strings.TrimSpace(dockerfile) == "" {
		return false, errors.New("WASM builder Dockerfile path is required")
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(inspectCtx, "docker", "image", "inspect", "--format", "{{.Id}}", image).CombinedOutput(); err == nil && strings.TrimSpace(string(output)) != "" {
		return false, nil
	}
	contextDir, err := os.MkdirTemp("", "janus-wasm-image-context-")
	if err != nil {
		return false, fmt.Errorf("create temporary Docker build context: %w", err)
	}
	defer os.RemoveAll(contextDir)
	buildCtx, cancelBuild := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelBuild()
	command := exec.CommandContext(buildCtx, "docker", "build", "--file", dockerfile, "--tag", image, contextDir)
	output, err := command.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if buildCtx.Err() != nil {
			return false, fmt.Errorf("build WASM builder image %q timed out: %w: %s", image, buildCtx.Err(), detail)
		}
		return false, fmt.Errorf("build WASM builder image %q: %w: %s", image, err, detail)
	}
	return true, nil
}

func ensureRunning(ctx context.Context) error {
	if err := checkEngine(ctx); err == nil {
		return nil
	}

	startCtx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	command := exec.CommandContext(startCtx, "docker", "desktop", "start", "--timeout", "60s")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("start Docker Desktop: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := checkEngine(startCtx); err != nil {
		return fmt.Errorf("Docker Desktop start command completed but the engine is unavailable: %w", err)
	}
	return nil
}

func checkEngine(ctx context.Context) error {
	output, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput()
	if err != nil {
		return fmt.Errorf("check Docker engine: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if strings.TrimSpace(string(output)) == "" {
		return errors.New("Docker daemon returned an empty server version")
	}
	return nil
}

func run(ctx *actor.ReceiveContext, msg *Run) *Ran {
	if !validImage.MatchString(msg.Image) {
		return &Ran{Err: errors.New("configure a trusted, pre-provisioned Docker image")}
	}
	if msg.Workspace == "" || msg.Command == "" {
		return &Ran{Err: errors.New("Docker workspace and command are required")}
	}
	maxLog, maxOutput := msg.MaxLog, msg.MaxOutput
	if maxLog <= 0 || maxLog > 64<<10 {
		maxLog = 64 << 10
	}
	if maxOutput <= 0 || maxOutput > 129<<20 {
		maxOutput = 129 << 20
	}
	args := []string{"run", "--rm", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128", "--memory=2g", "--cpus=2", "--tmpfs=/tmp:rw,noexec,nosuid,size=256m", "--tmpfs=/workspace:rw,exec,nosuid,size=1g", "--mount", "type=bind,source=" + msg.Workspace + ",target=/src,readonly", "--workdir=/workspace", "--entrypoint=sh", msg.Image, "-c", msg.Command}
	cmd := exec.CommandContext(ctx.Context(), "docker", args...)
	stdout, stderr := &limitedBuffer{limit: maxOutput}, &limitedBuffer{limit: maxLog}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if err == nil && stdout.truncated {
		err = errors.New("Docker output stream exceeds the configured limit")
	}
	return &Ran{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Err: err}
}

func (*Actor) PostStop(*actor.Context) error { return nil }

type limitedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			b.truncated = true
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	} else if n > 0 {
		b.truncated = true
	}
	return n, nil
}

func (b *limitedBuffer) Bytes() []byte { return b.data }
