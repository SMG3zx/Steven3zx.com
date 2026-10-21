// Package projectsnats is the app.Catalog adapter that asks the projects
// service over NATS. It translates app types to the projects contract, a
// refusal of the service (400, 404, 409) to an *app.ReplyError carrying the
// service's sentence, and reassembles files the service sends in pieces.
package projectsnats

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go"

	"janus/internal/app"
	"janus/internal/contract"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natsx"
)

// Client is an app.Catalog over a NATS connection.
type Client struct{ conn *nats.Conn }

var _ app.Catalog = (*Client)(nil)

// New returns a Client that sends its requests on conn.
func New(conn *nats.Conn) *Client { return &Client{conn: conn} }

// call sends one request and turns a failure the service reported back into
// the error the port documents.
func (c *Client) call(ctx context.Context, subject string, request, reply any) error {
	err := natsx.Call(ctx, c.conn, subject, request, reply)
	var remote *natsx.RemoteError
	if !errors.As(err, &remote) {
		return err
	}
	switch remote.Status {
	case contract.StatusInvalid, contract.StatusNotFound, contract.StatusConflict:
		return &app.ReplyError{Err: errors.New(remote.Message)}
	case contract.StatusUnavailable:
		// GitHub, or the service itself, could not be reached; the message
		// says which, and the gateway shows it.
		return errors.New(remote.Message)
	}
	return contract.FromStatus(remote.Status, remote.Message)
}

func project(p contractprojects.Project) app.Project { return app.Project(p) }

func (c *Client) CreateProject(ctx context.Context, request app.CreateProject) (app.Project, error) {
	var created contractprojects.Project
	err := c.call(ctx, contractprojects.SubjectCreateProject, contractprojects.CreateProject{ID: request.ID, OwnerID: request.OwnerID, Name: request.Name, Description: request.Description}, &created)
	return project(created), err
}

func (c *Client) UpdateProject(ctx context.Context, request app.UpdateProject) (app.Project, error) {
	var updated contractprojects.Project
	err := c.call(ctx, contractprojects.SubjectUpdateProject, contractprojects.UpdateProject{ID: request.ID, OwnerID: request.OwnerID, Name: request.Name, Description: request.Description}, &updated)
	return project(updated), err
}

func (c *Client) DeleteProject(ctx context.Context, id, ownerID string) (bool, error) {
	var deleted contractprojects.ProjectDeleted
	err := c.call(ctx, contractprojects.SubjectDeleteProject, contractprojects.ProjectKey{OwnerID: ownerID, ProjectID: id}, &deleted)
	return deleted.Deleted, err
}

func (c *Client) ListProjects(ctx context.Context, ownerID string) ([]app.Project, error) {
	var list contractprojects.Projects
	if err := c.call(ctx, contractprojects.SubjectListProjects, contractprojects.Owner{OwnerID: ownerID}, &list); err != nil {
		return nil, err
	}
	projects := make([]app.Project, 0, len(list.Projects))
	for _, p := range list.Projects {
		projects = append(projects, project(p))
	}
	return projects, nil
}

func (c *Client) GetProject(ctx context.Context, id, ownerID string) (app.Project, bool, error) {
	var found contractprojects.ProjectFound
	err := c.call(ctx, contractprojects.SubjectGetProject, contractprojects.ProjectKey{OwnerID: ownerID, ProjectID: id}, &found)
	return project(found.Project), found.Found, err
}

func (c *Client) ListSnapshots(ctx context.Context, ownerID, projectID string) ([]app.ProjectSnapshot, error) {
	var list contractprojects.Snapshots
	if err := c.call(ctx, contractprojects.SubjectListSnapshots, contractprojects.ProjectKey{OwnerID: ownerID, ProjectID: projectID}, &list); err != nil {
		return nil, err
	}
	snapshots := make([]app.ProjectSnapshot, 0, len(list.Snapshots))
	for _, s := range list.Snapshots {
		snapshots = append(snapshots, app.ProjectSnapshot(s))
	}
	return snapshots, nil
}

func (c *Client) ActivateSnapshot(ctx context.Context, ownerID, projectID, snapshotID string) (bool, error) {
	var activated contractprojects.Activated
	err := c.call(ctx, contractprojects.SubjectActivateSnapshot, contractprojects.SnapshotKey{OwnerID: ownerID, ProjectID: projectID, SnapshotID: snapshotID}, &activated)
	return activated.Activated, err
}

func (c *Client) SetEntryPoint(ctx context.Context, ownerID, projectID, snapshotID, entryPoint string) error {
	return c.call(ctx, contractprojects.SubjectSetEntryPoint, contractprojects.SetEntryPoint{OwnerID: ownerID, ProjectID: projectID, SnapshotID: snapshotID, EntryPoint: entryPoint}, nil)
}

