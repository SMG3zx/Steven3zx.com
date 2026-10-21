package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"janus/internal/domain/artifact"
	"janus/internal/domain/templates"
	"janus/internal/domain/usage"
)

const (
	// maxConcurrentRuns is how many WASM Runs may be active at once.
	maxConcurrentRuns = 2
	// maxRunTable is how many Runs are kept; the oldest is evicted for a new one.
	maxRunTable = 100
	// maxRunArgs and maxRunArgBytes bound the WASI arguments of a Run.
	maxRunArgs     = 64
	maxRunArgBytes = 4096
)

// Run is one execution of a WASM Build's module.
type Run struct {
	ID        string      `json:"id"`
	BuildID   string      `json:"build_id"`
	Module    string      `json:"module,omitempty"`
	Status    string      `json:"status"`
	Stdout    string      `json:"stdout,omitempty"`
	Stderr    string      `json:"stderr,omitempty"`
	Error     string      `json:"error,omitempty"`
	Usage     usage.Usage `json:"usage"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	ownerID   string
}

// Runs starts WASM Runs of completed Builds and reports on them. A Run happens
// on a goroutine the use case owns; Drain and Wait wait for those goroutines.
//
// The Run table lives in memory and keeps the newest 100 Runs. At most two Runs
// are active at once.
type Runs struct {
	store  Store
	runner Runner

	active   atomic.Int32
	draining atomic.Bool
	jobs     sync.WaitGroup

	mu   sync.Mutex
	runs map[string]Run
}

// NewRuns returns a Runs that reads Builds from store and runs their modules
// with runner.
func NewRuns(store Store, runner Runner) *Runs {
	return &Runs{store: store, runner: runner, runs: map[string]Run{}}
}

// Request starts a Run of the completed Build buildID, which owner must own, and
// returns its ID. The Run itself happens in the background. Failures are
// plain errors whose text is meant for the Account.
func (r *Runs) Request(ctx context.Context, owner, buildID string, args []string) (string, error) {
	if r.draining.Load() {
		return "", errors.New("WASM runtime is shutting down")
	}
	if available, ok := r.runner.(interface{ Available() bool }); ok && !available.Available() {
		return "", errors.New("Wasmtime runtime unavailable: enable CGO and a supported native toolchain")
	}
	if len(args) > maxRunArgs {
		return "", errors.New("at most 64 WASI arguments are allowed")
	}
	for _, arg := range args {
		if len(arg) > maxRunArgBytes {
			return "", errors.New("WASI argument exceeds 4096 bytes")
		}
	}
	if !r.acquire() {
		return "", errors.New("two WASM runs are already active")
	}
	started := false
	defer func() {
		if !started {
			r.active.Add(-1)
		}
	}()
	build, found, err := r.store.FindWASMBuild(ctx, buildID, owner)
	if err != nil && !IsReply(err) {
		return "", err
	}
	if err != nil || !found {
		return "", errors.New("completed WASM build not found")
	}
	if build.Status != "complete" {
		return "", errors.New("WASM build is not complete")
	}
	if build.Target != "wasm32-wasip1" && build.Target != "wasip1-wasm" {
		return "", errors.New("only WASI WebAssembly targets can run")
	}
	stored, err := r.store.FindWASMBuildArtifact(ctx, buildID, owner)
	if err != nil && !IsReply(err) {
		return "", err
	}
	if err != nil || !stored.Found {
		return "", errors.New("WASM artifact not found")
	}
	limits := r.limitsFor(ctx, owner, build)

	id := uuid.NewString()
	now := time.Now().UTC()
	r.mu.Lock()
	r.evictOldest()
	r.runs[id] = Run{ID: id, BuildID: buildID, Status: "running", CreatedAt: now, UpdatedAt: now, ownerID: owner}
	r.mu.Unlock()

	// Copy request data before returning to the caller.
	data := append([]byte(nil), stored.Data...)
	argv := append([]string(nil), args...)
	started = true
	r.jobs.Add(1)
	go r.execute(id, stored.Name, data, argv, limits)
	return id, nil
}

// Get returns one of owner's Runs.
func (r *Runs) Get(owner, runID string) (Run, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[runID]
	if !ok || run.ownerID != owner {
		return Run{}, false
	}
	run.ownerID = ""
	return run, true
}

// Drain makes Request refuse new Runs and waits for the active ones, or until
// ctx ends.
func (r *Runs) Drain(ctx context.Context) error {
	r.draining.Store(true)
	return r.wait(ctx)
}

// Wait waits for the active Runs without refusing new ones, or until ctx ends.
func (r *Runs) Wait(ctx context.Context) error { return r.wait(ctx) }

func (r *Runs) wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() { r.jobs.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// acquire takes one of the active-Run slots, and reports false when none is free.
func (r *Runs) acquire() bool {
	for {
		n := r.active.Load()
		if n >= maxConcurrentRuns {
			return false
		}
		if r.active.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// evictOldest removes the oldest Run when the table is full. The caller holds mu.
func (r *Runs) evictOldest() {
	if len(r.runs) < maxRunTable {
		return
	}
	var oldest string
	var when time.Time
	for key, value := range r.runs {
		if oldest == "" || value.CreatedAt.Before(when) {
			oldest, when = key, value.CreatedAt
		}
	}
	delete(r.runs, oldest)
}

// execute opens the Artifact, runs its module and records the outcome. A panic
// anywhere in it becomes a failed Run.
func (r *Runs) execute(id, artifactName string, data []byte, args []string, limits RunLimits) {
	defer r.jobs.Done()
	var (
		module string
		result RunResult
		err    error
	)
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("WASM run panicked: %v", recovered)
		}
		r.complete(id, module, result, err)
	}()
	var bytes []byte
	bytes, module, err = artifact.Module(data, artifactName)
	if err != nil {
		return
	}
	result, err = r.runner.Run(context.Background(), bytes, RunInput{Args: args, Limits: limits})
}

func (r *Runs) complete(id, module string, result RunResult, err error) {
	r.mu.Lock()
	run := r.runs[id]
	run.UpdatedAt = time.Now().UTC()
	run.Stdout = string(result.Stdout)
	if result.StdoutTruncated {
		run.Stdout += "\n[output truncated]"
	}
	run.Stderr = string(result.Stderr)
	run.Module = module
	run.Usage = result.Usage
	if err != nil {
		run.Status = "failed"
		run.Error = err.Error()
	} else {
		run.Status = "complete"
	}
	if _, kept := r.runs[id]; kept {
		r.runs[id] = run
	}
	r.mu.Unlock()
	r.active.Add(-1)
}

// limitsFor returns the resource limits of the template that built build, or
// the defaults when its Snapshot cannot be found.
func (r *Runs) limitsFor(ctx context.Context, owner string, build WASMBuild) RunLimits {
	return templateLimits(ctx, r.store, owner, build)
}

// templateLimits returns the resource limits of the template that built build,
// or the defaults when its Snapshot cannot be found.
func templateLimits(ctx context.Context, store Store, owner string, build WASMBuild) RunLimits {
	limits := RunLimits{Fuel: templates.DefaultFuel, MemoryBytes: templates.DefaultMemoryBytes, Timeout: templates.DefaultTimeout}
	snapshots, err := store.ListSnapshots(ctx, owner, build.ProjectID)
	if err != nil {
		return limits
	}
	for _, snapshot := range snapshots {
		if snapshot.ID != build.SnapshotID {
			continue
		}
		if template, found := templates.Find(snapshot.Language, snapshot.Runtime); found {
			return template.Resources
		}
	}
	return limits
}
