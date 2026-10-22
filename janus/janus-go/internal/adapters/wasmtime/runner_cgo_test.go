//go:build cgo

package wasmtime

import (
	"testing"

	"janus/internal/adapters/runnertest"
	"janus/internal/domain/agent"
)

func TestWasmtimePassesTheRunnerContract(t *testing.T) {
	runnertest.Run(t, func(*testing.T) agent.Runner { return Wasmtime{} })
}
