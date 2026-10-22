// Package app holds the Janus use cases and the ports they need.
package app

import (
	"context"
)

// Store is what callers use to read and write Janus data. It returns typed
// values and plain errors, so no caller sends a message, matches the reply type
// or checks an error field. Adapters implement it over a database or, for tests, over maps. The
// storetest package holds the contract suite every adapter must pass.
//
// Every method that reports an absent record returns found=false with a nil
// error, and reserves the error for failures.
type Store interface {
	// AddOwner records that the User id may own records here, so Projects,
	// Enrollment Codes and the rest can refer to it and go with it when the
	// Account is deleted. Adding an Owner that exists changes nothing. Users
	// themselves live behind Identities.
	AddOwner(ctx context.Context, id string) error

	// The Projects and Snapshots WASM Builds and Deployments belong to. They
	// live in the projects service; the Store keeps a reference to each one it
	// has records for, so those records can refer to it and go with it.
	// AddProject and AddSnapshot change nothing when the reference exists.
	// RemoveProject and RemoveSnapshot delete the reference and everything that
	// refers to it, and change nothing when there is none.
	AddProject(ctx context.Context, project ProjectRef) error
	AddSnapshot(ctx context.Context, snapshot SnapshotRef) error
	FindSnapshot(ctx context.Context, id, ownerID string) (snapshot SnapshotRef, found bool, err error)
	// ListOwnedProjects returns the IDs of the Owner's referenced Projects.
	ListOwnedProjects(ctx context.Context, ownerID string) ([]string, error)
	RemoveProject(ctx context.Context, id string) error
	RemoveSnapshot(ctx context.Context, id string) error

	// WASM Builds, as the references Deployments need. The builds service owns
	// them: it publishes each Build's and Check's state on EVENTS, and ApplyWASMBuild
	// and ApplyCheck store what it says. Applying a state again changes nothing, and
	// a Build whose Project or Snapshot is not referenced here is not kept.
	ApplyWASMBuild(ctx context.Context, build WASMBuildChange) error
	ApplyCheck(ctx context.Context, check CheckChange) error
	FindWASMBuild(ctx context.Context, id, ownerID string) (build WASMBuild, found bool, err error)
	ListActiveJobs(ctx context.Context, ownerID string) ([]ActiveJob, error)
	FindCheck(ctx context.Context, buildID, ownerID string) (check Check, found bool, err error)

	// DeleteAccount deletes an Account and everything it owns. Without
	// cancelActiveJobs an Account with unfinished jobs is left untouched and the
	// result lists those jobs.
	DeleteAccount(ctx context.Context, userID string, cancelActiveJobs bool) (AccountDeleted, error)
}

// Identities is where Users, their sign-in methods and their GitHub
// Connection live: the identity service. A request refused because it clashes
// with what is stored, such as an email that already has a local account or a
// GitHub account linked to another Account, fails with a *Conflict. An absent
// User is found=false with a nil error.
type Identities interface {
	CreateLocalUser(ctx context.Context, user CreateLocalUser) error
	FindLocalUser(ctx context.Context, email string) (LocalUserFound, error)
	FindUser(ctx context.Context, id string) (UserFound, error)
	// DeleteUser removes a User and its sign-in methods and GitHub Connection.
	// Deleted is false when there was no such User.
	DeleteUser(ctx context.Context, id string) (deleted bool, err error)
	// DeleteAccount starts deleting the User's Account, or reports how far a
	// deletion has got. Without cancelActiveJobs an Account with unfinished jobs
	// is refused and the status lists them.
	DeleteAccount(ctx context.Context, userID string, cancelActiveJobs bool) (AccountDeletion, error)
	ResolveGoogleIdentity(ctx context.Context, identity ResolveGoogleIdentity) (GoogleIdentityResolved, error)
	ResolveGitHubLogin(ctx context.Context, login ResolveGitHubLogin) (GitHubLoginResolved, error)
	ConnectGitHub(ctx context.Context, connection ConnectGitHub) error
	FindGitHubToken(ctx context.Context, userID string) (token string, connected bool, err error)
	DisconnectGitHub(ctx context.Context, userID string) error
}

