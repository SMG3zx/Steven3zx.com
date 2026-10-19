package projects

import (
	"context"
	"fmt"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"

	"janus/actors/jobrunner"
	"janus/actors/persistence"
)

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

// Drain makes the projects actor refuse new imports and reports how many are
// still running. Send it again until Pending is zero, or call WaitDrained.
type Drain struct{}
type Drained struct {
	Err     error
	Pending int
}

// WaitDrained stops new imports on the projects actor at pid and waits for the
// running ones to finish, or until ctx ends.
func WaitDrained(ctx context.Context, pid *goakt.PID) error {
	return jobrunner.Await(ctx, 50*time.Millisecond, func(ctx context.Context) (int, error) {
		response, err := goakt.Ask(ctx, pid, &Drain{}, 5*time.Second)
		if err != nil {
			return 0, err
		}
		drained, ok := response.(*Drained)
		if !ok {
			return 0, fmt.Errorf("unexpected projects actor response %T", response)
		}
		return drained.Pending, drained.Err
	})
}

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