// ListProjectFiles asks for pages until it has page.Limit whole files or the
// Snapshot has no more, joining the pieces of a file the service had to cut.
func (c *Client) ListProjectFiles(ctx context.Context, page app.ListProjectFiles) ([]app.SnapshotFile, error) {
	limit := page.Limit
	if limit <= 0 {
		limit = contractprojects.DefaultFilePage
	}
	files := make([]app.SnapshotFile, 0)
	offset, contentOffset := page.Offset, 0
	var partial *app.SnapshotFile
	for len(files) < limit {
		var got contractprojects.Files
		request := contractprojects.SnapshotFiles{OwnerID: page.OwnerID, ProjectID: page.ProjectID, SnapshotID: page.SnapshotID, Offset: offset, ContentOffset: contentOffset, Limit: limit - len(files)}
		if err := c.call(ctx, contractprojects.SubjectSnapshotFiles, request, &got); err != nil {
			return nil, err
		}
		for _, f := range got.Files {
			if partial == nil {
				partial = &app.SnapshotFile{Path: f.Path, Mode: f.Mode, Hash: f.Hash, Content: make([]byte, 0, f.Size)}
			}
			partial.Content = append(partial.Content, f.Content...)
			if int64(len(partial.Content)) < f.Size {
				contentOffset = len(partial.Content)
				continue
			}
			files = append(files, *partial)
			partial, offset, contentOffset = nil, offset+1, 0
		}
		if !got.More || len(got.Files) == 0 {
			break
		}
	}
	return files, nil
}

func (c *Client) RequestImport(ctx context.Context, request app.ImportRequest) (string, error) {
	var queued contractprojects.ImportQueued
	err := c.call(ctx, contractprojects.SubjectRequestImport, contractprojects.RequestImport(request), &queued)
	return queued.JobID, err
}

func (c *Client) RetryImport(ctx context.Context, ownerID, jobID string) (string, error) {
	var queued contractprojects.ImportQueued
	err := c.call(ctx, contractprojects.SubjectRetryImport, contractprojects.ImportKey{OwnerID: ownerID, JobID: jobID}, &queued)
	return queued.JobID, err
}

func (c *Client) FindImportJob(ctx context.Context, id, ownerID string) (app.ImportJob, bool, error) {
	var found contractprojects.ImportFound
	err := c.call(ctx, contractprojects.SubjectFindImport, contractprojects.ImportKey{OwnerID: ownerID, JobID: id}, &found)
	return app.ImportJob(found.Job), found.Found, err
}

func (c *Client) ActiveImports(ctx context.Context, ownerID string) ([]app.ActiveJob, error) {
	var list contractprojects.ActiveImports
	if err := c.call(ctx, contractprojects.SubjectActiveImports, contractprojects.Owner{OwnerID: ownerID}, &list); err != nil {
		return nil, err
	}
	active := make([]app.ActiveJob, 0, len(list.Imports))
	for _, job := range list.Imports {
		active = append(active, app.ActiveJob{Kind: "import", ID: job.ID, ProjectID: job.ProjectID, Status: job.Status})
	}
	return active, nil
}

func (c *Client) DeleteOwner(ctx context.Context, ownerID string) error {
	return c.call(ctx, contractprojects.SubjectDeleteOwner, contractprojects.DeleteOwner{OwnerID: ownerID}, nil)
}

func (c *Client) Repositories(ctx context.Context, ownerID string) (app.RepositoryList, error) {
	var list contractprojects.Repositories
	if err := c.call(ctx, contractprojects.SubjectRepositories, contractprojects.Owner{OwnerID: ownerID}, &list); err != nil {
		return app.RepositoryList{}, err
	}
	repositories := make([]app.Repository, 0, len(list.Repositories))
	for _, r := range list.Repositories {
		repositories = append(repositories, app.Repository(r))
	}
	return app.RepositoryList{Repositories: repositories, Connected: list.Connected}, nil
}

func (c *Client) Branches(ctx context.Context, ownerID, repository string) ([]app.Branch, error) {
	var list contractprojects.Branches
	if err := c.call(ctx, contractprojects.SubjectBranches, contractprojects.RepositoryKey{OwnerID: ownerID, Repository: repository}, &list); err != nil {
		return nil, err
	}
	branches := make([]app.Branch, 0, len(list.Branches))
	for _, b := range list.Branches {
		branches = append(branches, app.Branch(b))
	}
	return branches, nil
}

func (c *Client) Commits(ctx context.Context, ownerID, repository, branch string) ([]app.Commit, error) {
	var list contractprojects.Commits
	if err := c.call(ctx, contractprojects.SubjectCommits, contractprojects.RepositoryKey{OwnerID: ownerID, Repository: repository, Branch: branch}, &list); err != nil {
		return nil, err
	}
	commits := make([]app.Commit, 0, len(list.Commits))
	for _, c := range list.Commits {
		commits = append(commits, app.Commit(c))
	}
	return commits, nil
}
