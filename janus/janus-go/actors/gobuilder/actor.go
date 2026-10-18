package gobuilder

import (
	"os"
	"path/filepath"
	"strings"

	"janus/actors/wasmbuilder"

	"github.com/tochemey/goakt/v4/actor"
)

// Actor runs Go WASI builds in its configured builder image.
type Actor struct {
	image  string
	docker *actor.PID
}

// New creates a Go builder that uses image through dockerPID.
func New(image string, dockerPID *actor.PID) *Actor {
	return &Actor{image: strings.TrimSpace(image), docker: dockerPID}
}
func (*Actor) PreStart(*actor.Context) error { return nil }
func (a *Actor) Receive(ctx *actor.ReceiveContext) {
	m, ok := ctx.Message().(*wasmbuilder.Build)
	if !ok {
		ctx.Unhandled()
		return
	}
	if m.Target != "wasip1-wasm" {
		ctx.Response(&wasmbuilder.Built{Err: os.ErrInvalid})
		return
	}
	if _, err := os.Stat(filepath.Join(m.Workspace, "go.mod")); err != nil {
		ctx.Response(&wasmbuilder.Built{Err: err})
		return
	}
	command := "cp -a /src/. /workspace/ && mkdir -p /workspace/out && cd /workspace && GOOS=wasip1 GOARCH=wasm GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build -trimpath -buildvcs=false -o /workspace/out/main.wasm . >&2 && cd /workspace/out && tar -cf - main.wasm"
	r, err := wasmbuilder.RunDocker(ctx.Context(), a.docker, a.image, m.Workspace, command)
	ctx.Response(&wasmbuilder.Built{Name: r.Name, Artifact: r.Artifact, Log: r.Log, Err: err})
}
func (*Actor) PostStop(*actor.Context) error { return nil }
