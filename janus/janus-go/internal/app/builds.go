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

// Builds is the single process's use of WASM Builds. The builds service compiles
// and checks them; Builds records the references that Deployments need and
// answers the Account's requests through WASMBuilds.
type Builds struct {
	store   Store
	catalog Catalog
	service WASMBuilds
}

// NewBuilds returns a Builds that reads Projects and Snapshots from catalog,
// records the references in store and asks service for the Builds themselves.
func NewBuilds(store Store, catalog Catalog, service WASMBuilds) *Builds {
	return &Builds{store: store, catalog: catalog, service: service}
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
	// The Build refers to the Project and Snapshot, which live in the projects
	// service; the Store keeps a reference to each first.
	if err = b.store.AddProject(ctx, ProjectRef{ID: request.ProjectID, OwnerID: request.OwnerID}); err != nil {
		return "", err
	}
	if err = b.store.AddSnapshot(ctx, snapshot); err != nil {
		return "", err
	}
	id := uuid.NewString()
	build, err := b.service.StartBuild(ctx, StartBuild{ID: id, OwnerID: request.OwnerID, ProjectID: request.ProjectID, SnapshotID: snapshot.ID, Target: request.Target})
	if err != nil {
		return "", err
	}
	b.reference(ctx, request.OwnerID, build)
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
	b.reference(ctx, owner, build)
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

// ForgetProject deletes the references to the WASM Builds of a Project the
// projects service deleted, with what refers to them. Forgetting a Project the
// Store never heard of changes nothing.
func (b *Builds) ForgetProject(ctx context.Context, projectID string) error {
	return b.store.RemoveProject(ctx, projectID)
}

// ForgetSnapshot deletes the references to the WASM Builds of a Snapshot the
// projects service deleted, like ForgetProject.
func (b *Builds) ForgetSnapshot(ctx context.Context, snapshotID string) error {
	return b.store.RemoveSnapshot(ctx, snapshotID)
}

// reference stores a Build's state so a Deployment can refer to it at once. The
// builds service publishes the same state on EVENTS, so a failure here is not
// the Account's: the event brings the reference later.
func (b *Builds) reference(ctx context.Context, owner string, build WASMBuild) {
	_ = b.store.ApplyWASMBuild(ctx, WASMBuildChange{ID: build.ID, OwnerID: owner, ProjectID: build.ProjectID, SnapshotID: build.SnapshotID, Target: build.Target, Status: build.Status, Error: build.Error})
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
