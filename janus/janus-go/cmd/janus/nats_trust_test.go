package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/platform/natsx"
)

const keySecret = "0123456789abcdef0123456789abcdef"

// startTrusted starts an embedded server that trusts the keys at keysPath and
// connects the control plane with its own credentials.
func startTrusted(t *testing.T, keysPath string) *natsx.Hub {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	trust, err := openNATSTrust(ctx, keysPath, keySecret, "")
	if err != nil {
		t.Fatal(err)
	}
	hub, err := natsx.Start(ctx, natsx.Config{Address: "127.0.0.1:0", StoreDir: t.TempDir(), Name: "janus-test",
		Trust: trust.trust, ClientOptions: trust.clientOpts})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = hub.Close(closeCtx)
	})
	return hub
}

func TestTheControlPlaneConnectsAndUsesJetStreamWithItsKeys(t *testing.T) {
	hub := startTrusted(t, filepath.Join(t.TempDir(), "keys.db"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "PROBE", Subjects: []string{"janus.probe.>"}}); err != nil {
		t.Fatalf("the control plane cannot create a stream: %v", err)
	}
	sub, err := hub.Conn().SubscribeSync("janus.probe.ping")
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.Conn().Publish("janus.probe.ping", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	msg, err := sub.NextMsg(5 * time.Second)
	if err != nil || string(msg.Data) != "ok" {
		t.Fatalf("publish round trip: %v", err)
	}
}

func TestAClientWithoutKeysIsRefused(t *testing.T) {
	hub := startTrusted(t, filepath.Join(t.TempDir(), "keys.db"))

	if _, err := nats.Connect(hub.URL()); err == nil {
		t.Fatal("a client without credentials connected to the trusted server")
	}
}

func TestTheKeysAreTheSameAfterARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, err := openNATSTrust(ctx, path, keySecret, "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := openNATSTrust(ctx, path, keySecret, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.agents.Pub != again.agents.Pub || first.trust.Operator.Claims.Subject != again.trust.Operator.Claims.Subject {
		t.Fatal("a restart made new trust keys")
	}
}
