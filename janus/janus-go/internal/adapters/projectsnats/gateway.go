package projectsnats

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go"

	"janus/internal/contract"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natsx"
)

// Gateway is the projects service as the gateway reads it: the contract's types,
// with a failure the service reported as a *contract.Error.
type Gateway struct{ conn *nats.Conn }

// NewGateway returns a Gateway that reads from the projects service over conn.
func NewGateway(conn *nats.Conn) *Gateway { return &Gateway{conn: conn} }

func (g *Gateway) call(ctx context.Context, subject string, request, reply any) error {
	err := natsx.Call(ctx, g.conn, subject, request, reply)
	var remote *natsx.RemoteError
	if !errors.As(err, &remote) {
		return err
	}
	return contract.FromStatus(remote.Status, remote.Message)
}

// ListProjects returns the Projects the Owner has, oldest first.
func (g *Gateway) ListProjects(ctx context.Context, ownerID string) ([]contractprojects.Project, error) {
	var list contractprojects.Projects
	err := g.call(ctx, contractprojects.SubjectListProjects, contractprojects.Owner{OwnerID: ownerID}, &list)
	return list.Projects, err
}

// GetProject returns one Project of the Owner. Found is false when the Owner has
// no such Project.
func (g *Gateway) GetProject(ctx context.Context, id, ownerID string) (contractprojects.Project, bool, error) {
	var found contractprojects.ProjectFound
	err := g.call(ctx, contractprojects.SubjectGetProject, contractprojects.ProjectKey{OwnerID: ownerID, ProjectID: id}, &found)
	return found.Project, found.Found, err
}

// ListSnapshots returns the Snapshots of a Project of the Owner.
func (g *Gateway) ListSnapshots(ctx context.Context, ownerID, projectID string) ([]contractprojects.Snapshot, error) {
	var list contractprojects.Snapshots
	err := g.call(ctx, contractprojects.SubjectListSnapshots, contractprojects.ProjectKey{OwnerID: ownerID, ProjectID: projectID}, &list)
	return list.Snapshots, err
}

// Repositories lists the repositories the Owner can see. Connected is false when
// the Owner has no GitHub Connection.
func (g *Gateway) Repositories(ctx context.Context, ownerID string) (contractprojects.Repositories, error) {
	var list contractprojects.Repositories
	err := g.call(ctx, contractprojects.SubjectRepositories, contractprojects.Owner{OwnerID: ownerID}, &list)
	return list, err
}

// Branches lists the branches of a repository, default branch first.
func (g *Gateway) Branches(ctx context.Context, ownerID, repository string) ([]contractprojects.Branch, error) {
	var list contractprojects.Branches
	err := g.call(ctx, contractprojects.SubjectBranches, contractprojects.RepositoryKey{OwnerID: ownerID, Repository: repository}, &list)
	return list.Branches, err
}

// Commits lists the newest commits of a branch of a repository.
func (g *Gateway) Commits(ctx context.Context, ownerID, repository, branch string) ([]contractprojects.Commit, error) {
	var list contractprojects.Commits
	err := g.call(ctx, contractprojects.SubjectCommits, contractprojects.RepositoryKey{OwnerID: ownerID, Repository: repository, Branch: branch}, &list)
	return list.Commits, err
}
