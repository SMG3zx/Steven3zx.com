package projects

import (
	"context"
	"fmt"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"

	"janus/actors/github"
	"janus/actors/jobrunner"
)

type RequestImport struct {
	OwnerID, ProjectID, Repository, Branch, Commit string
}
type ImportRequested struct {
	JobID string
	Err   error
}

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

type SetEntryPoint struct{ OwnerID, ProjectID, SnapshotID, EntryPoint string }
type EntryPointSet struct{ Err error }
type RunImport struct {
	JobID, OwnerID, ProjectID string
	Repository                github.Repository
	Branch, Commit            string
	Token                     string
}

// importFinished is sent by an import worker's task to the worker itself when
// the import ends, with the error it ended with, if any.
type importFinished struct {
	Import *RunImport
	Err    error
}
