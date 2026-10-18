package authorization

import (
	"context"
	"testing"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"
)

func TestProjectBuildPolicyIsOwnerScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	system, err := goakt.NewActorSystem("project-build-policy-test", goakt.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = system.Stop(stop)
	})
	pid, err := system.Spawn(ctx, "authorization", New())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		subject string
		owner   string
		allowed bool
	}{
		{name: "owner can build", subject: "alice", owner: "alice", allowed: true},
		{name: "another user cannot build", subject: "bob", owner: "alice", allowed: false},
		{name: "anonymous cannot build", subject: "", owner: "", allowed: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, askErr := goakt.Ask(ctx, pid, &Check{Subject: test.subject, Action: "project.build", Resource: map[string]any{"owner_id": test.owner}}, time.Second)
			if askErr != nil {
				t.Fatal(askErr)
			}
			decision, ok := response.(*Decision)
			if !ok || decision.Err != nil || decision.Allowed != test.allowed {
				t.Fatalf("build authorization = %#v; want allowed=%t", response, test.allowed)
			}
		})
	}
}

func TestAccountDeletePolicyIsOwnerScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	system, err := goakt.NewActorSystem("account-delete-policy-test", goakt.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = system.Stop(stop)
	})
	pid, err := system.Spawn(ctx, "authorization", New())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		subject string
		owner   string
		allowed bool
	}{
		{name: "account owner can delete their account", subject: "alice", owner: "alice", allowed: true},
		{name: "another user cannot delete it", subject: "bob", owner: "alice", allowed: false},
		{name: "anonymous cannot delete an account", subject: "", owner: "", allowed: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, askErr := goakt.Ask(ctx, pid, &Check{Subject: test.subject, Action: "account.delete", Resource: map[string]any{"owner_id": test.owner}}, time.Second)
			if askErr != nil {
				t.Fatal(askErr)
			}
			decision, ok := response.(*Decision)
			if !ok || decision.Err != nil || decision.Allowed != test.allowed {
				t.Fatalf("account delete authorization = %#v; want allowed=%t", response, test.allowed)
			}
		})
	}
}
