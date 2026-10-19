package wasm

import (
	"context"
	"fmt"
	"time"

	"github.com/tochemey/goakt/v4/actor"

	"janus/actors/jobrunner"
	"janus/actors/persistence"
)

type RequestBuild struct {
	OwnerID, ProjectID, Target string
	// SnapshotID selects the Snapshot to build. When empty, the project's active
	// Snapshot is built.
	SnapshotID string
}

type BuildRequested struct {
	JobID string
	Err   error
}

type Build struct{ OwnerID, JobID string }
type BuildFound struct {
	Build persistence.WASMBuild
	Found bool
	Err   error
}

type RunBuild struct {
	OwnerID, ProjectID, SnapshotID string
	JobID, Target, Language        string
}

// Drain makes the build actor refuse new builds and reports how many are still
// running. Send it again until Pending is zero, or call WaitDrained.
type Drain struct{}
type Drained struct {
	Err     error
	Pending int
}

// ResumeBuilds starts every WASM build interrupted by the previous shutdown. It
// runs once at startup.
type ResumeBuilds struct{}

// BuildsResumed reports how many interrupted builds were started again.
type BuildsResumed struct {
	Count int
	Err   error
}

// RetryBuild starts a new build of the same Snapshot as a failed build. The new
// build starts with no resumes.
type RetryBuild struct{ OwnerID, JobID string }

// buildFinished is sent by a build worker's task to the worker itself when the
// build ends, with the error the build ended with, if any.
type buildFinished struct {
	Build *RunBuild
	Err   error
}

// CancelBuilds stops every running build of an Account. Their containers are
// stopped; queued builds are cancelled in the database by the caller.
type CancelBuilds struct{ OwnerID string }

// BuildsCancelled reports how many running builds were signalled.
type BuildsCancelled struct{ Count int }

// WaitDrained stops new builds on the build actor at pid and waits for the
// running ones to finish, or until ctx ends.
func WaitDrained(ctx context.Context, pid *actor.PID) error {
	return jobrunner.Await(ctx, 50*time.Millisecond, func(ctx context.Context) (int, error) {
		response, err := actor.Ask(ctx, pid, &Drain{}, 5*time.Second)
		if err != nil {
			return 0, err
		}
		drained, ok := response.(*Drained)
		if !ok {
			return 0, fmt.Errorf("unexpected build actor response %T", response)
		}
		return drained.Pending, drained.Err
	})
}
