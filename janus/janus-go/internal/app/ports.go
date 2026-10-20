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
