package app

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// NewBuild asks for a WASM Build of a Project's Snapshot.
type NewBuild struct {
	OwnerID, ProjectID, Target string
	// SnapshotID selects the Snapshot to build. When empty, the Project's active
	// Snapshot is built.
	SnapshotID string
}

// Builds is the single process's use of WASM Builds. The builds service compiles,
// checks and keeps them; Builds reads the Projects and Snapshots a Build needs
// from the projects service and answers the Account's requests through
// WASMBuilds.
type Builds struct {
	catalog Catalog
	service WASMBuilds
}

// NewBuilds returns a Builds that reads Projects and Snapshots from catalog and
// asks service for the Builds themselves.
func NewBuilds(catalog Catalog, service WASMBuilds) *Builds {
	return &Builds{catalog: catalog, service: service}
}

// Request queues a Build and returns its ID. It returns a plain error whose text
// is meant for the Account when the Build cannot start.
func (b *Builds) Request(ctx context.Context, request NewBuild) (string, error) {
	if b.service == nil {
		return "", errors.New("WASM build runner is not configured")
	}
	if request.OwnerID == "" || request.ProjectID == "" {
		return "", errors.New("project is required")
	}
	project, found, err := b.catalog.GetProject(ctx, request.ProjectID, request.OwnerID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("project not found")
	}
	snapshot, err := b.buildSource(ctx, request, project)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	if _, err = b.service.StartBuild(ctx, StartBuild{ID: id, OwnerID: request.OwnerID, ProjectID: request.ProjectID, SnapshotID: snapshot.ID, Target: request.Target}); err != nil {
		return "", err
	}
	return id, nil
}

// Find returns one of an Account's Builds.
func (b *Builds) Find(ctx context.Context, owner, id string) (WASMBuild, bool, error) {
	if b.service == nil {
		return WASMBuild{}, false, errors.New("WASM build runner is not configured")
	}
	return b.service.FindBuild(ctx, owner, id)
}

// Artifact returns the Artifact of one of an Account's completed Builds.
func (b *Builds) Artifact(ctx context.Context, owner, id string) (WASMBuildArtifact, error) {
	if b.service == nil {
		return WASMBuildArtifact{}, errors.New("WASM build runner is not configured")
	}
	return b.service.Artifact(ctx, owner, id)
}

// Retry starts a new Build of a failed Build's Snapshot and returns its ID.
func (b *Builds) Retry(ctx context.Context, owner, id string) (string, error) {
	if b.service == nil {
		return "", errors.New("WASM build runner is not configured")
	}
	build, err := b.service.RetryBuild(ctx, owner, id)
	if err != nil {
		return "", err
	}
	return build.ID, nil
}

// DeleteOwner deletes an Account's Builds and Artifacts, cancelling the ones
// running, and is idempotent.
func (b *Builds) DeleteOwner(ctx context.Context, owner string) error {
	if b.service == nil {
		return nil
	}
	return b.service.DeleteOwner(ctx, owner)
}

// buildSource returns the Snapshot a Build compiles, with its detected language
// and runtime. The Project's active Snapshot is used unless the request names
// one.
func (b *Builds) buildSource(ctx context.Context, request NewBuild, project Project) (SnapshotRef, error) {
	ref := SnapshotRef{ID: project.SnapshotID, ProjectID: project.ID, OwnerID: request.OwnerID, Language: project.Language, Runtime: project.Runtime}
	if request.SnapshotID == "" {
		if project.SnapshotID == "" {
			return SnapshotRef{}, errors.New("import a repository before building it")
		}
		return ref, nil
	}
	snapshots, err := b.catalog.ListSnapshots(ctx, request.OwnerID, request.ProjectID)
	if err != nil {
		return SnapshotRef{}, err
	}
	for _, snapshot := range snapshots {
		if snapshot.ID == request.SnapshotID {
			ref.ID, ref.Language, ref.Runtime = snapshot.ID, snapshot.Language, snapshot.Runtime
			return ref, nil
		}
	}
	return SnapshotRef{}, errors.New("snapshot not found")
}

// Check returns the Check of one of an Account's Builds. found is false when the
// Build has no Check yet.
func (b *Builds) Check(ctx context.Context, owner, id string) (Check, bool, error) {
	if b.service == nil {
		return Check{}, false, errors.New("WASM build runner is not configured")
	}
	return b.service.FindCheck(ctx, owner, id)
}
