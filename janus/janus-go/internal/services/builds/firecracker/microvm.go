package firecracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Host is the Firecracker setup the jobs run on.
type Host struct {
	// Binary is the firecracker executable.
	Binary string
	// Kernel is an uncompressed vmlinux that every guest boots.
	Kernel string
	// WorkDir holds one scratch directory per job.
	WorkDir string
}

// Run requests one microVM execution. The guest boots a fresh copy of the
// read-only root filesystem Rootfs, gets Workspace as a read-only source tree
// and runs Command in a writable /workspace. Every job gets the same resource
// and filesystem limits.
type Run struct {
	Rootfs, Workspace, Command string
	MaxLog, MaxOutput          int
}

// Ran is the outcome of a Run. Stdout is the bounded output stream and Stderr
// the bounded log. A command that exits non-zero is an error.
type Ran struct {
	Stdout, Stderr []byte
	Err            error
}

// Execute boots one microVM for msg, waits for its command and returns what it
// wrote. It runs on the caller's goroutine, so any number of jobs proceed at
// once; the caller bounds how many. When ctx ends, the VM is killed.
func (h Host) Execute(ctx context.Context, msg Run) Ran {
	if !validRootfs(msg.Rootfs) {
		return Ran{Err: errors.New("configure a trusted, built WASM root filesystem")}
	}
	if msg.Workspace == "" || msg.Command == "" {
		return Ran{Err: errors.New("microVM workspace and command are required")}
	}
	maxLog, maxOutput := msg.MaxLog, msg.MaxOutput
	if maxLog <= 0 || maxLog > 64<<10 {
		maxLog = 64 << 10
	}
	if maxOutput <= 0 || maxOutput > 129<<20 {
		maxOutput = 129 << 20
	}
	if err := os.MkdirAll(h.WorkDir, 0o700); err != nil {
		return Ran{Err: fmt.Errorf("create the microVM work directory: %w", err)}
	}
	dir, err := os.MkdirTemp(h.WorkDir, "job-")
	if err != nil {
		return Ran{Err: fmt.Errorf("create the microVM job directory: %w", err)}
	}
	defer os.RemoveAll(dir)
	drives, err := prepareDrives(ctx, dir, msg, maxLog, maxOutput)
	if err != nil {
		return Ran{Err: err}
	}
	configPath := filepath.Join(dir, "vm.json")
	if err := os.WriteFile(configPath, drives.config(h.Kernel), 0o600); err != nil {
		return Ran{Err: fmt.Errorf("write the microVM configuration: %w", err)}
	}

	console := &limitedBuffer{limit: maxLog}
	cmd := exec.CommandContext(ctx, h.Binary, "--no-api", "--config-file", configPath)
	cmd.Stdout, cmd.Stderr = console, console
	cmd.Cancel = func() error { return cmd.Process.Kill() }
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		return Ran{Stderr: console.Bytes(), Err: fmt.Errorf("run the microVM: %w", err)}
	}
	return collect(ctx, drives.output, maxLog, maxOutput)
}

// collect reads what the guest wrote to its output drive. The guest's init
// writes the command's status to "status", its output to "stdout" and "stderr".
func collect(ctx context.Context, output string, maxLog, maxOutput int) Ran {
	dir := filepath.Dir(output)
	read := func(name string, limit int) ([]byte, bool, error) {
		dst := filepath.Join(dir, "read-"+name)
		defer os.Remove(dst)
		if err := extract(ctx, output, name, dst); err != nil {
			return nil, false, err
		}
		file, err := os.Open(dst)
		if err != nil {
			return nil, false, fmt.Errorf("read %s from the microVM: %w", name, err)
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
		if err != nil {
			return nil, false, fmt.Errorf("read %s from the microVM: %w", name, err)
		}
		if len(data) > limit {
			return data[:limit], true, nil
		}
		return data, false, nil
	}

	statusBytes, _, err := read("status", 64)
	if err != nil {
		return Ran{Err: err}
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(statusBytes)))
	if err != nil {
		return Ran{Err: errors.New("the microVM did not report its command status")}
	}
	stdout, truncated, err := read("stdout", maxOutput)
	if err != nil {
		return Ran{Err: err}
	}
	stderr, _, err := read("stderr", maxLog)
	if err != nil {
		return Ran{Err: err}
	}
	ran := Ran{Stdout: stdout, Stderr: stderr}
	switch {
	case status != 0:
		ran.Err = fmt.Errorf("the command exited with status %d", status)
	case truncated:
		ran.Err = errors.New("the microVM output stream exceeds the configured limit")
	}
	return ran
}

// limitedBuffer keeps the first limit bytes written to it and reports the rest
// as truncated.
type limitedBuffer struct {
	data  []byte
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func (b *limitedBuffer) Bytes() []byte { return b.data }
