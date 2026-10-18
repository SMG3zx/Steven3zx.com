package wasmruntime

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	actor "github.com/tochemey/goakt/v4/actor"
	"janus/actors/persistence"
)

const maxArchiveSize = 128 << 20

type Actor struct {
	persistence *actor.PID
	runs        map[string]Run
	slots       chan struct{}
	draining    bool
}

var _ actor.Actor = (*Actor)(nil)

func New(p *actor.PID) *Actor {
	return &Actor{persistence: p, runs: map[string]Run{}, slots: make(chan struct{}, 2)}
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
	resp, err := actor.Ask(ctx.Context(), a.persistence, &persistence.FindWASMBuild{ID: m.BuildID, OwnerID: m.OwnerID}, 5*time.Second)
	if err != nil {
		<-a.slots
		return &RunRequested{Err: err}
	}
	b, ok := resp.(*persistence.WASMBuildFound)
	if !ok || b.Err != nil || !b.Found {
		<-a.slots
		return &RunRequested{Err: errors.New("completed WASM build not found")}
	}
	if b.Build.Status != "complete" {
		<-a.slots
		return &RunRequested{Err: errors.New("WASM build is not complete")}
	}
	if b.Build.Target != "wasm32-wasip1" && b.Build.Target != "wasip1-wasm" {
		<-a.slots
		return &RunRequested{Err: errors.New("only WASI WebAssembly targets can run")}
	}
	ar, err := actor.Ask(ctx.Context(), a.persistence, &persistence.FindWASMBuildArtifact{ID: m.BuildID, OwnerID: m.OwnerID}, 5*time.Second)
	if err != nil {
		<-a.slots
		return &RunRequested{Err: err}
	}
	artifact, ok := ar.(*persistence.WASMBuildArtifact)
	if !ok || artifact.Err != nil || !artifact.Found {
		<-a.slots
		return &RunRequested{Err: errors.New("WASM artifact not found")}
	}
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
	data := append([]byte(nil), artifact.Data...)
	args := append([]string(nil), m.Args...)
	self := ctx.Self()
	go func() {
		module, name, e := extract(data, artifact.Name)
		var out, stderr []byte
		if e == nil {
			out, stderr, e = ExecuteWASI(module, args, 10*time.Second)
		}
		_ = actor.Tell(context.Background(), self, &completed{ID: id, Module: name, Stdout: out, Stderr: stderr, Err: e})
	}()
	return &RunRequested{RunID: id}
}

func extract(data []byte, name string) ([]byte, string, error) {
	if strings.HasSuffix(strings.ToLower(name), ".zst") {
		d, e := zstd.NewReader(bytes.NewReader(data))
		if e != nil {
			return nil, "", e
		}
		defer d.Close()
		data, e = io.ReadAll(io.LimitReader(d, maxArchiveSize+1))
		if e != nil {
			return nil, "", e
		}
		if len(data) > maxArchiveSize {
			return nil, "", errors.New("WASM archive exceeds 128 MiB")
		}
	}
	if len(data) > maxArchiveSize {
		return nil, "", errors.New("WASM archive exceeds 128 MiB")
	}
	zr, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return nil, "", fmt.Errorf("open WASM archive: %w", e)
	}
	files := make([]*zip.File, 0)
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() && strings.HasSuffix(strings.ToLower(f.Name), ".wasm") {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, "", errors.New("artifact contains no WASM module")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	f := files[0]
	if f.UncompressedSize64 > maxArchiveSize {
		return nil, "", errors.New("WASM module exceeds 128 MiB")
	}
	r, e := f.Open()
	if e != nil {
		return nil, "", e
	}
	defer r.Close()
	module, e := io.ReadAll(io.LimitReader(r, maxArchiveSize+1))
	if e != nil {
		return nil, "", e
	}
	if len(module) > maxArchiveSize {
		return nil, "", errors.New("WASM module exceeds 128 MiB")
	}
	return module, f.Name, nil
}

func (*Actor) PostStop(*actor.Context) error { return nil }
