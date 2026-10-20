package storetest

import (
	"testing"

	"janus/internal/app"
)

func TestMemoryPassesTheStoreContract(t *testing.T) {
	Run(t, func(*testing.T) app.Store { return NewMemory() })
}
