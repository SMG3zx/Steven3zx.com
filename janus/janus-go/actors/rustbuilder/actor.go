package rustbuilder

import (
	"os"
	"path/filepath"
	"strings"

	"janus/actors/wasmbuilder"

	"github.com/tochemey/goakt/v4/actor"
)

// Actor runs Rust WebAssembly builds in its configured builder image.
type Actor struct {
	image  string
	docker *actor.PID
}

// New creates a Rust builder that uses image through dockerPID.
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
	if m.Target != "wasm32-unknown-unknown" && m.Target != "wasm32-wasip1" {
		ctx.Response(&wasmbuilder.Built{Err: os.ErrInvalid})
		return
	}
	if _, err := os.Stat(filepath.Join(m.Workspace, "Cargo.toml")); err != nil {
		ctx.Response(&wasmbuilder.Built{Err: err})
		return
	}
	command := "cp -a /src/. /workspace/ && cargo build --locked --offline --release --target " + m.Target + " >&2 && cd /workspace/target/" + m.Target + "/release && find . -type f -name '*.wasm' -print0 | tar --null -T - -cf -"
	r, err := wasmbuilder.RunDocker(ctx.Context(), a.docker, a.image, m.Workspace, command)
	ctx.Response(&wasmbuilder.Built{Name: r.Name, Artifact: r.Artifact, Log: r.Log, Err: err})
}
func (*Actor) PostStop(*actor.Context) error { return nil }
