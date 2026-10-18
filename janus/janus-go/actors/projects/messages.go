package projects

import "janus/actors/persistence"

type ListRepositories struct{ OwnerID string }
type RepositoriesListed struct {
	Repositories []Repository `json:"repositories"`
	Connected    bool         `json:"connected"`
	Err          error        `json:"-"`
}
type ListBranches struct{ OwnerID, Repository string }
type BranchesListed struct {
	Branches []Branch `json:"branches"`
	Err      error    `json:"-"`
}
type ListCommits struct{ OwnerID, Repository, Branch string }
type CommitsListed struct {
	Commits []Commit `json:"commits"`
	Err     error    `json:"-"`
}
type RequestImport struct {
	OwnerID, ProjectID, Repository, Branch, Commit string
}
type ImportRequested struct {
	JobID string
	Err   error
}
type Disconnect struct{ OwnerID string }
type Disconnected struct{ Err error }
type Drain struct{}
type Drained struct{ Err error }

type Create struct{ Project persistence.CreateProject }
type Created struct {
	Project persistence.Project
	Err     error
}
type UpdateProject struct{ OwnerID, ProjectID, Name, Description string }
type Updated struct {
	Project persistence.Project
	Err     error
}
type DeleteProject struct{ OwnerID, ProjectID string }
type Deleted struct {
	Deleted bool
	Err     error
}
type List struct{ OwnerID string }
type Listed struct {
	Projects []persistence.Project
	Err      error
}
type Get struct{ OwnerID, ProjectID string }
type Found struct {
	Project persistence.Project
	Exists  bool
	Err     error
}
type Job struct{ OwnerID, JobID string }
type JobFound struct {
	Job    persistence.ImportJob
	Exists bool
	Err    error
}
type SetEntryPoint struct{ OwnerID, ProjectID, SnapshotID, EntryPoint string }
type EntryPointSet struct{ Err error }
type History struct{ OwnerID, ProjectID string }
type HistoryListed struct {
	Snapshots []persistence.ProjectSnapshot
	Err       error
}
type ActivateSnapshot struct{ OwnerID, ProjectID, SnapshotID string }
type SnapshotActivated struct {
	Activated bool
	Err       error
}

type RunImport struct {
	JobID, OwnerID, ProjectID string
	Repository                Repository
	Branch, Commit            string
	Token                     string
}
