package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PrepareImages makes sure the three WASM builder images exist locally, building
// any that are missing from the Dockerfiles under build/. It returns the images it
// had to build.
func PrepareImages(ctx context.Context, images Images) ([]string, error) {
	rustImage, goImage, jsImage := images.Rust, images.Go, images.JavaScript
	root, err := findProjectRoot()
	if err != nil {
		return nil, err
	}
	built := make([]string, 0, 3)
	for _, builder := range []struct{ name, image, dockerfile string }{
		{name: "Rust", image: rustImage, dockerfile: "Dockerfile.wasm-rust"},
		{name: "Go", image: goImage, dockerfile: "Dockerfile.wasm-go"},
		{name: "JavaScript/TypeScript", image: jsImage, dockerfile: "Dockerfile.wasm-js"},
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

// EnsureRunning checks the Docker Engine and starts Docker Desktop if needed.
func EnsureRunning(ctx context.Context) error {
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
