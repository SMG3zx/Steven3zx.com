package wasm

import "janus/actors/persistence"

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

type Drain struct{}
type Drained struct{ Err error }

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
