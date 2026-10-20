package app

import "context"

// Projects reads and changes the Projects an Account owns, their Snapshots and
// their import jobs. Every call is scoped to the owning Account, so an Account
// never sees another's records.
type Projects struct {
	store Store
}

// NewProjects returns a Projects that reads and writes through store.
func NewProjects(store Store) *Projects {
	return &Projects{store: store}
}

// List returns the Projects the Account owns.
func (p *Projects) List(ctx context.Context, ownerID string) ([]Project, error) {
	return p.store.ListProjects(ctx, ownerID)
}

// Create adds a Project to the Account.
func (p *Projects) Create(ctx context.Context, project CreateProject) (Project, error) {
	return p.store.CreateProject(ctx, project)
}

// Get returns one Project of the Account, if it exists.
func (p *Projects) Get(ctx context.Context, id, ownerID string) (Project, bool, error) {
	return p.store.GetProject(ctx, id, ownerID)
}

// Update changes the name and description of a Project of the Account.
func (p *Projects) Update(ctx context.Context, project UpdateProject) (Project, error) {
	return p.store.UpdateProject(ctx, project)
}

// Delete removes a Project of the Account and everything it holds.
func (p *Projects) Delete(ctx context.Context, id, ownerID string) (bool, error) {
	return p.store.DeleteProject(ctx, id, ownerID)
}

// Snapshots lists the Snapshots of a Project of the Account.
func (p *Projects) Snapshots(ctx context.Context, ownerID, projectID string) ([]ProjectSnapshot, error) {
	return p.store.ListSnapshots(ctx, ownerID, projectID)
}

// ActivateSnapshot makes a Snapshot the active one of its Project.
func (p *Projects) ActivateSnapshot(ctx context.Context, ownerID, projectID, snapshotID string) (bool, error) {
	return p.store.ActivateSnapshot(ctx, ownerID, projectID, snapshotID)
}

// ImportJob returns one import job of the Account, if it exists.
func (p *Projects) ImportJob(ctx context.Context, id, ownerID string) (ImportJob, bool, error) {
	return p.store.FindImportJob(ctx, id, ownerID)
}
