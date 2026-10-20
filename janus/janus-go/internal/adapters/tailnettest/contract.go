// Package tailnettest holds an in-memory app.Tailnet and the contract suite
// every app.Tailnet adapter, real or test, must pass.
package tailnettest

import (
	"context"
	"slices"
	"testing"
	"time"

	"janus/internal/app"
)

// Subject is an adapter under test, with the one thing the port cannot do: make
// a node join, as an Agent does with the key it was given.
type Subject struct {
	Tailnet app.Tailnet
	// Join registers a node named name using key and returns once it has joined
	// or failed. It returns an error when the key cannot be used.
	Join func(t *testing.T, key app.TailnetKey, name string) error
}

// Run runs the Tailnet contract against subjects made by newSubject. Cases share
// no state except what the adapter itself keeps, so they use their own tags.
func Run(t *testing.T, newSubject func(t *testing.T) Subject) {
	t.Helper()
	for _, c := range []struct {
		name string
		test func(t *testing.T, s Subject)
	}{
		{"KeysAreDistinctAndCarryAnID", testKeys},
		{"AJoinedNodeShowsItsKeyAndTag", testJoin},
		{"AKeyWorksOnce", testSingleUse},
		{"AnExpiredKeyCannotJoin", testExpired},
		{"DeletingANodeRemovesIt", testDelete},
	} {
		t.Run(c.name, func(t *testing.T) { c.test(t, newSubject(t)) })
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newKey(t *testing.T, s Subject, tag string) app.TailnetKey {
	t.Helper()
	key, err := s.Tailnet.CreatePreAuthKey(context.Background(), tag, time.Now().Add(time.Hour))
	must(t, err)
	return key
}

// waitNode polls Nodes until the node that joined with keyID is listed and
// online, since a real control server reports a join a moment late.
func waitNode(t *testing.T, s Subject, keyID string) app.TailnetNode {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		nodes, err := s.Tailnet.Nodes(context.Background())
		must(t, err)
		for _, node := range nodes {
			if node.KeyID == keyID && node.Online {
				return node
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no online node joined with key %s; nodes = %#v", keyID, nodes)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func testKeys(t *testing.T, s Subject) {
	first, second := newKey(t, s, "tag:contract-keys"), newKey(t, s, "tag:contract-keys")
	if first.ID == "" || first.Key == "" {
		t.Fatalf("key = %#v, want an ID and a secret", first)
	}
	if first.ID == second.ID || first.Key == second.Key {
		t.Fatalf("two keys share an ID or a secret: %#v, %#v", first, second)
	}
}

func testJoin(t *testing.T, s Subject) {
	key := newKey(t, s, "tag:contract-join")
	must(t, s.Join(t, key, "contract-join"))
	node := waitNode(t, s, key.ID)
	if !slices.Equal(node.Tags, []string{"tag:contract-join"}) {
		t.Fatalf("node tags = %v, want the key's tag", node.Tags)
	}
	if node.Name != "contract-join" || node.ID == "" || len(node.Addresses) == 0 {
		t.Fatalf("node = %#v, want its name, an ID and an address", node)
	}
}

func testSingleUse(t *testing.T, s Subject) {
	key := newKey(t, s, "tag:contract-once")
	must(t, s.Join(t, key, "contract-once-a"))
	waitNode(t, s, key.ID)
	if err := s.Join(t, key, "contract-once-b"); err == nil {
		t.Fatal("a second node joined with a key that was already used")
	}
}

func testExpired(t *testing.T, s Subject) {
	key := newKey(t, s, "tag:contract-expired")
	must(t, s.Tailnet.ExpirePreAuthKey(context.Background(), key.ID))
	if err := s.Join(t, key, "contract-expired"); err == nil {
		t.Fatal("a node joined with an expired key")
	}
	for _, unknown := range []string{"no-such-key", "987654321"} {
		if err := s.Tailnet.ExpirePreAuthKey(context.Background(), unknown); err != nil {
			t.Fatalf("expiring unknown key %q = %v, want no error", unknown, err)
		}
	}
}

func testDelete(t *testing.T, s Subject) {
	ctx := context.Background()
	key := newKey(t, s, "tag:contract-delete")
	must(t, s.Join(t, key, "contract-delete"))
	node := waitNode(t, s, key.ID)

	must(t, s.Tailnet.DeleteNode(ctx, node.ID))
	nodes, err := s.Tailnet.Nodes(ctx)
	must(t, err)
	for _, left := range nodes {
		if left.ID == node.ID {
			t.Fatalf("node %s is still listed after it was deleted", node.ID)
		}
	}
	if err = s.Tailnet.DeleteNode(ctx, node.ID); err != nil {
		t.Fatalf("deleting a node twice = %v, want no error", err)
	}
	for _, unknown := range []string{"no-such-node", "987654321"} {
		if err = s.Tailnet.DeleteNode(ctx, unknown); err != nil {
			t.Fatalf("deleting unknown node %q = %v, want no error", unknown, err)
		}
	}
}
