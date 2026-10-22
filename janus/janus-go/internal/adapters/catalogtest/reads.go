package catalogtest

import (
	"context"
	"errors"
	"strings"

	"janus/internal/app"
	"janus/internal/contract"
	contractprojects "janus/internal/contract/projects"
)

// Gateway serves the projects service's paths to the gateway from a Memory, with
// the contract's types and errors, as the gateway's NATS client does.
type Gateway struct{ memory *Memory }

// NewGateway returns a Gateway over memory.
func NewGateway(memory *Memory) *Gateway { return &Gateway{memory: memory} }

// ListProjects returns the Projects the Owner has.
func (r *Gateway) ListProjects(ctx context.Context, ownerID string) ([]contractprojects.Project, error) {
	projects, err := r.memory.ListProjects(ctx, ownerID)
	out := make([]contractprojects.Project, 0, len(projects))
	for _, p := range projects {
		out = append(out, contractprojects.Project(p))
	}
	return out, contractError(err)
}

// GetProject returns one Project of the Owner, if it exists.
func (r *Gateway) GetProject(ctx context.Context, id, ownerID string) (contractprojects.Project, bool, error) {
	project, found, err := r.memory.GetProject(ctx, id, ownerID)
	return contractprojects.Project(project), found, contractError(err)
}

// ListSnapshots returns the Snapshots of a Project of the Owner.
func (r *Gateway) ListSnapshots(ctx context.Context, ownerID, projectID string) ([]contractprojects.Snapshot, error) {
	snapshots, err := r.memory.ListSnapshots(ctx, ownerID, projectID)
	out := make([]contractprojects.Snapshot, 0, len(snapshots))
	for _, s := range snapshots {
		out = append(out, contractprojects.Snapshot(s))
	}
	return out, contractError(err)
}

// Repositories lists the repositories the Owner can see.
func (r *Gateway) Repositories(ctx context.Context, ownerID string) (contractprojects.Repositories, error) {
	list, err := r.memory.Repositories(ctx, ownerID)
	out := contractprojects.Repositories{Connected: list.Connected, Repositories: make([]contractprojects.Repository, 0, len(list.Repositories))}
	for _, repository := range list.Repositories {
		out.Repositories = append(out.Repositories, contractprojects.Repository(repository))
	}
	return out, contractError(err)
}

// Branches lists the branches of a repository.
func (r *Gateway) Branches(ctx context.Context, ownerID, repository string) ([]contractprojects.Branch, error) {
	branches, err := r.memory.Branches(ctx, ownerID, repository)
	out := make([]contractprojects.Branch, 0, len(branches))
	for _, b := range branches {
		out = append(out, contractprojects.Branch(b))
	}
	return out, contractError(err)
}

// Commits lists the commits of a branch of a repository.
func (r *Gateway) Commits(ctx context.Context, ownerID, repository, branch string) ([]contractprojects.Commit, error) {
	commits, err := r.memory.Commits(ctx, ownerID, repository, branch)
	out := make([]contractprojects.Commit, 0, len(commits))
	for _, c := range commits {
		out = append(out, contractprojects.Commit(c))
	}
	return out, contractError(err)
}

// CreateProject creates a Project for the Owner.
func (r *Gateway) CreateProject(ctx context.Context, request contractprojects.CreateProject) (contractprojects.Project, error) {
	project, err := r.memory.CreateProject(ctx, app.CreateProject{ID: request.ID, OwnerID: request.OwnerID, Name: request.Name, Description: request.Description})
	return contractprojects.Project(project), contractError(err)
}

// UpdateProject renames a Project of the Owner and changes its description.
func (r *Gateway) UpdateProject(ctx context.Context, request contractprojects.UpdateProject) (contractprojects.Project, error) {
	project, err := r.memory.UpdateProject(ctx, app.UpdateProject{ID: request.ID, OwnerID: request.OwnerID, Name: request.Name, Description: request.Description})
	return contractprojects.Project(project), contractError(err)
}

// DeleteProject removes a Project of the Owner.
func (r *Gateway) DeleteProject(ctx context.Context, id, ownerID string) (bool, error) {
	deleted, err := r.memory.DeleteProject(ctx, id, ownerID)
	return deleted, contractError(err)
}

// ActivateSnapshot makes a Snapshot the active one of its Project.
func (r *Gateway) ActivateSnapshot(ctx context.Context, ownerID, projectID, snapshotID string) (bool, error) {
	activated, err := r.memory.ActivateSnapshot(ctx, ownerID, projectID, snapshotID)
	return activated, contractError(err)
}

// SetEntryPoint records the Entrypoint of the Project's active Snapshot.
func (r *Gateway) SetEntryPoint(ctx context.Context, request contractprojects.SetEntryPoint) error {
	return contractError(r.memory.SetEntryPoint(ctx, request.OwnerID, request.ProjectID, request.SnapshotID, request.EntryPoint))
}

// RequestImport queues an Import of a commit into the Project.
func (r *Gateway) RequestImport(ctx context.Context, request contractprojects.RequestImport) (string, error) {
	jobID, err := r.memory.RequestImport(ctx, app.ImportRequest{OwnerID: request.OwnerID, ProjectID: request.ProjectID, Repository: request.Repository, Branch: request.Branch, Commit: request.Commit})
	return jobID, contractError(err)
}

// RetryImport queues a new Import like the failed one.
func (r *Gateway) RetryImport(ctx context.Context, ownerID, jobID string) (string, error) {
	newJobID, err := r.memory.RetryImport(ctx, ownerID, jobID)
	return newJobID, contractError(err)
}

// FindImportJob returns one Import of the Owner, if it exists.
func (r *Gateway) FindImportJob(ctx context.Context, id, ownerID string) (contractprojects.ImportJob, bool, error) {
	job, found, err := r.memory.FindImportJob(ctx, id, ownerID)
	return contractprojects.ImportJob(job), found, contractError(err)
}

// contractError turns a refusal the Memory reports into the contract's error with
// the same meaning: a missing record is not found, a clash with what is running is
// a conflict, and anything else the Account asked for wrongly is invalid.
func contractError(err error) error {
	var reply *app.ReplyError
	if !errors.As(err, &reply) {
		return err
	}
	message := reply.Error()
	switch {
	case strings.HasSuffix(message, "not found"):
		return contract.Errorf(contract.StatusNotFound, "%s", message)
	case strings.Contains(message, "in progress"), message == "project could not be created":
		return contract.Errorf(contract.StatusConflict, "%s", message)
	default:
		return contract.Errorf(contract.StatusInvalid, "%s", message)
	}
}
