package statetest

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"janus/internal/domain/agent"
	domainfleet "janus/internal/domain/fleet"
)

// Memory is an agent.LocalState kept in maps. Handles made by Reopen share it, as
// two processes share a directory. It is safe for concurrent use.
type Memory struct{ data *memoryData }

type memoryData struct {
	mu          sync.Mutex
	paused      bool
	deployments map[string]domainfleet.HostedDeployment
}

var _ agent.LocalState = Memory{}

// NewMemory returns an empty, unpaused Memory.
func NewMemory() Memory {
	return Memory{data: &memoryData{deployments: map[string]domainfleet.HostedDeployment{}}}
}

// Reopen returns another handle on the same data.
func (m Memory) Reopen() Memory { return Memory{data: m.data} }

// ValidID reports whether id can name a Deployment on disk: not empty, not a
// dot name, and without path separators. Every LocalState refuses other IDs.
func ValidID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\:`)
}

func (m Memory) SaveDeployment(_ context.Context, deployment domainfleet.HostedDeployment) error {
	if !ValidID(deployment.ID) {
		return errors.New("a Deployment ID cannot be empty or contain a path")
	}
	deployment.Module = bytes.Clone(deployment.Module)
	m.data.mu.Lock()
	defer m.data.mu.Unlock()
	m.data.deployments[deployment.ID] = deployment
	return nil
}

func (m Memory) DeleteDeployment(_ context.Context, id string) error {
	m.data.mu.Lock()
	defer m.data.mu.Unlock()
	delete(m.data.deployments, id)
	return nil
}

func (m Memory) Deployments(context.Context) ([]domainfleet.HostedDeployment, error) {
	m.data.mu.Lock()
	defer m.data.mu.Unlock()
	out := make([]domainfleet.HostedDeployment, 0, len(m.data.deployments))
	for _, deployment := range m.data.deployments {
		deployment.Module = bytes.Clone(deployment.Module)
		out = append(out, deployment)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m Memory) SetPaused(_ context.Context, paused bool) error {
	m.data.mu.Lock()
	defer m.data.mu.Unlock()
	m.data.paused = paused
	return nil
}

func (m Memory) Paused(context.Context) (bool, error) {
	m.data.mu.Lock()
	defer m.data.mu.Unlock()
	return m.data.paused, nil
}
