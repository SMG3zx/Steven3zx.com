// Package vm owns the lifecycle of microVMs: it persists their definitions,
// launches one Firecracker process per running VM, and tracks their state.
package vm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"firecracker-manager/internal/firecracker"
)

const (
	// bootTimeout bounds how long we wait for a new Firecracker process to
	// expose its API socket and accept the boot configuration.
	bootTimeout = 10 * time.Second
	// gracePeriod is how long a guest gets to shut down after Ctrl-Alt-Del
	// before the Firecracker process is killed.
	gracePeriod = 10 * time.Second
	// maxSocketPath is the Linux limit for a Unix socket path (sun_path).
	maxSocketPath = 107
)

// State is the runtime state of a VM. It is not persisted.
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StatePaused   State = "paused"
	StateStopping State = "stopping"
	StateError    State = "error"
)

// VM is a point-in-time view of a microVM, safe to serialize.
type VM struct {
	ID        string     `json:"id"`
	Spec      Spec       `json:"spec"`
	State     State      `json:"state"`
	PID       int        `json:"pid,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	LastError string     `json:"lastError,omitempty"`
}

// Options configures a Manager.
type Options struct {
	// DataDir holds the VM list (vms.json) and one directory per VM.
	DataDir string
	// FirecrackerBin is the firecracker executable to run for each VM.
	FirecrackerBin string
}

// entry is the in-memory state for one VM. All fields are guarded by Manager.mu.
type entry struct {
	rec       record
	state     State
	pid       int
	startedAt *time.Time
	lastErr   string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	exited    chan struct{}
}

// Manager is safe for concurrent use.
type Manager struct {
	mu        sync.Mutex
	opts      Options
	storePath string
	vms       map[string]*entry
}

// NewManager loads persisted VMs from opts.DataDir. Every VM starts stopped:
// processes from a previous manager run are not adopted.
func NewManager(opts Options) (*Manager, error) {
	if opts.DataDir == "" {
		return nil, errors.New("data dir is required")
	}
	if opts.FirecrackerBin == "" {
		opts.FirecrackerBin = "firecracker"
	}
	dataDir, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return nil, err
	}
	opts.DataDir = dataDir
	if err := os.MkdirAll(filepath.Join(dataDir, "vms"), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	probe := filepath.Join(dataDir, "vms", strings.Repeat("0", 16), "api.sock")
	if len(probe) > maxSocketPath {
		return nil, fmt.Errorf("data dir %s is too long: API socket path would exceed %d bytes", dataDir, maxSocketPath)
	}

	m := &Manager{
		opts:      opts,
		storePath: filepath.Join(dataDir, "vms.json"),
		vms:       map[string]*entry{},
	}
	recs, err := loadRecords(m.storePath)
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		m.vms[r.ID] = &entry{rec: r, state: StateStopped}
	}
	return m, nil
}

// List returns all VMs, oldest first.
func (m *Manager) List() []VM {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]VM, 0, len(m.vms))
	for _, e := range m.vms {
		out = append(out, e.view())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Get returns one VM by ID.
func (m *Manager) Get(id string) (VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.lookupLocked(id)
	if err != nil {
		return VM{}, err
	}
	return e.view(), nil
}

// Create validates and persists a new stopped VM.
func (m *Manager) Create(spec Spec) (VM, error) {
	spec.applyDefaults()
	if err := spec.validate(); err != nil {
		return VM{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.vms {
		if e.rec.Spec.Name == spec.Name {
			return VM{}, fmt.Errorf("%w: a VM named %q already exists", ErrConflict, spec.Name)
		}
	}
	id, err := newID()
	if err != nil {
		return VM{}, err
	}
	if err := os.MkdirAll(m.dir(id), 0o700); err != nil {
		return VM{}, err
	}
	e := &entry{rec: record{ID: id, CreatedAt: time.Now().UTC(), Spec: spec}, state: StateStopped}
	m.vms[id] = e
	if err := m.saveLocked(); err != nil {
		delete(m.vms, id)
		_ = os.RemoveAll(m.dir(id))
		return VM{}, err
	}
	return e.view(), nil
}

// Delete removes a VM and its directory. The VM must be stopped or in error.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	e, err := m.lookupLocked(id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if e.state != StateStopped && e.state != StateError {
		st := e.state
		m.mu.Unlock()
		return fmt.Errorf("%w: cannot delete a VM that is %s", ErrConflict, st)
	}
	delete(m.vms, id)
	if err := m.saveLocked(); err != nil {
		m.vms[id] = e
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	return os.RemoveAll(m.dir(id))
}

// Start launches the Firecracker process for a VM and boots it.
// It returns once the guest has been started, or the error that prevented that.
func (m *Manager) Start(id string) error {
	m.mu.Lock()
	e, err := m.lookupLocked(id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if e.state != StateStopped && e.state != StateError {
		st := e.state
		m.mu.Unlock()
		return fmt.Errorf("%w: vm is %s", ErrConflict, st)
	}
	e.state = StateStarting
	e.lastErr = ""
	spec := e.rec.Spec
	m.mu.Unlock()

	// Boot happens without holding the lock; the StateStarting marker keeps
	// other operations out in the meantime.
	p, err := m.launch(id, spec)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		e.state = StateError
		e.lastErr = err.Error()
		return err
	}
	exited := make(chan struct{})
	now := time.Now().UTC()
	e.cmd = p.cmd
	e.stdin = p.stdin
	e.pid = p.cmd.Process.Pid
	e.startedAt = &now
	e.exited = exited
	e.state = StateRunning
	go m.watch(e, p, exited)
	return nil
}

// Stop shuts a VM down: Ctrl-Alt-Del first, then SIGKILL after gracePeriod.
// A paused VM is killed directly.
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	e, err := m.lookupLocked(id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if e.state != StateRunning && e.state != StatePaused {
		st := e.state
		m.mu.Unlock()
		return fmt.Errorf("%w: vm is %s", ErrConflict, st)
	}
	graceful := e.state == StateRunning
	e.state = StateStopping
	cmd, exited := e.cmd, e.exited
	m.mu.Unlock()

	if graceful {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = firecracker.NewClient(m.socketPath(id)).Action(ctx, "SendCtrlAltDel")
		cancel()
		select {
		case <-exited:
			return nil
		case <-time.After(gracePeriod):
		}
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill firecracker: %w", err)
	}
	<-exited
	return nil
}

// Pause freezes a running VM's vCPUs.
func (m *Manager) Pause(id string) error {
	return m.setVMState(id, StateRunning, "Paused", StatePaused)
}

// Resume continues a paused VM.
func (m *Manager) Resume(id string) error {
	return m.setVMState(id, StatePaused, "Resumed", StateRunning)
}

// SendInput writes bytes to the VM's serial console (the Firecracker stdin).
func (m *Manager) SendInput(id string, data []byte) error {
	m.mu.Lock()
	e, err := m.lookupLocked(id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	stdin := e.stdin
	st := e.state
	m.mu.Unlock()
	if stdin == nil {
		return fmt.Errorf("%w: vm is %s", ErrConflict, st)
	}
	_, err = stdin.Write(data)
	return err
}

// ConsolePath returns the file that holds the VM's serial console output.
// The file may not exist yet if the VM has never been started.
func (m *Manager) ConsolePath(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.lookupLocked(id); err != nil {
		return "", err
	}
	return m.consolePath(id), nil
}

// StopAll stops every running or paused VM. It is called on manager shutdown.
func (m *Manager) StopAll() error {
	m.mu.Lock()
	var ids []string
	for id, e := range m.vms {
		if e.state == StateRunning || e.state == StatePaused {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()

	var errs []error
	for _, id := range ids {
		if err := m.Stop(id); err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) setVMState(id string, from State, apiState string, to State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.lookupLocked(id)
	if err != nil {
		return err
	}
	if e.state != from {
		return fmt.Errorf("%w: vm is %s", ErrConflict, e.state)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := firecracker.NewClient(m.socketPath(id)).SetVMState(ctx, apiState); err != nil {
		return err
	}
	e.state = to
	return nil
}

// process is a launched and booted Firecracker process.
type process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	log   *os.File
}

// launch starts Firecracker for id and configures and boots the guest.
// On failure the process is killed and reaped before returning.
func (m *Manager) launch(id string, spec Spec) (*process, error) {
	if err := os.MkdirAll(m.dir(id), 0o700); err != nil {
		return nil, err
	}
	sock := m.socketPath(id)
	_ = os.Remove(sock)

	log, err := os.OpenFile(m.consolePath(id), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open console log: %w", err)
	}
	cmd := exec.Command(m.opts.FirecrackerBin, "--api-sock", sock)
	cmd.Stdout = log
	cmd.Stderr = log
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = log.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return nil, fmt.Errorf("start firecracker: %w", err)
	}
	if err := boot(sock, spec); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = log.Close()
		return nil, err
	}
	return &process{cmd: cmd, stdin: stdin, log: log}, nil
}

// boot configures a freshly started Firecracker process and starts the instance.
func boot(sock string, spec Spec) error {
	ctx, cancel := context.WithTimeout(context.Background(), bootTimeout)
	defer cancel()
	if err := firecracker.WaitForSocket(ctx, sock); err != nil {
		return err
	}
	c := firecracker.NewClient(sock)
	steps := []struct {
		name string
		run  func() error
	}{
		{"configure machine", func() error { return c.ConfigureMachine(ctx, spec.VCPUs, spec.MemoryMiB) }},
		{"set boot source", func() error { return c.SetBootSource(ctx, spec.KernelPath, spec.BootArgs) }},
		{"attach rootfs", func() error { return c.SetRootDrive(ctx, "rootfs", spec.RootfsPath) }},
		{"start instance", func() error { return c.Action(ctx, "InstanceStart") }},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	return nil
}

// watch reaps a Firecracker process and records how it ended.
func (m *Manager) watch(e *entry, p *process, exited chan struct{}) {
	err := p.cmd.Wait()
	_ = p.log.Close()

	m.mu.Lock()
	defer m.mu.Unlock()
	e.cmd = nil
	e.stdin = nil
	e.pid = 0
	e.startedAt = nil
	if e.state != StateStopping && err != nil {
		e.state = StateError
		e.lastErr = fmt.Sprintf("firecracker exited unexpectedly: %v", err)
	} else {
		e.state = StateStopped
	}
	close(exited)
}

func (m *Manager) lookupLocked(id string) (*entry, error) {
	e, ok := m.vms[id]
	if !ok {
		return nil, fmt.Errorf("%w: vm %s", ErrNotFound, id)
	}
	return e, nil
}

// saveLocked persists all VM records. Caller must hold m.mu.
func (m *Manager) saveLocked() error {
	recs := make([]record, 0, len(m.vms))
	for _, e := range m.vms {
		recs = append(recs, e.rec)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].CreatedAt.Before(recs[j].CreatedAt) })
	return saveRecords(m.storePath, recs)
}

func (m *Manager) dir(id string) string {
	return filepath.Join(m.opts.DataDir, "vms", id)
}

func (m *Manager) socketPath(id string) string {
	return filepath.Join(m.dir(id), "api.sock")
}

func (m *Manager) consolePath(id string) string {
	return filepath.Join(m.dir(id), "console.log")
}

func (e *entry) view() VM {
	return VM{
		ID:        e.rec.ID,
		Spec:      e.rec.Spec,
		State:     e.state,
		PID:       e.pid,
		CreatedAt: e.rec.CreatedAt,
		StartedAt: e.startedAt,
		LastError: e.lastErr,
	}
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