// WASMBuilds is the builds service as the single process uses it: WASM Builds
// of a Project's Snapshot, their Checks and their Artifacts. A request the
// service refuses fails with a plain error whose text is meant for the Account,
// or a contract error. A Build or Artifact that is not the Owner's is absent.
type WASMBuilds interface {
	// StartBuild queues a Build and returns it.
	StartBuild(ctx context.Context, build StartBuild) (WASMBuild, error)
	// RetryBuild queues a new Build of a failed Build's Snapshot.
	RetryBuild(ctx context.Context, owner, buildID string) (WASMBuild, error)
	FindBuild(ctx context.Context, owner, buildID string) (WASMBuild, bool, error)
	// FindCheck returns the Check of one of the Owner's Builds; found is false
	// when the Build has none yet.
	FindCheck(ctx context.Context, owner, buildID string) (Check, bool, error)
	// Artifact returns the Artifact of a complete Build; Found is false when
	// it has none.
	Artifact(ctx context.Context, owner, buildID string) (WASMBuildArtifact, error)
	// DeleteOwner cancels the Owner's running Builds and deletes its Builds and
	// Artifacts. It is idempotent.
	DeleteOwner(ctx context.Context, owner string) error
}

// BuildArtifacts reads the Artifacts of an Account's WASM Builds, which Runs
// read.
type BuildArtifacts interface {
	Artifact(ctx context.Context, owner, buildID string) (WASMBuildArtifact, error)
}

// Fleet is the fleet service as the single process uses it: enrollment of
// Agents, the Agents on the tailnet, their Deployments and the Usage they report.
// A request the service refuses fails with an error whose text is meant for the
// Account, or with ErrAgentNotFound, ErrAgentUnreachable or an *AgentError. An
// Agent or Deployment that is not the Owner's is absent.
type Fleet interface {
	IssueCode(ctx context.Context, owner string) (IssuedCode, error)
	Enroll(ctx context.Context, code, name string) (Enrollment, error)
	ListAgents(ctx context.Context, owner string) ([]AgentStatus, error)
	// RemoveAgent deletes the Owner's Agent and takes its node off the tailnet.
	// removed is false for an Agent the Owner does not have.
	RemoveAgent(ctx context.Context, owner, agentID string) (removed bool, err error)
	// AgentStatuses returns the last state each of the Owner's Agents reported,
	// by Agent ID.
	AgentStatuses(ctx context.Context, owner string) (map[string]AgentStatusReport, error)
	PauseAgent(ctx context.Context, owner, agentID string) error
	ResumeAgent(ctx context.Context, owner, agentID string) error
	// Usage returns the Owner's recorded runs that match query, newest first.
	Usage(ctx context.Context, query RunUsageQuery) ([]RunUsageRecord, error)
	// CreateDeployment queues a Deployment and returns its ID.
	CreateDeployment(ctx context.Context, deployment NewDeployment) (id string, err error)
	// RollbackDeployment queues a Deployment of a stopped one's WASM Build.
	RollbackDeployment(ctx context.Context, owner, id string) (string, error)
	StopDeployment(ctx context.Context, owner, id string) error
	FindDeployment(ctx context.Context, owner, id string) (deployment Deployment, found bool, err error)
	// RevokeAgents takes the Owner's Agents off the tailnet and keeps their
	// records. TeardownDeployments stops the Owner's running Deployments.
	// DeleteOwner removes everything the Owner has in the service. Accounts calls
	// them in that order when it deletes an Account, and each can be repeated.
	RevokeAgents(ctx context.Context, owner string) error
	TeardownDeployments(ctx context.Context, owner string) error
	DeleteOwner(ctx context.Context, owner string) error
}

// Runner runs a WASI module under input.Limits. Limits.Timeout bounds the run,
// and an adapter may also stop early when ctx ends. It reports what the module
// wrote and what the run consumed. A module that cannot be loaded, traps or
// exceeds a limit fails with an error, and the RunResult still holds the output
// and Usage measured up to that point. A run that spends all its fuel fails with
// an error matching ErrOutOfFuel, and one stopped by the timeout with an error
// matching ErrRunTimeout.
//
// The module reads input.Stdin and sees input.Env as its environment and
// input.Args as its arguments. When input.Dir is set, that host directory is
// the module's one preopened directory (file descriptor 3, named "/"), which it
// may read and write; nothing outside it is reachable. Without Dir the module
// has no files at all. Stdout is cut at input.MaxStdout bytes, 64 KiB when it
// is zero, and StdoutTruncated says so. Stderr is cut at 64 KiB.
type Runner interface {
	Run(ctx context.Context, module []byte, input RunInput) (RunResult, error)
}

