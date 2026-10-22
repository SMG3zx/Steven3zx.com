// Package buildsnats is the app.WASMBuilds adapter that asks the builds service
// over NATS. It translates app types to the builds contract, a refusal of the
// service (400, 404, 409) to an *app.ReplyError carrying the service's sentence,
// and reads Artifacts from the artifacts object store, copying one back from
// builds.db first when the object is missing. The Gateway does the requests in
// the contract's types; the Client is the app port over it.
package buildsnats

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go"

	"janus/internal/app"
	"janus/internal/contract"
	contractbuilds "janus/internal/contract/builds"
)

// Client is an app.WASMBuilds over a Gateway.
type Client struct {
	gateway *Gateway
}

var _ app.WASMBuilds = (*Client)(nil)
var _ app.BuildArtifacts = (*Client)(nil)

// New returns a Client that sends its requests on conn and reads Artifacts from
// the artifacts object store.
func New(ctx context.Context, conn *nats.Conn) (*Client, error) {
	gateway, err := NewGateway(ctx, conn)
	if err != nil {
		return nil, err
	}
	return &Client{gateway: gateway}, nil
}

// portError turns a refusal the service reported into the *app.ReplyError the port
// documents. Other errors are returned as they are.
func portError(err error) error {
	var failure *contract.Error
	if !errors.As(err, &failure) {
		return err
	}
	switch failure.Status {
	case contract.StatusInvalid, contract.StatusNotFound, contract.StatusConflict:
		return &app.ReplyError{Err: errors.New(failure.Message)}
	}
	return err
}

func build(b contractbuilds.Build) app.WASMBuild {
	return app.WASMBuild{ID: b.ID, ProjectID: b.ProjectID, SnapshotID: b.SnapshotID, Target: b.Target, Status: b.Status,
		Progress: b.Progress, Error: b.Error, ArtifactName: b.ArtifactName, Log: b.Log, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
}

func (c *Client) StartBuild(ctx context.Context, request app.StartBuild) (app.WASMBuild, error) {
	created, err := c.gateway.Request(ctx, contractbuilds.RequestBuild{ID: request.ID, OwnerID: request.OwnerID,
		ProjectID: request.ProjectID, SnapshotID: request.SnapshotID, Target: request.Target})
	return build(created), portError(err)
}

func (c *Client) RetryBuild(ctx context.Context, owner, buildID string) (app.WASMBuild, error) {
	retried, err := c.gateway.Retry(ctx, owner, buildID)
	return build(retried), portError(err)
}

func (c *Client) FindBuild(ctx context.Context, owner, buildID string) (app.WASMBuild, bool, error) {
	found, ok, err := c.gateway.Find(ctx, owner, buildID)
	return build(found), ok, portError(err)
}

// Artifact returns the Artifact of a complete Build.
func (c *Client) Artifact(ctx context.Context, owner, buildID string) (app.WASMBuildArtifact, error) {
	stored, data, err := c.gateway.Artifact(ctx, owner, buildID)
	if err != nil {
		return app.WASMBuildArtifact{}, portError(err)
	}
	if !stored.Found {
		return app.WASMBuildArtifact{}, nil
	}
	return app.WASMBuildArtifact{Name: stored.ArtifactName, Data: data, Found: true}, nil
}

func (c *Client) DeleteOwner(ctx context.Context, owner string) error {
	return portError(c.gateway.call(ctx, contractbuilds.SubjectDeleteOwner, contractbuilds.Owner{OwnerID: owner}, nil))
}

// FindCheck returns the Check of one of the Owner's Builds. found is false when
// the Build has no Check yet.
func (c *Client) FindCheck(ctx context.Context, owner, buildID string) (app.Check, bool, error) {
	check, found, err := c.gateway.Check(ctx, owner, buildID)
	return app.Check{BuildID: check.BuildID, Status: check.Status, Error: check.Error, Log: check.Log, NoTests: check.NoTests}, found, portError(err)
}
