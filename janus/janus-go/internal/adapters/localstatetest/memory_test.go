package localstatetest

import (
	"testing"

	"janus/internal/app"
)

func TestMemoryPassesTheLocalStateContract(t *testing.T) {
	Run(t, func(*testing.T) Subject {
		memory := NewMemory()
		return Subject{State: memory, Reopen: func(*testing.T) app.LocalState { return memory.Reopen() }}
	})
}