// Catalog is where Projects, their Snapshots and files, and their Imports
// live: the projects service. It also lists the GitHub repositories, branches
// and commits an Account can import, with the Account's GitHub Connection.
//
// Every call is scoped to the Owner, and another Owner's record is absent. A
// request the service refused, such as an absent Project, an import that
// cannot run or a deletion while an import is in progress, fails with a
// *ReplyError whose text is a sentence for the Account; any other error is a
// failure to reach the service.
type Catalog interface {
	CreateProject(ctx context.Context, project CreateProject) (Project, error)
	UpdateProject(ctx context.Context, project UpdateProject) (Project, error)
	DeleteProject(ctx context.Context, id, ownerID string) (deleted bool, err error)
	ListProjects(ctx context.Context, ownerID string) ([]Project, error)
	GetProject(ctx context.Context, id, ownerID string) (project Project, found bool, err error)
	ListSnapshots(ctx context.Context, ownerID, projectID string) ([]ProjectSnapshot, error)
	ActivateSnapshot(ctx context.Context, ownerID, projectID, snapshotID string) (activated bool, err error)
	SetEntryPoint(ctx context.Context, ownerID, projectID, snapshotID, entryPoint string) error
	// ListProjectFiles returns up to page.Limit whole files of a ready Snapshot
	// from page.Offset on, ordered by path; fewer means there are no more.
	ListProjectFiles(ctx context.Context, page ListProjectFiles) ([]SnapshotFile, error)

	// RequestImport checks the repository, branch and commit and queues an
	// Import of them, returning its job ID. RetryImport queues a new Import like
	// a failed one.
	RequestImport(ctx context.Context, request ImportRequest) (jobID string, err error)
	RetryImport(ctx context.Context, ownerID, jobID string) (string, error)
	FindImportJob(ctx context.Context, id, ownerID string) (job ImportJob, found bool, err error)
	// ActiveImports lists the Owner's unfinished Imports, as kind "import".
	ActiveImports(ctx context.Context, ownerID string) ([]ActiveJob, error)
	// DeleteOwner cancels the Owner's Imports and deletes its Projects.
	DeleteOwner(ctx context.Context, ownerID string) error

	Repositories(ctx context.Context, ownerID string) (RepositoryList, error)
	Branches(ctx context.Context, ownerID, repository string) ([]Branch, error)
	Commits(ctx context.Context, ownerID, repository, branch string) ([]Commit, error)
}

// Audit records Audit Events. Recording never fails the caller and never
// blocks it for long; an adapter that cannot write an event drops it. An event
// with no Level and no Name is recorded as info under a generic name, and every
// Field is recorded.
type Audit interface {
	Record(ctx context.Context, event AuditEvent)
}

// LocalState is what an Agent keeps on its own machine, so that it serves the
// same Deployments, paused or not, after a restart. It is safe for concurrent
// use, and a pause written by another process using the same storage, such as
// the Agent's command line, is what the next Paused reads.
type LocalState interface {
	// SaveDeployment keeps a Deployment, replacing one with the same ID.
	SaveDeployment(ctx context.Context, deployment HostedDeployment) error
	// DeleteDeployment forgets a Deployment. Forgetting one that is not kept is
	// not an error.
	DeleteDeployment(ctx context.Context, id string) error
	// Deployments returns every kept Deployment, ordered by ID.
	Deployments(ctx context.Context) ([]HostedDeployment, error)
	// SetPaused records whether the Agent is paused.
	SetPaused(ctx context.Context, paused bool) error
	// Paused reads the pause as last recorded through any handle on the same
	// storage. It is false until SetPaused is first called.
	Paused(ctx context.Context) (bool, error)
}
