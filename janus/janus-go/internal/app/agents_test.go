package app_test

import (
	"context"
	"testing"
	"time"

	"janus/internal/adapters/audittest"
	"janus/internal/adapters/catalogtest"
	"janus/internal/adapters/identitytest"
	"janus/internal/adapters/storetest"
	"janus/internal/adapters/tailnettest"
	"janus/internal/app"
)

// agentEnv is the Agents use case wired to test adapters, with a clock the
// test moves.
type agentEnv struct {
	agents     *app.Agents
	store      *storetest.Memory
	identities *identitytest.Memory
	catalog    *catalogtest.Memory
	tailnet    *tailnettest.Fake
	audit      *audittest.Recorder
	now        time.Time
}

func newAgentEnv(t *testing.T, owners ...string) *agentEnv {
	t.Helper()
	env := &agentEnv{
		store: storetest.NewMemory(), tailnet: tailnettest.NewFake(), audit: audittest.NewRecorder(),
		now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	}
	env.identities = users(t, owners...)
	env.catalog = catalogtest.NewMemory()
	for _, id := range owners {
		must(t, env.store.AddOwner(context.Background(), id))
	}
	env.tailnet.Now = func() time.Time { return env.now }
	env.agents = app.NewAgents(env.store, env.tailnet, env.audit, "https://tailnet.example.test", func() time.Time { return env.now })
	return env
}

// enroll runs the whole enrollment for owner: a code, an enrollment, and the
// Agent joining the tailnet with the key it was given.
func (e *agentEnv) enroll(t *testing.T, owner, name string) app.Enrollment {
	t.Helper()
	ctx := context.Background()
	code, err := e.agents.IssueCode(ctx, owner)
	must(t, err)
	enrollment, err := e.agents.Enroll(ctx, code.Code, name)
	must(t, err)
	if _, err = e.tailnet.Join(enrollment.AuthKey, name); err != nil {
		t.Fatalf("the Agent could not join the tailnet with its key: %v", err)
	}
	return enrollment
}

func TestAnAgentEnrollsWithACodeAndShowsOnlineUnderItsOwnersTag(t *testing.T) {
	env := newAgentEnv(t, "alice")
	enrollment := env.enroll(t, "alice", "laptop")

	if enrollment.Tag != "tag:acct-alice" || enrollment.ControlURL != "https://tailnet.example.test" || enrollment.AgentID == "" {
		t.Fatalf("enrollment = %#v, want the owner's tag, the control URL and an Agent ID", enrollment)
	}
	agents, err := env.agents.List(context.Background(), "alice")
	must(t, err)
	if len(agents) != 1 {
		t.Fatalf("alice's Agents = %#v, want one", agents)
	}
	got := agents[0]
	if got.ID != enrollment.AgentID || got.Name != "laptop" || got.Tag != "tag:acct-alice" || got.Status != app.AgentOnline {
		t.Fatalf("listed Agent = %#v, want laptop, online, under alice's tag", got)
	}
}
