package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// recorder builds components that log their start and stop into one slice.
type recorder struct{ events []string }

func (r *recorder) component(name string) component {
	return component{
		name:  name,
		start: func(context.Context) error { r.events = append(r.events, "start "+name); return nil },
		stop:  func(context.Context) error { r.events = append(r.events, "stop "+name); return nil },
	}
}

func TestStartRunsGroupsInOrderAndStopRunsThemInReverse(t *testing.T) {
	r := &recorder{}
	lc := newLifecycle(
		group{name: "foundation", components: []component{r.component("audit"), r.component("store")}},
		group{name: "web", components: []component{r.component("http")}},
	)
	if err := lc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"start audit", "start store", "start http", "stop http", "stop store", "stop audit"}
	if !reflect.DeepEqual(r.events, want) {
		t.Fatalf("events = %v, want %v", r.events, want)
	}
}

func TestFailedStartStopsOnlyWhatAlreadyStartedAndReturnsTheStartError(t *testing.T) {
	r := &recorder{}
	boom := errors.New("builder image missing")
	failing := r.component("docker")
	failing.start = func(context.Context) error { return boom }
	lc := newLifecycle(
		group{name: "foundation", components: []component{r.component("audit")}},
		group{name: "builds", components: []component{failing, r.component("builds")}},
	)
	err := lc.Start(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Start error = %v, want it to wrap %v", err, boom)
	}
	want := []string{"start audit", "stop audit"}
	if !reflect.DeepEqual(r.events, want) {
		t.Fatalf("events = %v, want %v", r.events, want)
	}
}

func TestAddingAComponentIsOneTableEntry(t *testing.T) {
	r := &recorder{}
	noop := component{name: "noop"}
	lc := newLifecycle(group{name: "foundation", components: []component{r.component("audit"), noop}})
	if err := lc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"start audit", "stop audit"}
	if !reflect.DeepEqual(r.events, want) {
		t.Fatalf("events = %v, want %v", r.events, want)
	}
}

func TestStopKeepsGoingAfterAFailureAndReturnsTheFirstError(t *testing.T) {
	r := &recorder{}
	first := errors.New("drain timed out")
	bad := r.component("builds")
	bad.stop = func(context.Context) error { r.events = append(r.events, "stop builds"); return first }
	other := r.component("store")
	other.stop = func(context.Context) error {
		r.events = append(r.events, "stop store")
		return errors.New("later error")
	}
	lc := newLifecycle(group{name: "g", components: []component{other, bad, r.component("http")}})
	if err := lc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lc.Stop(context.Background()); err != first {
		t.Fatalf("Stop error = %v, want %v", err, first)
	}
	want := []string{"start store", "start builds", "start http", "stop http", "stop builds", "stop store"}
	if !reflect.DeepEqual(r.events, want) {
		t.Fatalf("events = %v, want %v", r.events, want)
	}
}

func TestStartedHookRunsBeforeRollbackWithTheStartResult(t *testing.T) {
	r := &recorder{}
	boom := errors.New("boom")
	failing := component{name: "bad", start: func(context.Context) error { return boom }}
	lc := newLifecycle(group{name: "g", components: []component{r.component("audit"), failing}})
	lc.startDone = func(err error) { r.events = append(r.events, "done: "+err.Error()) }
	if err := lc.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Start error = %v", err)
	}
	want := []string{"start audit", "done: boom", "stop audit"}
	if !reflect.DeepEqual(r.events, want) {
		t.Fatalf("events = %v, want %v", r.events, want)
	}
}
