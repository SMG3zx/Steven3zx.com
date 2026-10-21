package natstest

import (
	"context"
	"net"
	"testing"
	"time"

	"janus/internal/platform/natsx"
)

// StartWithLeaf is Start for a test that needs leaf connections: the server also
// takes leaf connections on a free loopback port, whose address it returns.
func StartWithLeaf(t testing.TB) (*natsx.Hub, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	leafAddress := listener.Addr().String()
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hub, err := natsx.Start(ctx, natsx.Config{StoreDir: t.TempDir(), Name: t.Name(), LeafAddress: leafAddress})
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
	return hub, leafAddress
}
