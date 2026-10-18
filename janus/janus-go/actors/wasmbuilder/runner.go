package wasmbuilder

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	dockeractor "janus/actors/docker"
)

const (
	MaxLog      = 64 << 10
	MaxArtifact = 128 << 20
)

var validImage = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)

// Result contains a ZIP artifact and bounded build log returned by a builder.
type Result struct {
	Name          string
	Artifact, Log []byte
}

// RunDocker executes command in the trusted builder image through the Docker
// controller and returns its ZIP artifact and log. The workspace is mounted by
// the Docker controller according to its isolation policy.
func RunDocker(ctx context.Context, dockerPID *actor.PID, image, workspace, command string) (Result, error) {
	if !validImage.MatchString(image) {
		return Result{}, errors.New("configure a trusted, pre-provisioned WASM builder image")
	}
	if dockerPID == nil {
		return Result{}, errors.New("Docker controller is unavailable")
	}
	response, err := actor.Ask(ctx, dockerPID, &dockeractor.Run{Image: image, Workspace: workspace, Command: command, MaxLog: MaxLog, MaxOutput: MaxArtifact + (1 << 20)}, 10*time.Minute)
	if err != nil {
		return Result{}, err
	}
	run, ok := response.(*dockeractor.Ran)
	if !ok {
		return Result{}, fmt.Errorf("unexpected Docker controller response %T", response)
	}
	if run.Err != nil {
		return Result{Log: run.Stderr}, run.Err
	}
	archive, err := zipFromTar(run.Stdout)
	if err != nil {
		return Result{Log: run.Stderr}, err
	}
	return Result{Name: "wasm-artifacts.zip", Artifact: archive, Log: run.Stderr}, nil
}

func zipFromTar(data []byte) ([]byte, error) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	tr := tar.NewReader(bytes.NewReader(data))
	var total int64
	count := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = z.Close()
			return nil, fmt.Errorf("read WASM output: %w", err)
		}
		name := strings.TrimPrefix(path.Clean(h.Name), "./")
		if h.Typeflag != tar.TypeReg || !strings.EqualFold(path.Ext(name), ".wasm") {
			continue
		}
		if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\:") {
			_ = z.Close()
			return nil, errors.New("unsafe WASM output path")
		}
		if h.Size < 0 || total+h.Size > MaxArtifact {
			_ = z.Close()
			return nil, errors.New("WASM output exceeds artifact limit")
		}
		f, err := z.Create(name)
		if err != nil {
			_ = z.Close()
			return nil, err
		}
		n, err := io.CopyN(f, tr, h.Size)
		if err != nil || n != h.Size {
			_ = z.Close()
			return nil, fmt.Errorf("read WASM output: %w", err)
		}
		total += n
		count++
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, errors.New("build completed without producing a .wasm artifact")
	}
	if b.Len() > MaxArtifact {
		return nil, errors.New("WASM archive exceeds artifact limit")
	}
	return b.Bytes(), nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	n := len(data)
	rem := b.limit - b.buffer.Len()
	if rem > 0 {
		if len(data) > rem {
			b.truncated = true
			data = data[:rem]
		}
		_, _ = b.buffer.Write(data)
	} else if n > 0 {
		b.truncated = true
	}
	return n, nil
}
func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }

// ZipFiles creates a ZIP archive of regular build outputs beneath root.
func ZipFiles(root string) ([]byte, error) {
	var names []string
	var total int64
	err := filepath.WalkDir(root, func(name string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() || e.Type()&fs.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(name), ".wasm") {
			return nil
		}
		i, err := e.Info()
		if err != nil {
			return err
		}
		total += i.Size()
		if total > MaxArtifact {
			return errors.New("WASM output exceeds artifact limit")
		}
		names = append(names, name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("build completed without producing a .wasm artifact")
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, n := range names {
		rel, _ := filepath.Rel(root, n)
		f, err := z.Create(filepath.ToSlash(rel))
		if err != nil {
			_ = z.Close()
			return nil, err
		}
		data, err := os.ReadFile(n)
		if err != nil {
			_ = z.Close()
			return nil, err
		}
		if _, err = f.Write(data); err != nil {
			_ = z.Close()
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	if b.Len() > MaxArtifact {
		return nil, errors.New("WASM archive exceeds artifact limit")
	}
	return b.Bytes(), nil
}
