package httpapi

import (
	"context"

	contractprojects "janus/internal/contract/projects"
)

// Catalog is the projects service's read paths as the gateway reaches them: the
// Projects, Snapshots, and GitHub repositories, branches and commits an Account
// can see. Every call is scoped to the Owner.
type Catalog interface {
	ListProjects(ctx context.Context, ownerID string) ([]contractprojects.Project, error)
	GetProject(ctx context.Context, id, ownerID string) (contractprojects.Project, bool, error)
	ListSnapshots(ctx context.Context, ownerID, projectID string) ([]contractprojects.Snapshot, error)
	Repositories(ctx context.Context, ownerID string) (contractprojects.Repositories, error)
	Branches(ctx context.Context, ownerID, repository string) ([]contractprojects.Branch, error)
	Commits(ctx context.Context, ownerID, repository, branch string) ([]contractprojects.Commit, error)
}
