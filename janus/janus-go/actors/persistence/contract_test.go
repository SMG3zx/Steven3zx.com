package persistence

import (
	"path/filepath"
	"testing"
	"time"

	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

func TestClientPassesTheStoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) app.Store {
		pid, stop := startPersistence(t, filepath.Join(t.TempDir(), "store.db"))
		t.Cleanup(stop)
		return NewClient(pid, 5*time.Second)
	})
}
