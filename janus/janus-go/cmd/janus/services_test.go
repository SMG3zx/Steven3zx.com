package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestTheComponentTableHasTheDocumentedGroupsInStartOrder(t *testing.T) {
	var names []string
	for _, g := range newApplication(nil, nil).groups() {
		names = append(names, g.name)
		if len(g.components) == 0 {
			t.Errorf("group %q has no components", g.name)
		}
	}
	want := []string{"foundation", "identity", "projects and builds", "runtime", "web"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("groups = %v, want %v", names, want)
	}
}

func TestNATSStartsAfterTelemetryAndBeforeTheStore(t *testing.T) {
	var names []string
	for _, c := range newApplication(nil, nil).groups()[0].components {
		names = append(names, c.name)
	}
	want := []string{"audit", "telemetry", "nats", "store"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("foundation = %v, want %v", names, want)
	}
}

func TestTheNATSComponentEmbedsAServerAndStopsIt(t *testing.T) {
	a := newApplication(nil, nil)
	a.settings = settings{NATSAddress: "127.0.0.1:0", NATSDir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.startNATS(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.nats.Embedded() || !a.nats.Conn().IsConnected() {
		t.Fatalf("NATS embedded = %v, connected = %v, want both", a.nats.Embedded(), a.nats.Conn().IsConnected())
	}
	if err := a.stopNATS(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.nats.Conn().IsClosed() {
		t.Fatal("the connection is still open after stop")
	}
}
