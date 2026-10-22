package buildstest

import (
	"context"

	"janus/internal/app"
	contractbuilds "janus/internal/contract/builds"
)

// Gateway serves a Fake to the gateway with the contract's types, as the gateway's
// NATS client does.
type Gateway struct{ fake *Fake }

// NewGateway returns a Gateway over fake.
func NewGateway(fake *Fake) *Gateway { return &Gateway{fake: fake} }

// Request starts a Build of the request's Snapshot and returns it.
func (g *Gateway) Request(ctx context.Context, request contractbuilds.RequestBuild) (contractbuilds.Build, error) {
	build, err := g.fake.StartBuild(ctx, app.StartBuild{ID: request.ID, OwnerID: request.OwnerID, ProjectID: request.ProjectID,
		SnapshotID: request.SnapshotID, Target: request.Target})
	return toContract(build, request.OwnerID), err
}

// Retry starts a new Build of a failed Build's Snapshot and returns it.
func (g *Gateway) Retry(ctx context.Context, owner, buildID string) (contractbuilds.Build, error) {
	build, err := g.fake.RetryBuild(ctx, owner, buildID)
	return toContract(build, owner), err
}

// Find returns one of the Owner's Builds, if it exists.
func (g *Gateway) Find(ctx context.Context, owner, buildID string) (contractbuilds.Build, bool, error) {
	build, found, err := g.fake.FindBuild(ctx, owner, buildID)
	return toContract(build, owner), found, err
}

// Check returns the Check of one of the Owner's Builds, if it has one.
func (g *Gateway) Check(ctx context.Context, owner, buildID string) (contractbuilds.Check, bool, error) {
	check, found, err := g.fake.FindCheck(ctx, owner, buildID)
	return contractbuilds.Check{BuildID: check.BuildID, Status: check.Status, Error: check.Error, Log: check.Log, NoTests: check.NoTests}, found, err
}

// Artifact returns the Artifact of one of the Owner's Builds with its bytes. Found
// is false when there is none.
func (g *Gateway) Artifact(ctx context.Context, owner, buildID string) (contractbuilds.ArtifactFound, []byte, error) {
	artifact, err := g.fake.Artifact(ctx, owner, buildID)
	if err != nil || !artifact.Found {
		return contractbuilds.ArtifactFound{}, nil, err
	}
	return contractbuilds.ArtifactFound{Found: true, ArtifactName: artifact.Name}, artifact.Data, nil
}

func toContract(b app.WASMBuild, owner string) contractbuilds.Build {
	return contractbuilds.Build{ID: b.ID, OwnerID: owner, ProjectID: b.ProjectID, SnapshotID: b.SnapshotID, Target: b.Target,
		Status: b.Status, Progress: b.Progress, Error: b.Error, ArtifactName: b.ArtifactName, Log: b.Log, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
}
