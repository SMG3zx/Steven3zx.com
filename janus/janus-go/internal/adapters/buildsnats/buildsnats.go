// Package buildsnats is the app.WASMBuilds adapter that asks the builds service
// over NATS. It translates app types to the builds contract, a refusal of the
// service (400, 404, 409) to an *app.ReplyError carrying the service's sentence,
// and reads Artifacts from the artifacts object store, copying one back from
// builds.db first when the object is missing.
package buildsnats

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/app"
	"janus/internal/contract"
	contractbuilds "janus/internal/contract/builds"
	"janus/internal/platform/natsx"
)

// Client is an app.WASMBuilds over a NATS connection and the artifacts store.
type Client struct {
	conn      *nats.Conn
	artifacts jetstream.ObjectStore
}

var _ app.WASMBuilds = (*Client)(nil)
var _ app.BuildArtifacts = (*Client)(nil)

// New returns a Client that sends its requests on conn and reads Artifacts from
// the artifacts object store.
func New(ctx context.Context, conn *nats.Conn) (*Client, error) {
	js, err := jetstream.New(conn)
	if err != nil {
		return nil, err
	}
	artifacts, err := natsx.DeclareObjectStore(ctx, js, natsx.ArtifactsBucket)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, artifacts: artifacts}, nil
}

// call sends one request and turns a refusal the service reported into the
// error the port documents.
func (c *Client) call(ctx context.Context, subject string, request, reply any) error {
	err := natsx.Call(ctx, c.conn, subject, request, reply)
	var remote *natsx.RemoteError
	if !errors.As(err, &remote) {
		return err
	}
	switch remote.Status {
	case contract.StatusInvalid, contract.StatusNotFound, contract.StatusConflict:
		return &app.ReplyError{Err: errors.New(remote.Message)}
	}
	return contract.FromStatus(remote.Status, remote.Message)
}

func build(b contractbuilds.Build) app.WASMBuild {
	return app.WASMBuild{ID: b.ID, ProjectID: b.ProjectID, SnapshotID: b.SnapshotID, Target: b.Target, Status: b.Status,
		Progress: b.Progress, Error: b.Error, ArtifactName: b.ArtifactName, Log: b.Log, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
}

func (c *Client) StartBuild(ctx context.Context, request app.StartBuild) (app.WASMBuild, error) {
	var found contractbuilds.BuildFound
	err := c.call(ctx, contractbuilds.SubjectRequestBuild, contractbuilds.RequestBuild{ID: request.ID, OwnerID: request.OwnerID,
		ProjectID: request.ProjectID, SnapshotID: request.SnapshotID, Target: request.Target}, &found)
	return build(found.Build), err
}

func (c *Client) RetryBuild(ctx context.Context, owner, buildID string) (app.WASMBuild, error) {
	var found contractbuilds.BuildFound
	err := c.call(ctx, contractbuilds.SubjectRetryBuild, contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}, &found)
	return build(found.Build), err
}

func (c *Client) FindBuild(ctx context.Context, owner, buildID string) (app.WASMBuild, bool, error) {
	var found contractbuilds.BuildFound
	err := c.call(ctx, contractbuilds.SubjectFindBuild, contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}, &found)
	return build(found.Build), found.Found, err
}

// Artifact returns the Artifact of a complete Build. The bytes come from the
// artifacts store; when the object is not there, the service copies it back from
// builds.db and the read is tried once more.
func (c *Client) Artifact(ctx context.Context, owner, buildID string) (app.WASMBuildArtifact, error) {
	key := contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}
	var stored contractbuilds.ArtifactFound
	if err := c.call(ctx, contractbuilds.SubjectFindArtifact, key, &stored); err != nil || !stored.Found {
		return app.WASMBuildArtifact{}, err
	}
	data, found, err := natsx.GetObject(ctx, c.artifacts, stored.ObjectName)
	if err != nil {
		return app.WASMBuildArtifact{}, err
	}
	if !found {
		if err = c.call(ctx, contractbuilds.SubjectRestoreArtifact, key, nil); err != nil {
			return app.WASMBuildArtifact{}, err
		}
		if data, found, err = natsx.GetObject(ctx, c.artifacts, stored.ObjectName); err != nil || !found {
			return app.WASMBuildArtifact{}, errors.Join(err, errors.New("the artifact could not be restored"))
		}
	}
	return app.WASMBuildArtifact{Name: stored.ArtifactName, Data: data, Found: true}, nil
}

func (c *Client) DeleteOwner(ctx context.Context, owner string) error {
	return c.call(ctx, contractbuilds.SubjectDeleteOwner, contractbuilds.Owner{OwnerID: owner}, nil)
}

// FindCheck returns the Check of one of the Owner's Builds. found is false when
// the Build has no Check yet.
func (c *Client) FindCheck(ctx context.Context, owner, buildID string) (app.Check, bool, error) {
	var found contractbuilds.CheckFound
	err := c.call(ctx, contractbuilds.SubjectFindCheck, contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}, &found)
	check := found.Check
	return app.Check{BuildID: check.BuildID, Status: check.Status, Error: check.Error, Log: check.Log, NoTests: check.NoTests}, found.Found, err
}
