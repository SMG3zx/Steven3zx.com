package app_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"janus/internal/app"
)

func TestACodeWorksOnceAndOnlyBeforeItExpires(t *testing.T) {
	env := newAgentEnv(t, "alice")
	ctx := context.Background()

	code, err := env.agents.IssueCode(ctx, "alice")
	must(t, err)
	if !code.ExpiresAt.Equal(env.now.Add(15 * time.Minute)) {
		t.Fatalf("code expires at %v, want 15 minutes from now", code.ExpiresAt)
	}
	if _, err = env.agents.Enroll(ctx, "  "+strings.ToUpper(code.Code)+" ", "laptop"); err != nil {
		t.Fatalf("enrolling with the code typed in capitals and spaces = %v, want it accepted", err)
	}
	if _, err = env.agents.Enroll(ctx, code.Code, "again"); !errors.Is(err, app.ErrEnrollmentCodeInvalid) {
		t.Fatalf("enrolling with a used code = %v, want ErrEnrollmentCodeInvalid", err)
	}

	late, err := env.agents.IssueCode(ctx, "alice")
	must(t, err)
	env.now = env.now.Add(15 * time.Minute)
	if _, err = env.agents.Enroll(ctx, late.Code, "late"); !errors.Is(err, app.ErrEnrollmentCodeInvalid) {
		t.Fatalf("enrolling with an expired code = %v, want ErrEnrollmentCodeInvalid", err)
	}
	if _, err = env.agents.Enroll(ctx, "not-a-code", "guess"); !errors.Is(err, app.ErrEnrollmentCodeInvalid) {
		t.Fatalf("enrolling with a guess = %v, want ErrEnrollmentCodeInvalid", err)
	}
	if agents, _ := env.agents.List(ctx, "alice"); len(agents) != 1 {
		t.Fatalf("alice has %d Agents, want only the one that enrolled in time", len(agents))
	}
}

func TestAnAgentNeedsAReasonableName(t *testing.T) {
	env := newAgentEnv(t, "alice")
	ctx := context.Background()
	for _, name := range []string{"", "   ", strings.Repeat("x", 65)} {
		code, err := env.agents.IssueCode(ctx, "alice")
		must(t, err)
		if _, err = env.agents.Enroll(ctx, code.Code, name); !errors.Is(err, app.ErrAgentName) {
			t.Fatalf("enrolling as %q = %v, want ErrAgentName", name, err)
		}
		if _, err = env.agents.Enroll(ctx, code.Code, "ok"); err != nil {
			t.Fatalf("a rejected name used up the code: %v", err)
		}
	}
}

func TestEnrollmentKeyJoinsOnceUnderTheOwnersTag(t *testing.T) {
	env := newAgentEnv(t, "alice", "bob")
	enrollment := env.enroll(t, "alice", "laptop")
	if _, err := env.tailnet.Join(enrollment.AuthKey, "second-machine"); err == nil {
		t.Fatal("a second machine joined with the same key")
	}
	nodes, err := env.tailnet.Nodes(context.Background())
	must(t, err)
	if len(nodes) != 1 || len(nodes[0].Tags) != 1 || nodes[0].Tags[0] != "tag:acct-alice" {
		t.Fatalf("nodes = %#v, want one node tagged for alice", nodes)
	}
}

func TestOnlyTheOwnerSeesAndRemovesAnAgent(t *testing.T) {
	env := newAgentEnv(t, "alice", "bob")
	ctx := context.Background()
	enrollment := env.enroll(t, "alice", "laptop")

	if agents, _ := env.agents.List(ctx, "bob"); len(agents) != 0 {
		t.Fatalf("bob sees alice's Agents: %#v", agents)
	}
	if removed, err := env.agents.Remove(ctx, "bob", enrollment.AgentID); err != nil || removed {
		t.Fatalf("bob removing alice's Agent = %v, %v, want false", removed, err)
	}
	if nodes, _ := env.tailnet.Nodes(ctx); len(nodes) != 1 {
		t.Fatal("bob's attempt removed alice's node")
	}

	if removed, err := env.agents.Remove(ctx, "alice", enrollment.AgentID); err != nil || !removed {
		t.Fatalf("alice removing her Agent = %v, %v, want true", removed, err)
	}
	if nodes, _ := env.tailnet.Nodes(ctx); len(nodes) != 0 {
		t.Fatalf("the node survived its Agent: %#v", nodes)
	}
	if agents, _ := env.agents.List(ctx, "alice"); len(agents) != 0 {
		t.Fatalf("the Agent survived its removal: %#v", agents)
	}
	if removed, err := env.agents.Remove(ctx, "alice", enrollment.AgentID); err != nil || removed {
		t.Fatalf("removing an Agent twice = %v, %v, want false the second time", removed, err)
	}
}

// failingTailnet fails DeleteNode while broken.
type failingTailnet struct {
	app.Tailnet
	broken bool
}

func (f *failingTailnet) DeleteNode(ctx context.Context, nodeID string) error {
	if f.broken {
		return errors.New("tailnet is down")
	}
	return f.Tailnet.DeleteNode(ctx, nodeID)
}

