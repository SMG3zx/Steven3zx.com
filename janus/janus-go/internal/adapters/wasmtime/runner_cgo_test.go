//go:build cgo

package wasmtime

import (
	"testing"

	"janus/internal/adapters/runnertest"
	"janus/internal/app"
)

func TestWasmtimePassesTheRunnerContract(t *testing.T) {
	runnertest.Run(t, func(*testing.T) app.Runner { return Wasmtime{} })
}
