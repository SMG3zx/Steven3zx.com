package tailnettest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"janus/internal/services/fleet/headscale"
)

// Fake is an headscale.Tailnet that keeps keys and nodes in maps. Tests make nodes
// join with Join, the way an Agent does with the key it was given, and flip a
// node offline with SetOnline.
type Fake struct {
	// Now reports the time keys are judged against. It defaults to time.Now.
	Now func() time.Time

	mu    sync.Mutex
	seq   int
	keys  map[string]*fakeKey  // by key ID
	nodes map[string]*fakeNode // by node ID
}

type fakeKey struct {
	headscale.Key
	used, expired bool
}

type fakeNode struct{ headscale.Node }

var _ headscale.Tailnet = (*Fake)(nil)

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{Now: time.Now, keys: map[string]*fakeKey{}, nodes: map[string]*fakeNode{}}
}

func (f *Fake) CreatePreAuthKey(_ context.Context, tag string, expires time.Time) (headscale.Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	key := headscale.Key{ID: fmt.Sprint(f.seq), Key: "fake-key-" + uuid.NewString(), Tag: tag, Expires: expires}
	f.keys[key.ID] = &fakeKey{Key: key}
	return key, nil
}

func (f *Fake) ExpirePreAuthKey(_ context.Context, keyID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if key, ok := f.keys[keyID]; ok {
		key.expired = true
	}
	return nil
}

func (f *Fake) Nodes(context.Context) ([]headscale.Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	nodes := make([]headscale.Node, 0, len(f.nodes))
	for _, node := range f.nodes {
		nodes = append(nodes, node.Node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes, nil
}

func (f *Fake) DeleteNode(_ context.Context, nodeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.nodes, nodeID)
	return nil
}

// Join makes a node named name join with the key whose secret is secret, as an
// Agent does. It fails when the secret is unknown, was used, was expired or
// ran past its expiry.
func (f *Fake) Join(secret, name string) (headscale.Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, key := range f.keys {
		if key.Key.Key != secret {
			continue
		}
		if key.used || key.expired || !f.Now().Before(key.Expires) {
			return headscale.Node{}, errors.New("pre-auth key is used or expired")
		}
		key.used = true
		f.seq++
		node := headscale.Node{ID: fmt.Sprint(f.seq), Name: name, KeyID: key.ID, Tags: []string{key.Tag},
			Addresses: []string{fmt.Sprintf("100.64.0.%d", f.seq)}, Online: true, LastSeen: f.Now().UTC()}
		f.nodes[node.ID] = &fakeNode{node}
		return node, nil
	}
	return headscale.Node{}, errors.New("unknown pre-auth key")
}

// JoinForContract is Join in the shape Subject.Join wants.
func (f *Fake) JoinForContract(_ *testing.T, key headscale.Key, name string) error {
	_, err := f.Join(key.Key, name)
	return err
}

// SetOnline sets whether the node is reported online.
func (f *Fake) SetOnline(nodeID string, online bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if node, ok := f.nodes[nodeID]; ok {
		node.Online = online
		node.LastSeen = f.Now().UTC()
	}
}
