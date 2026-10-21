package identitytest

import (
	"testing"

	"janus/internal/app"
)

func TestMemoryPassesTheIdentitiesContract(t *testing.T) {
	Run(t, func(*testing.T) app.Identities { return NewMemory() })
}
