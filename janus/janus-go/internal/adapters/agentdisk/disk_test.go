package agentdisk

import (
	"testing"

	"janus/internal/adapters/localstatetest"
	"janus/internal/domain/agent"
)

func TestDiskPassesTheLocalStateContract(t *testing.T) {
	localstatetest.Run(t, func(t *testing.T) localstatetest.Subject {
		dir := t.TempDir()
		open := func(t *testing.T) agent.LocalState {
			disk, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			return disk
		}
		return localstatetest.Subject{State: open(t), Reopen: open}
	})
}
