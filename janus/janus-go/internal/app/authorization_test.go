package app

import (
	"context"
	"sync"
	"testing"
)

func TestOwnerScopedPolicies(t *testing.T) {
	policy, err := NewPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"project.build", "account.delete"} {
		for _, test := range []struct {
			name    string
			subject string
			owner   string
			allowed bool
		}{
			{name: "owner is allowed", subject: "alice", owner: "alice", allowed: true},
			{name: "another user is refused", subject: "bob", owner: "alice", allowed: false},
			{name: "anonymous is refused", subject: "", owner: "", allowed: false},
		} {
			t.Run(action+" "+test.name, func(t *testing.T) {
				allowed, err := policy.Allowed(context.Background(), test.subject, action, map[string]any{"owner_id": test.owner})
				if err != nil || allowed != test.allowed {
					t.Fatalf("%s for %q on %q = %v, %v; want allowed=%t", action, test.subject, test.owner, allowed, err, test.allowed)
				}
			})
		}
	}
}

// The prepared query is shared by every request, so concurrent checks must
// agree with sequential ones.
func TestPolicyIsSafeForConcurrentChecks(t *testing.T) {
	policy, err := NewPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if allowed, err := policy.Allowed(context.Background(), "alice", "project.build", map[string]any{"owner_id": "alice"}); err != nil || !allowed {
				t.Errorf("owner check = %v, %v", allowed, err)
			}
			if allowed, err := policy.Allowed(context.Background(), "bob", "project.build", map[string]any{"owner_id": "alice"}); err != nil || allowed {
				t.Errorf("stranger check = %v, %v", allowed, err)
			}
		}()
	}
	wg.Wait()
}
