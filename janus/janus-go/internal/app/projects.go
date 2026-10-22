package app

import "context"

// Projects reads and changes the Projects an Account owns, their Snapshots and
// their Imports, in the projects service. Every call is scoped to the owning
// Account, so an Account never sees another's records.
type Projects struct {
	catalog Catalog
}

// NewProjects returns a Projects over catalog.
func NewProjects(catalog Catalog) *Projects {
	return &Projects{catalog: catalog}
}

// Create adds a Project to the Account.
func (p *Projects) Create(ctx context.Context, project CreateProject) (Project, error) {
	return p.catalog.CreateProject(ctx, project)
}

// Update changes the name and description of a Project of the Account.
func (p *Projects) Update(ctx context.Context, project UpdateProject) (Project, error) {
	return p.catalog.UpdateProject(ctx, project)
}

// Delete removes a Project of the Account and everything it holds. Its WASM
// Builds and Deployments go once the projects service says it is gone.
func (p *Projects) Delete(ctx context.Context, id, ownerID string) (bool, error) {
	return p.catalog.DeleteProject(ctx, id, ownerID)
}

// ActivateSnapshot makes a Snapshot the active one of its Project.
func (p *Projects) ActivateSnapshot(ctx context.Context, ownerID, projectID, snapshotID string) (bool, error) {
	return p.catalog.ActivateSnapshot(ctx, ownerID, projectID, snapshotID)
}

// ImportJob returns one Import of the Account, if it exists.
func (p *Projects) ImportJob(ctx context.Context, id, ownerID string) (ImportJob, bool, error) {
	return p.catalog.FindImportJob(ctx, id, ownerID)
}
