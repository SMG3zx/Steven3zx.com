package disk

import (
	"testing"

	"janus/internal/agent/statetest"
	"janus/internal/domain/agent"
)

func TestDiskPassesTheLocalStateContract(t *testing.T) {
	statetest.Run(t, func(t *testing.T) statetest.Subject {
		dir := t.TempDir()
		open := func(t *testing.T) agent.LocalState {
			disk, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			return disk
		}
		return statetest.Subject{State: open(t), Reopen: open}
	})
}
