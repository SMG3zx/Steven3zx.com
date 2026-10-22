package httpapi

import (
	"context"

	contractbuilds "janus/internal/contract/builds"
)

// Builds is the builds service as the gateway reaches it: the Account's WASM
// Builds, their Checks and their Artifacts, in the contract's types. Every call is
// scoped to the Owner.
type Builds interface {
	Request(ctx context.Context, request contractbuilds.RequestBuild) (contractbuilds.Build, error)
	Find(ctx context.Context, owner, buildID string) (contractbuilds.Build, bool, error)
	Retry(ctx context.Context, owner, buildID string) (contractbuilds.Build, error)
	Check(ctx context.Context, owner, buildID string) (contractbuilds.Check, bool, error)
	Artifact(ctx context.Context, owner, buildID string) (contractbuilds.ArtifactFound, []byte, error)
}

// wasmBuildView is a WASM Build as the gateway's responses have always shown it. It
// leaves out the Owner's ID the contract carries.
type wasmBuildView struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	SnapshotID   string `json:"snapshot_id"`
	Target       string `json:"target"`
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	Error        string `json:"error,omitempty"`
	ArtifactName string `json:"artifact_name,omitempty"`
	Log          string `json:"log,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func buildView(b contractbuilds.Build) wasmBuildView {
	return wasmBuildView{ID: b.ID, ProjectID: b.ProjectID, SnapshotID: b.SnapshotID, Target: b.Target, Status: b.Status,
		Progress: b.Progress, Error: b.Error, ArtifactName: b.ArtifactName, Log: b.Log, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
}
