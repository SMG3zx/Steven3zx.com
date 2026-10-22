// Package disk is the agent.LocalState an Agent keeps in a directory on its
// machine: one module file and one description file per Deployment, and a file
// that is present while the Agent is paused.
package disk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"janus/internal/domain/agent"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	domainfleet "janus/internal/domain/fleet"
)

const pausedFile = "paused"

// Disk is an agent.LocalState in a directory. Every write goes to a temporary
// file that is then renamed into place, so a crash leaves either the old or the
// new file. Two Disks, even in two processes, may share a directory.
type Disk struct {
	dir string
	// mu orders this process's writes; another process's writes are ordered by
	// the renames.
	mu sync.Mutex
}

var _ agent.LocalState = (*Disk)(nil)

// Open returns a Disk over dir, creating it if it does not exist.
func Open(dir string) (*Disk, error) {
	if err := os.MkdirAll(filepath.Join(dir, "deployments"), 0o700); err != nil {
		return nil, fmt.Errorf("create the Agent's state directory: %w", err)
	}
	return &Disk{dir: dir}, nil
}

// description is what the JSON file of a Deployment holds.
type description struct {
	ID          string `json:"id"`
	SHA256      string `json:"sha256"`
	Fuel        uint64 `json:"fuel"`
	MemoryBytes int64  `json:"memory_bytes"`
}

func validID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\:`)
}

func (d *Disk) path(id, ext string) string { return filepath.Join(d.dir, "deployments", id+ext) }

func (d *Disk) SaveDeployment(_ context.Context, deployment domainfleet.HostedDeployment) error {
	if !validID(deployment.ID) {
		return errors.New("a Deployment ID cannot be empty or contain a path")
	}
	desc, err := json.Marshal(description{ID: deployment.ID, SHA256: deployment.SHA256,
		Fuel: deployment.Limits.Fuel, MemoryBytes: deployment.Limits.MemoryBytes})
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	// The description is written last: a Deployment exists once it is there.
	if err = writeFile(d.path(deployment.ID, ".wasm"), deployment.Module); err != nil {
		return err
	}
	return writeFile(d.path(deployment.ID, ".json"), desc)
}

func (d *Disk) DeleteDeployment(_ context.Context, id string) error {
	if !validID(id) {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ext := range []string{".json", ".wasm"} {
		if err := os.Remove(d.path(id, ext)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (d *Disk) Deployments(context.Context) ([]domainfleet.HostedDeployment, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	names, err := filepath.Glob(filepath.Join(d.dir, "deployments", "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]domainfleet.HostedDeployment, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		var desc description
		if err = json.Unmarshal(data, &desc); err != nil {
			return nil, fmt.Errorf("read %s: %w", filepath.Base(name), err)
		}
		module, err := os.ReadFile(d.path(desc.ID, ".wasm"))
		if err != nil {
			return nil, err
		}
		out = append(out, domainfleet.HostedDeployment{ID: desc.ID, SHA256: desc.SHA256, Module: module,
			Limits: agent.RunLimits{Fuel: desc.Fuel, MemoryBytes: desc.MemoryBytes}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (d *Disk) SetPaused(_ context.Context, paused bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	path := filepath.Join(d.dir, pausedFile)
	if paused {
		return writeFile(path, []byte("paused\n"))
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (d *Disk) Paused(context.Context) (bool, error) {
	_, err := os.Stat(filepath.Join(d.dir, pausedFile))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	}
	return false, err
}

// writeFile writes data to a temporary file next to path and renames it there.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
