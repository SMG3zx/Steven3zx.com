package wasmruntime

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	actor "github.com/tochemey/goakt/v4/actor"
	"janus/actors/persistence"
	"janus/internal/app"
	artifactpkg "janus/internal/domain/artifact"
	"janus/internal/domain/templates"
)

type Actor struct {
	store    app.Store
	runner   app.Runner
	runs     map[string]Run
	slots    chan struct{}
	draining bool
}

var _ actor.Actor = (*Actor)(nil)

// New returns the runtime actor. It reads builds from store and runs their
// modules with runner.
func New(store app.Store, runner app.Runner) *Actor {
	return &Actor{store: store, runner: runner, runs: map[string]Run{}, slots: make(chan struct{}, 2)}
}
func (*Actor) PreStart(*actor.Context) error { return nil }

func (a *Actor) Receive(ctx *actor.ReceiveContext) {
	switch m := ctx.Message().(type) {
	case *RequestRun:
		ctx.Response(a.request(ctx, m))
	case *GetRun:
		r, ok := a.runs[m.RunID]
		if ok && r.ownerID == m.OwnerID {
			r.ownerID = ""
			ctx.Response(&RunFound{Run: r, Found: true})
		} else {
			ctx.Response(&RunFound{})
		}
	case *completed:
		r := a.runs[m.ID]
		r.UpdatedAt = time.Now().UTC()
		r.Stdout = string(m.Stdout)
		r.Stderr = string(m.Stderr)
		r.Module = m.Module
		r.Usage = m.Usage
		if m.Err != nil {
			r.Status = "failed"
			r.Error = m.Err.Error()
		} else {
			r.Status = "complete"
		}
		a.runs[m.ID] = r
		<-a.slots
	case *Drain:
		a.draining = true
		// Active runs have already loaded their artifact and do not use persistence.
		// Let them finish while the application closes unrelated services.
		ctx.Response(&Drained{})
	default:
		ctx.Unhandled()
	}
}

func (a *Actor) request(ctx *actor.ReceiveContext, m *RequestRun) *RunRequested {
	if a.draining {
		return &RunRequested{Err: errors.New("WASM runtime is shutting down")}
	}
	if !Available() {
		return &RunRequested{Err: errors.New("Wasmtime runtime unavailable: enable CGO and a supported native toolchain")}
	}
	if len(m.Args) > 64 {
		return &RunRequested{Err: errors.New("at most 64 WASI arguments are allowed")}
	}
	for _, arg := range m.Args {
		if len(arg) > 4096 {
			return &RunRequested{Err: errors.New("WASI argument exceeds 4096 bytes")}
		}
	}
	select {
	case a.slots <- struct{}{}:
	default:
		return &RunRequested{Err: errors.New("two WASM runs are already active")}
	}
	build, found, err := a.store.FindWASMBuild(ctx.Context(), m.BuildID, m.OwnerID)
	if err != nil && !persistence.IsReply(err) {
		<-a.slots
		return &RunRequested{Err: err}
	}
	if err != nil || !found {
		<-a.slots
		return &RunRequested{Err: errors.New("completed WASM build not found")}
	}
	if build.Status != "complete" {
		<-a.slots
		return &RunRequested{Err: errors.New("WASM build is not complete")}
	}
	if build.Target != "wasm32-wasip1" && build.Target != "wasip1-wasm" {
		<-a.slots
		return &RunRequested{Err: errors.New("only WASI WebAssembly targets can run")}
	}
	stored, err := a.store.FindWASMBuildArtifact(ctx.Context(), m.BuildID, m.OwnerID)
	if err != nil && !persistence.IsReply(err) {
		<-a.slots
		return &RunRequested{Err: err}
	}
	if err != nil || !stored.Found {
		<-a.slots
		return &RunRequested{Err: errors.New("WASM artifact not found")}
	}
	limits := a.limitsFor(ctx.Context(), m.OwnerID, build)
	id := uuid.NewString()
	if len(a.runs) >= 100 {
		var oldest string
		var when time.Time
		for key, value := range a.runs {
			if oldest == "" || value.CreatedAt.Before(when) {
				oldest, when = key, value.CreatedAt
			}
		}
		delete(a.runs, oldest)
	}
	now := time.Now().UTC()
	run := Run{ID: id, BuildID: m.BuildID, Status: "running", CreatedAt: now, UpdatedAt: now, ownerID: m.OwnerID}
	a.runs[id] = run
	// Copy request data before returning to the HTTP caller.
	data := append([]byte(nil), stored.Data...)
	args := append([]string(nil), m.Args...)
	self := ctx.Self()
	go func() {
		module, name, e := artifactpkg.Module(data, stored.Name)
		var out, stderr []byte
		var usage Usage
		if e == nil {
			var result app.RunResult
			result, e = a.runner.Run(context.Background(), module, args, limits)
			out, stderr, usage = result.Stdout, result.Stderr, result.Usage
		}
		_ = actor.Tell(context.Background(), self, &completed{ID: id, Module: name, Stdout: out, Stderr: stderr, Usage: usage, Err: e})
	}()
	return &RunRequested{RunID: id}
}

// limitsFor returns the resource limits of the template that built build, or
// the defaults when its Snapshot cannot be found.
func (a *Actor) limitsFor(ctx context.Context, ownerID string, build persistence.WASMBuild) templates.Resources {
	limits := templates.Resources{Fuel: templates.DefaultFuel, MemoryBytes: templates.DefaultMemoryBytes, Timeout: templates.DefaultTimeout}
	snapshots, err := a.store.ListSnapshots(ctx, ownerID, build.ProjectID)
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

func (*Actor) PostStop(*actor.Context) error { return nil }
