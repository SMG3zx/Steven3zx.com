// Package app holds the Janus use cases and the ports they need.
package app

import (
	"context"
)

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
