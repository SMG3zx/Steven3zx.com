package localstatetest

import (
	"testing"

	"janus/internal/domain/agent"
)

func TestMemoryPassesTheLocalStateContract(t *testing.T) {
	Run(t, func(*testing.T) Subject {
		memory := NewMemory()
		return Subject{State: memory, Reopen: func(*testing.T) agent.LocalState { return memory.Reopen() }}
	})
}
