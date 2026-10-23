package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// defaultRespawnDelay paces restarts after the program exits on its own, so one
// that fails at startup does not spin.
const defaultRespawnDelay = 5 * time.Second

// runner keeps one program running: it starts it, waits, and starts it again
// after it exits, until the context is done.
type runner struct {
	exe          string
	args         []string
	dir          string
	env          []string
	out          io.Writer
	respawnDelay time.Duration
}

// run blocks until ctx is done, then stops the program. It returns nil on a
// clean stop.
func (r *runner) run(ctx context.Context) error {
	for {
		cmd := exec.CommandContext(ctx, r.exe, r.args...)
		cmd.Dir = r.dir
		cmd.Env = append(os.Environ(), r.env...)
		cmd.Stdout = r.out
		cmd.Stderr = r.out

		start := time.Now()
		err := cmd.Start()
		if err == nil {
			log.Printf("started %s (pid %d)", r.exe, cmd.Process.Pid)
			err = cmd.Wait()
		}
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("%s exited after %s: %v; restarting in %s", r.exe, time.Since(start).Round(time.Second), err, r.respawnDelay)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(r.respawnDelay):
		}
	}
}

// loadEnvFile reads KEY=VALUE lines. Blank lines and lines starting with # are
// skipped, and one pair of surrounding quotes is removed from a value.
func loadEnvFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var env []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s: line %q is not KEY=VALUE", path, line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		env = append(env, key+"="+value)
	}
	return env, scanner.Err()
}
