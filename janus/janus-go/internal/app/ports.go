// Package app holds the Janus use cases and the ports they need.
package app

import "context"

// Store is what callers use to read and write Janus data. It returns typed
// values and plain errors, so no caller sends a message, matches the reply type
// or checks an error field. Adapters implement it over a database or, for tests, over maps. The
// storetest package holds the contract suite every adapter must pass.
//
// Every method that reports an absent record returns found=false with a nil
// error, and reserves the error for failures.
type Store interface {
	// Identity: users, Provider Identities and the GitHub Connection.
	CreateLocalUser(ctx context.Context, user CreateLocalUser) error
	FindLocalUser(ctx context.Context, email string) (LocalUserFound, error)
	FindUser(ctx context.Context, id string) (UserFound, error)
	ResolveGoogleIdentity(ctx context.Context, identity ResolveGoogleIdentity) (GoogleIdentityResolved, error)
	ResolveGitHubLogin(ctx context.Context, login ResolveGitHubLogin) (GitHubLoginResolved, error)
	ConnectGitHub(ctx context.Context, connection ConnectGitHub) error
	FindGitHubToken(ctx context.Context, userID string) (token string, connected bool, err error)
	DisconnectGitHub(ctx context.Context, userID string) error

	// Projects, Snapshots, files and Entrypoints.
	CreateProject(ctx context.Context, project CreateProject) (Project, error)
	UpdateProject(ctx context.Context, project UpdateProject) (Project, error)
	DeleteProject(ctx context.Context, id, ownerID string) (deleted bool, err error)
	ListProjects(ctx context.Context, ownerID string) ([]Project, error)
	GetProject(ctx context.Context, id, ownerID string) (project Project, found bool, err error)
	ListSnapshots(ctx context.Context, ownerID, projectID string) ([]ProjectSnapshot, error)
	ActivateSnapshot(ctx context.Context, ownerID, projectID, snapshotID string) (activated bool, err error)
	SetProjectEntryPoint(ctx context.Context, entry SetProjectEntryPoint) error
	ListProjectFiles(ctx context.Context, page ListProjectFiles) ([]SnapshotFile, error)

	// Jobs: imports, Staging Snapshots, WASM Builds and Resume.
	CreateImportJob(ctx context.Context, job CreateImportJob) error
	UpdateImportJob(ctx context.Context, update UpdateImportJob) error
	FindImportJob(ctx context.Context, id, ownerID string) (job ImportJob, found bool, err error)
	BeginSnapshot(ctx context.Context, snapshot BeginSnapshot) error
	AddSnapshotFiles(ctx context.Context, snapshotID string, files []SnapshotFile) error
	CompleteSnapshot(ctx context.Context, snapshot CompleteSnapshot) error
	DeleteStagingSnapshot(ctx context.Context, snapshotID string) error
	FindStagingSnapshot(ctx context.Context, jobID string) (StagingSnapshotFound, error)
	DiscardJobStaging(ctx context.Context, jobID string) error
	CreateWASMBuild(ctx context.Context, build CreateWASMBuild) error
	UpdateWASMBuild(ctx context.Context, update UpdateWASMBuild) error
	CompleteWASMBuild(ctx context.Context, build CompleteWASMBuild) error
	FindWASMBuild(ctx context.Context, id, ownerID string) (build WASMBuild, found bool, err error)
	FindWASMBuildArtifact(ctx context.Context, id, ownerID string) (WASMBuildArtifact, error)
	ListResumableImports(ctx context.Context) ([]ResumableImport, error)
	ListResumableBuilds(ctx context.Context) ([]ResumableBuild, error)
	ListActiveJobs(ctx context.Context, ownerID string) ([]ActiveJob, error)

	// DeleteAccount deletes an Account and everything it owns. Without
	// cancelActiveJobs an Account with unfinished jobs is left untouched and the
	// result lists those jobs.
	DeleteAccount(ctx context.Context, userID string, cancelActiveJobs bool) (AccountDeleted, error)
}

// Builder compiles a workspace of one Language to an Artifact. The language is
// matched without regard to case.
//
// A workspace without the manifest the Language's Template requires fails with
// a *ManifestError. A Language or Target no Template covers fails with a plain
// error. The BuildResult's Log is set on failure too, and its Artifact is a
// compressed ZIP that artifact.Module can open.
type Builder interface {
	Build(ctx context.Context, language string, request BuildRequest) (BuildResult, error)
}

// Runner runs a WASI module with the given arguments under limits. limits.Timeout
// bounds the run, and an adapter may also stop early when ctx ends. It reports
// what the module wrote and what the run consumed. A module that cannot be
// loaded, traps or exceeds a limit fails with an error, and the RunResult still
// holds the output and Usage measured up to that point. Stdin is empty.
type Runner interface {
	Run(ctx context.Context, module []byte, args []string, limits RunLimits) (RunResult, error)
}

// Source fetches a repository at a commit. Open makes the commit's file tree
// readable and Close releases whatever Open holds; callers read the tree before
// closing it. An unknown commit or an unreadable repository fails Open with a
// *SourceError. A tree that breaks a size limit fails Files with an error.
type Source interface {
	Open(ctx context.Context, request SourceRequest) (SourceTree, error)
}

// SourceTree is the file tree of one commit.
type SourceTree interface {
	// Files returns the regular files and submodule pointers of the tree, in no
	// particular order. When want is not nil only the files whose slash-separated
	// paths it lists are read. Paths are clean and relative.
	Files(want map[string]bool) ([]SnapshotFile, error)
	Close() error
}

// GitHost reads repository metadata from a code host on behalf of an Account,
// using the Account's access token. Repository, Branches and Commits fail with
// an error for a name that is not "owner/name". Commits lists at most the 50
// newest commits of a branch, newest first.
type GitHost interface {
	Repositories(ctx context.Context, token string) ([]Repository, error)
	Repository(ctx context.Context, token, fullName string) (Repository, error)
	Branches(ctx context.Context, token, fullName string) ([]Branch, error)
	Commits(ctx context.Context, token, fullName, branch string) ([]Commit, error)
}

// Audit records Audit Events. Recording never fails the caller and never
// blocks it for long; an adapter that cannot write an event drops it. An event
// with no Level and no Name is recorded as info under a generic name, and every
// Field is recorded.
type Audit interface {
	Record(ctx context.Context, event AuditEvent)
}
