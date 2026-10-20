package buildertest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"janus/internal/app"
	"janus/internal/domain/artifact"
)

func TestFakePassesTheBuilderContract(t *testing.T) {
	Run(t, func(*testing.T) app.Builder { return NewFake() })
}

func TestFakeRecordsRequestsAndProducesItsModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f := NewFake()
	request := app.BuildRequest{Workspace: dir, Target: "wasm32-wasip1"}
	got, err := f.Build(context.Background(), "Rust", request)
	if err != nil {
		t.Fatal(err)
	}
	module, _, err := artifact.Module(got.Artifact, got.Name)
	if err != nil || string(module) != string(Module) {
		t.Fatalf("module = %q, %v", module, err)
	}
	if requests := f.Requests(); len(requests) != 1 || requests[0] != request {
		t.Fatalf("requests = %+v", requests)
	}
}
