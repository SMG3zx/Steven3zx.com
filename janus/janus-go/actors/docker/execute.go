package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os/exec"
	"time"
)

// Run requests one isolated container execution. Every job gets the same
// resource and filesystem limits.
type Run struct {
	Image, Workspace, Command string
	MaxLog, MaxOutput         int
}

// Ran is the outcome of a Run. Stdout is the bounded output stream and Stderr
// the bounded log.
type Ran struct {
	Stdout, Stderr []byte
	Err            error
}

// dockerCommand builds a Docker CLI invocation. Tests replace it.
var dockerCommand = func(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "docker", args...)
}

// Execute runs msg in an isolated container and waits for it. It runs on the
// caller's goroutine, so any number of runs proceed at once; the caller bounds
// how many. When ctx ends, the container is stopped and removed.
func Execute(ctx context.Context, msg Run) Ran {
	if !validImage.MatchString(msg.Image) {
		return Ran{Err: errors.New("configure a trusted, pre-provisioned Docker image")}
	}
	if msg.Workspace == "" || msg.Command == "" {
		return Ran{Err: errors.New("Docker workspace and command are required")}
	}
	maxLog, maxOutput := msg.MaxLog, msg.MaxOutput
	if maxLog <= 0 || maxLog > 64<<10 {
		maxLog = 64 << 10
	}
	if maxOutput <= 0 || maxOutput > 129<<20 {
		maxOutput = 129 << 20
	}
	name, err := containerName()
	if err != nil {
		return Ran{Err: err}
	}
	cmd := dockerCommand(ctx, runArgs(name, msg)...)
	cmd.Cancel = func() error {
		removeContainer(name)
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 10 * time.Second
	stdout, stderr := &limitedBuffer{limit: maxOutput}, &limitedBuffer{limit: maxLog}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if err == nil && stdout.truncated {
		err = errors.New("Docker output stream exceeds the configured limit")
	}
	return Ran{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Err: err}
}

// runArgs are the arguments of "docker run" for msg. The isolation flags are
// the same for every job: no network, read-only root, no capabilities, and
// capped memory, processes and CPUs.
func runArgs(name string, msg Run) []string {
	return []string{"run", "--rm", "--name=" + name, "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128", "--memory=2g", "--cpus=2", "--tmpfs=/tmp:rw,noexec,nosuid,size=256m", "--tmpfs=/workspace:rw,exec,nosuid,size=1g", "--mount", "type=bind,source=" + msg.Workspace + ",target=/src,readonly", "--workdir=/workspace", "--entrypoint=sh", msg.Image, "-c", msg.Command}
}

func containerName() (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return "janus-run-" + hex.EncodeToString(suffix[:]), nil
}

// removeContainer stops and removes the named container. Killing the "docker
// run" client alone would leave the container running.
func removeContainer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = dockerCommand(ctx, "rm", "--force", name).Run()
}

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
