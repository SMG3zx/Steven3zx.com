package httpapi

import (
	"context"

	contractprojects "janus/internal/contract/projects"
)

// Catalog is the projects service as the gateway reaches it: the Projects,
// Snapshots, Imports, and GitHub repositories, branches and commits an Account
// can see or change. Every call is scoped to the Owner.
type Catalog interface {
	ListProjects(ctx context.Context, ownerID string) ([]contractprojects.Project, error)
	GetProject(ctx context.Context, id, ownerID string) (contractprojects.Project, bool, error)
	ListSnapshots(ctx context.Context, ownerID, projectID string) ([]contractprojects.Snapshot, error)
	CreateProject(ctx context.Context, request contractprojects.CreateProject) (contractprojects.Project, error)
	UpdateProject(ctx context.Context, request contractprojects.UpdateProject) (contractprojects.Project, error)
	DeleteProject(ctx context.Context, id, ownerID string) (bool, error)
	ActivateSnapshot(ctx context.Context, ownerID, projectID, snapshotID string) (bool, error)
	SetEntryPoint(ctx context.Context, request contractprojects.SetEntryPoint) error
	RequestImport(ctx context.Context, request contractprojects.RequestImport) (string, error)
	RetryImport(ctx context.Context, ownerID, jobID string) (string, error)
	FindImportJob(ctx context.Context, id, ownerID string) (contractprojects.ImportJob, bool, error)
	Repositories(ctx context.Context, ownerID string) (contractprojects.Repositories, error)
	Branches(ctx context.Context, ownerID, repository string) ([]contractprojects.Branch, error)
	Commits(ctx context.Context, ownerID, repository, branch string) ([]contractprojects.Commit, error)
}
