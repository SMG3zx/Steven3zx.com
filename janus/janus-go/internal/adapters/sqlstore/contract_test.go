package sqlstore

import (
	"path/filepath"
	"testing"

	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

func TestStorePassesTheStoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) app.Store {
		return openTest(t, filepath.Join(t.TempDir(), "store.db"))
	})
}
