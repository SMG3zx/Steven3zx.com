package agentnats

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/adapters/localstatetest"
	"janus/internal/app"
	contractfleet "janus/internal/contract/fleet"
)

// TestALargeModuleStagedForATrustedOwnerIsReadByTheAgent reads a module of several
// chunks through the leaf.
func TestALargeModuleStagedForATrustedOwnerIsReadByTheAgent(t *testing.T) {
	module := append([]byte("\x00asm\x01\x00\x00\x00big"), make([]byte, 3<<20)...)
	sum := sha256.Sum256(module)
	sha := hex.EncodeToString(sum[:])

	_, trust, fleet, leafAddress := trustedHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	js, err := jetstream.New(fleet)
	if err != nil {
		t.Fatal(err)
	}
	modules, err := js.CreateObjectStore(ctx, jetstream.ObjectStoreConfig{Bucket: contractfleet.ModulesBucket(ownerID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modules.PutBytes(ctx, sha, module); err != nil {
		t.Fatal(err)
	}
	link, err := NewLink(fleet)
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	runtime := app.NewAgentRuntime(stagedRunner{}, localstatetest.NewMemory(), "", time.Now)
	stop := trustedAgent(t, trust, leafAddress, runtime)
	defer stop()
	link.Connect(agentID, func(app.AgentEvent) error { return nil })

	deadline := time.Now().Add(40 * time.Second)
	for {
		err := link.Send(ctx, agentID, contractfleet.AgentCommand{Deploy: &contractfleet.HostedDeployment{
			ID: "dep-1", SHA256: sha, Limits: app.RunLimits{Fuel: 1_000_000, MemoryBytes: 16 << 20}}})
		if err == nil {
			break
		}
		if !errors.Is(err, app.ErrAgentUnreachable) || time.Now().After(deadline) {
			t.Fatalf("deploy with the large module staged on the hub: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
