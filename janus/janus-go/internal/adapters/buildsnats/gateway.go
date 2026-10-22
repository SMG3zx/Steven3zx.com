package buildsnats

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/contract"
	contractbuilds "janus/internal/contract/builds"
	"janus/internal/platform/natsx"
)

// Gateway is the builds service as the gateway reaches it: the contract's types,
// with a refusal the service reported as a *contract.Error, and Artifacts read from
// the artifacts object store.
type Gateway struct {
	conn      *nats.Conn
	artifacts jetstream.ObjectStore
}

// NewGateway returns a Gateway that sends its requests on conn and reads Artifacts
// from the artifacts object store.
func NewGateway(ctx context.Context, conn *nats.Conn) (*Gateway, error) {
	js, err := jetstream.New(conn)
	if err != nil {
		return nil, err
	}
	artifacts, err := natsx.DeclareObjectStore(ctx, js, natsx.ArtifactsBucket)
	if err != nil {
		return nil, err
	}
	return &Gateway{conn: conn, artifacts: artifacts}, nil
}

// call sends one request and turns a refusal the service reported into a
// *contract.Error.
func (g *Gateway) call(ctx context.Context, subject string, request, reply any) error {
	err := natsx.Call(ctx, g.conn, subject, request, reply)
	var remote *natsx.RemoteError
	if !errors.As(err, &remote) {
		return err
	}
	return contract.FromStatus(remote.Status, remote.Message)
}

// Request queues a Build of a Project's Snapshot. The caller chooses the Build's
// ID. The service checks the Project and the Snapshot, and refuses a request it
// cannot carry out.
func (g *Gateway) Request(ctx context.Context, request contractbuilds.RequestBuild) (contractbuilds.Build, error) {
	var found contractbuilds.BuildFound
	err := g.call(ctx, contractbuilds.SubjectRequestBuild, request, &found)
	return found.Build, err
}

// Retry queues a new Build of a failed Build's Snapshot.
func (g *Gateway) Retry(ctx context.Context, owner, buildID string) (contractbuilds.Build, error) {
	var found contractbuilds.BuildFound
	err := g.call(ctx, contractbuilds.SubjectRetryBuild, contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}, &found)
	return found.Build, err
}

// Find returns one of the Owner's Builds. Found is false when there is none.
func (g *Gateway) Find(ctx context.Context, owner, buildID string) (contractbuilds.Build, bool, error) {
	var found contractbuilds.BuildFound
	err := g.call(ctx, contractbuilds.SubjectFindBuild, contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}, &found)
	return found.Build, found.Found, err
}

// Check returns the Check of one of the Owner's Builds. Found is false when the
// Build has no Check yet.
func (g *Gateway) Check(ctx context.Context, owner, buildID string) (contractbuilds.Check, bool, error) {
	var found contractbuilds.CheckFound
	err := g.call(ctx, contractbuilds.SubjectFindCheck, contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}, &found)
	return found.Check, found.Found, err
}

// Artifact returns the Artifact of a complete Build, with its bytes. Found is false,
// and the bytes nil, when the Build has no Artifact the Owner may read. The bytes
// come from the artifacts store; when the object is not there, the service copies it
// back from builds.db and the read is tried once more.
func (g *Gateway) Artifact(ctx context.Context, owner, buildID string) (contractbuilds.ArtifactFound, []byte, error) {
	key := contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}
	var stored contractbuilds.ArtifactFound
	if err := g.call(ctx, contractbuilds.SubjectFindArtifact, key, &stored); err != nil || !stored.Found {
		return contractbuilds.ArtifactFound{}, nil, err
	}
	data, found, err := natsx.GetObject(ctx, g.artifacts, stored.ObjectName)
	if err != nil {
		return contractbuilds.ArtifactFound{}, nil, err
	}
	if !found {
		if err = g.call(ctx, contractbuilds.SubjectRestoreArtifact, key, nil); err != nil {
			return contractbuilds.ArtifactFound{}, nil, err
		}
		if data, found, err = natsx.GetObject(ctx, g.artifacts, stored.ObjectName); err != nil || !found {
			return contractbuilds.ArtifactFound{}, nil, errors.Join(err, errors.New("the artifact could not be restored"))
		}
	}
	return stored, data, nil
}
