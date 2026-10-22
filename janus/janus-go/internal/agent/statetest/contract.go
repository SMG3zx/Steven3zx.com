// Package statetest holds an in-memory agent.LocalState and the contract
// suite every agent.LocalState adapter, real or test, must pass.
package statetest

import (
	"bytes"
	"context"
	"janus/internal/domain/agent"
	"testing"
	"time"

	domainfleet "janus/internal/domain/fleet"
)

// Subject is an adapter under test. Reopen returns a second LocalState over the
// same storage, as a restarted Agent or the Agent's command line would open it.
type Subject struct {
	State  agent.LocalState
	Reopen func(t *testing.T) agent.LocalState
}

// Run runs the LocalState contract against subjects made by newSubject. Each
// case gets fresh, empty storage.
func Run(t *testing.T, newSubject func(t *testing.T) Subject) {
	t.Helper()
	for _, c := range []struct {
		name string
		test func(t *testing.T, s Subject)
	}{
		{"StartsEmptyAndUnpaused", testEmpty},
		{"KeepsDeploymentsInIDOrder", testKeeps},
		{"SavingAgainReplaces", testReplaces},
		{"DeletesAndForgetsMissingOnes", testDeletes},
		{"KeepsThePause", testPause},
		{"SurvivesAReopen", testReopen},
		{"RefusesAnIDThatIsNotAName", testBadID},
	} {
		t.Run(c.name, func(t *testing.T) { c.test(t, newSubject(t)) })
	}
}

var ctx = context.Background()

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func deployment(id string, module string) domainfleet.HostedDeployment {
	return domainfleet.HostedDeployment{ID: id, SHA256: "hash-of-" + module, Module: []byte(module),
		Limits: agent.RunLimits{Fuel: 1234, MemoryBytes: 5678, Timeout: time.Second}}
}

func deployments(t *testing.T, s agent.LocalState) []domainfleet.HostedDeployment {
	t.Helper()
	got, err := s.Deployments(ctx)
	must(t, err)
	return got
}

func same(a, b domainfleet.HostedDeployment) bool {
	return a.ID == b.ID && a.SHA256 == b.SHA256 && bytes.Equal(a.Module, b.Module) &&
		a.Limits.Fuel == b.Limits.Fuel && a.Limits.MemoryBytes == b.Limits.MemoryBytes
}

func testEmpty(t *testing.T, s Subject) {
	if got := deployments(t, s.State); len(got) != 0 {
		t.Fatalf("new state has %d deployments", len(got))
	}
	paused, err := s.State.Paused(ctx)
	must(t, err)
	if paused {
		t.Fatal("new state is paused")
	}
}

func testKeeps(t *testing.T, s Subject) {
	b, a := deployment("b", "module b"), deployment("a", "module a")
	must(t, s.State.SaveDeployment(ctx, b))
	must(t, s.State.SaveDeployment(ctx, a))
	got := deployments(t, s.State)
	if len(got) != 2 || !same(got[0], a) || !same(got[1], b) {
		t.Fatalf("got %+v", got)
	}
}

func testReplaces(t *testing.T, s Subject) {
	must(t, s.State.SaveDeployment(ctx, deployment("a", "old")))
	must(t, s.State.SaveDeployment(ctx, deployment("a", "new")))
	got := deployments(t, s.State)
	if len(got) != 1 || !same(got[0], deployment("a", "new")) {
		t.Fatalf("got %+v", got)
	}
}

func testDeletes(t *testing.T, s Subject) {
	must(t, s.State.SaveDeployment(ctx, deployment("a", "module a")))
	must(t, s.State.SaveDeployment(ctx, deployment("b", "module b")))
	must(t, s.State.DeleteDeployment(ctx, "a"))
	must(t, s.State.DeleteDeployment(ctx, "never-saved"))
	got := deployments(t, s.State)
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("got %+v", got)
	}
}

func testPause(t *testing.T, s Subject) {
	for _, want := range []bool{true, true, false} {
		must(t, s.State.SetPaused(ctx, want))
		paused, err := s.State.Paused(ctx)
		must(t, err)
		if paused != want {
			t.Fatalf("paused = %v, want %v", paused, want)
		}
	}
}

func testReopen(t *testing.T, s Subject) {
	must(t, s.State.SaveDeployment(ctx, deployment("a", "module a")))
	must(t, s.State.SetPaused(ctx, true))
	other := s.Reopen(t)
	got := deployments(t, other)
	if len(got) != 1 || !same(got[0], deployment("a", "module a")) {
		t.Fatalf("reopened state has %+v", got)
	}
	paused, err := other.Paused(ctx)
	must(t, err)
	if !paused {
		t.Fatal("reopened state is not paused")
	}
	// A pause written through one handle is read through the other.
	must(t, other.SetPaused(ctx, false))
	paused, err = s.State.Paused(ctx)
	must(t, err)
	if paused {
		t.Fatal("a resume through a second handle was not seen")
	}
}

func testBadID(t *testing.T, s Subject) {
	for _, id := range []string{"", "../escape", "a/b", `a\b`, ".", ".."} {
		if err := s.State.SaveDeployment(ctx, deployment(id, "module")); err == nil {
			t.Errorf("saved a deployment with ID %q", id)
		}
	}
}
