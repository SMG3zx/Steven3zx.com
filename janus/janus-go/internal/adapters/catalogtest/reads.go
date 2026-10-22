package catalogtest

import (
	"context"

	contractprojects "janus/internal/contract/projects"
)

// Reads serves the projects service's read paths to the gateway from a Memory,
// with the contract's types.
type Reads struct{ memory *Memory }

// NewReads returns a Reads over memory.
func NewReads(memory *Memory) *Reads { return &Reads{memory: memory} }

// ListProjects returns the Projects the Owner has.
func (r *Reads) ListProjects(ctx context.Context, ownerID string) ([]contractprojects.Project, error) {
	projects, err := r.memory.ListProjects(ctx, ownerID)
	out := make([]contractprojects.Project, 0, len(projects))
	for _, p := range projects {
		out = append(out, contractprojects.Project(p))
	}
	return out, err
}

// GetProject returns one Project of the Owner, if it exists.
func (r *Reads) GetProject(ctx context.Context, id, ownerID string) (contractprojects.Project, bool, error) {
	project, found, err := r.memory.GetProject(ctx, id, ownerID)
	return contractprojects.Project(project), found, err
}

// ListSnapshots returns the Snapshots of a Project of the Owner.
func (r *Reads) ListSnapshots(ctx context.Context, ownerID, projectID string) ([]contractprojects.Snapshot, error) {
	snapshots, err := r.memory.ListSnapshots(ctx, ownerID, projectID)
	out := make([]contractprojects.Snapshot, 0, len(snapshots))
	for _, s := range snapshots {
		out = append(out, contractprojects.Snapshot(s))
	}
	return out, err
}

// Repositories lists the repositories the Owner can see.
func (r *Reads) Repositories(ctx context.Context, ownerID string) (contractprojects.Repositories, error) {
	list, err := r.memory.Repositories(ctx, ownerID)
	out := contractprojects.Repositories{Connected: list.Connected, Repositories: make([]contractprojects.Repository, 0, len(list.Repositories))}
	for _, repository := range list.Repositories {
		out.Repositories = append(out.Repositories, contractprojects.Repository(repository))
	}
	return out, err
}

// Branches lists the branches of a repository.
func (r *Reads) Branches(ctx context.Context, ownerID, repository string) ([]contractprojects.Branch, error) {
	branches, err := r.memory.Branches(ctx, ownerID, repository)
	out := make([]contractprojects.Branch, 0, len(branches))
	for _, b := range branches {
		out = append(out, contractprojects.Branch(b))
	}
	return out, err
}

// Commits lists the commits of a branch of a repository.
func (r *Reads) Commits(ctx context.Context, ownerID, repository, branch string) ([]contractprojects.Commit, error) {
	commits, err := r.memory.Commits(ctx, ownerID, repository, branch)
	out := make([]contractprojects.Commit, 0, len(commits))
	for _, c := range commits {
		out = append(out, contractprojects.Commit(c))
	}
	return out, err
}
