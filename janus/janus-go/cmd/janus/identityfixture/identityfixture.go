// Package identityfixture runs the identity service in one test process: an
// embedded NATS server and the service on a temporary identity.db. Tests reach
// it through the gateway's client of the service, so they cover the NATS path.
package identityfixture

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"janus/internal/platform/natstest"
	"janus/internal/services/gateway/identityclient"
	"janus/internal/services/identity"
)

// Secret is the key identity encrypts GitHub tokens under in the fixture.
const Secret = "identity-fixture-secret-that-is-long-enough"

// Start runs the identity service on a new embedded NATS server and returns the
// gateway's client of it. The service stops when the test ends.
func Start(t testing.TB) *identityclient.Gateway {
	t.Helper()
	hub := natstest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	service := identity.New(identity.Config{DatabasePath: filepath.Join(t.TempDir(), "identity.db"), TokenSecret: Secret})
	if err := service.Start(ctx, hub.Conn()); err != nil {
		t.Fatalf("start the identity service: %v", err)
	}
	t.Cleanup(func() { _ = service.Stop(context.Background()) })
	return identityclient.NewGateway(hub.Conn())
}
