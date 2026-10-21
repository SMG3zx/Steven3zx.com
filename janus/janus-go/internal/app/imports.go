package app

import "context"

// ImportRequest asks for a commit of a repository to be imported into a Project.
type ImportRequest struct {
	OwnerID, ProjectID, Repository, Branch, Commit string
}

// Imports brings a repository's files into a Project as a Snapshot. The
// projects service runs each Import as a job that survives restarts, and
// audits it; this use case asks it to.
type Imports struct {
	catalog Catalog
}

// NewImports returns an Imports that queues Imports in catalog.
func NewImports(catalog Catalog) *Imports {
	return &Imports{catalog: catalog}
}

// Request checks the repository, branch and commit and queues an Import of them
// into the Project. It returns the Import's job ID, and an error whose text is
// meant for the Account when the Import cannot start.
func (i *Imports) Request(ctx context.Context, request ImportRequest) (string, error) {
	return i.catalog.RequestImport(ctx, request)
}

// Retry queues a new Import like a failed one, with the same Project,
// repository, branch and commit.
func (i *Imports) Retry(ctx context.Context, owner, jobID string) (string, error) {
	return i.catalog.RetryImport(ctx, owner, jobID)
}

// SetEntryPoint records the Entrypoint the Account confirmed for the Project's
// active Snapshot, when it is one of the candidates the scan found.
func (i *Imports) SetEntryPoint(ctx context.Context, owner, projectID, snapshotID, entryPoint string) error {
	return i.catalog.SetEntryPoint(ctx, owner, projectID, snapshotID, entryPoint)
}
