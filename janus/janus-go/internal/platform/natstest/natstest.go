// Package natstest starts an embedded JetStream server for a test.
package natstest

import (
	"context"
	"testing"
	"time"

	"janus/internal/platform/natsx"
)

// Start runs a JetStream server in the test's process, on a free port with its
// store in a temporary directory, and returns the connection to it. Both are
// stopped when the test ends.
func Start(t testing.TB) *natsx.Hub {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hub, err := natsx.Start(ctx, natsx.Config{StoreDir: t.TempDir(), Name: t.Name()})
	if err != nil {
		t.Fatalf("start NATS: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := hub.Close(ctx); err != nil {
			t.Errorf("close NATS: %v", err)
		}
	})
	return hub
}