func TestAnAgentIsKeptWhenItsNodeCannotBeRemovedSoRemovingAgainFinishesTheJob(t *testing.T) {
	env := newAgentEnv(t, "alice")
	ctx := context.Background()
	enrollment := env.enroll(t, "alice", "laptop")
	flaky := &failingTailnet{Tailnet: env.tailnet, broken: true}
	agents := app.NewAgents(env.store, flaky, env.audit, "https://tailnet.example.test", app.WithClock(func() time.Time { return env.now }))

	if removed, err := agents.Remove(ctx, "alice", enrollment.AgentID); err == nil || removed {
		t.Fatalf("removing while the tailnet is down = %v, %v, want an error", removed, err)
	}
	if listed, _ := agents.List(ctx, "alice"); len(listed) != 1 {
		t.Fatal("the Agent record was deleted although its node was not removed")
	}
	flaky.broken = false
	if removed, err := agents.Remove(ctx, "alice", enrollment.AgentID); err != nil || !removed {
		t.Fatalf("removing again = %v, %v, want true", removed, err)
	}
	if nodes, _ := env.tailnet.Nodes(ctx); len(nodes) != 0 {
		t.Fatal("the node is still on the tailnet")
	}
}

type brokenTailnet struct{ app.Tailnet }

func (brokenTailnet) Nodes(context.Context) ([]app.TailnetNode, error) {
	return nil, errors.New("tailnet is down")
}

func TestListReportsPendingOnlineOfflineAndUnknown(t *testing.T) {
	env := newAgentEnv(t, "alice")
	ctx := context.Background()
	code, err := env.agents.IssueCode(ctx, "alice")
	must(t, err)
	if _, err = env.agents.Enroll(ctx, code.Code, "pending"); err != nil {
		t.Fatal(err)
	}
	env.enroll(t, "alice", "joined")

	statusOf := func(agents *app.Agents) map[string]string {
		listed, err := agents.List(ctx, "alice")
		must(t, err)
		out := map[string]string{}
		for _, agent := range listed {
			out[agent.Name] = agent.Status
		}
		return out
	}
	if got := statusOf(env.agents); got["pending"] != app.AgentPending || got["joined"] != app.AgentOnline {
		t.Fatalf("statuses = %v, want pending and online", got)
	}
	nodes, _ := env.tailnet.Nodes(ctx)
	env.tailnet.SetOnline(nodes[0].ID, false)
	if got := statusOf(env.agents); got["joined"] != app.AgentOffline {
		t.Fatalf("statuses = %v, want the joined Agent offline", got)
	}
	down := app.NewAgents(env.store, brokenTailnet{}, env.audit, "https://tailnet.example.test")
	if got := statusOf(down); got["pending"] != app.AgentUnknown || got["joined"] != app.AgentUnknown {
		t.Fatalf("statuses with the tailnet down = %v, want unknown", got)
	}
}

func TestAuditEventsNameWhatHappenedAndNeverCarryACodeOrAKey(t *testing.T) {
	env := newAgentEnv(t, "alice")
	ctx := context.Background()
	code, err := env.agents.IssueCode(ctx, "alice")
	must(t, err)
	enrollment, err := env.agents.Enroll(ctx, code.Code, "laptop")
	must(t, err)
	_, _ = env.tailnet.Join(enrollment.AuthKey, "laptop")
	_, _ = env.agents.Enroll(ctx, code.Code, "replay")
	_, err = env.agents.Remove(ctx, "alice", enrollment.AgentID)
	must(t, err)

	var names []string
	for _, event := range env.audit.Events() {
		names = append(names, event.Name)
		text := event.Name + " " + fmt.Sprint(event.Fields)
		if strings.Contains(text, code.Code) || strings.Contains(text, enrollment.AuthKey) {
			t.Fatalf("audit event %q carries a code or a key: %v", event.Name, event.Fields)
		}
	}
	want := []string{"agent.code.issued", "agent.enrolled", "agent.enroll.rejected", "agent.removed"}
	if !slices.Equal(names, want) {
		t.Fatalf("audit events = %v, want %v", names, want)
	}
}

func TestDeletingAnAccountRemovesItsAgentsFromTheTailnet(t *testing.T) {
	env := newAgentEnv(t, "alice", "bob")
	ctx := context.Background()
	env.enroll(t, "alice", "laptop")
	env.enroll(t, "bob", "server")
	accounts := app.NewAccounts(env.store).WithAgents(env.agents)

	deleted, err := accounts.Delete(ctx, "alice", false)
	if err != nil || !deleted.Deleted {
		t.Fatalf("deleting the Account = %#v, %v", deleted, err)
	}
	nodes, err := env.tailnet.Nodes(ctx)
	must(t, err)
	if len(nodes) != 1 || nodes[0].Tags[0] != "tag:acct-bob" {
		t.Fatalf("nodes after deleting alice = %#v, want only bob's", nodes)
	}
}
