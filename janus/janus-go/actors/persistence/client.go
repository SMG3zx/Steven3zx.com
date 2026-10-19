package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
)

// Store is what callers use to read and write Janus data. It returns typed
// values and plain errors, so no caller sends a message, matches the reply type
// or checks an error field. Client talks to the persistence actor; Memory keeps
// the same data in maps for tests of callers that need no database.
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

// Client is the Store backed by the persistence actor.
type Client struct {
	pid     *goakt.PID
	timeout time.Duration
}

var _ Store = (*Client)(nil)

// NewClient returns a Client that asks the persistence actor at pid and gives
// up on each request after timeout.
func NewClient(pid *goakt.PID, timeout time.Duration) *Client {
	return &Client{pid: pid, timeout: timeout}
}

// ask sends msg and returns the reply as *R. errOf extracts the error field the
// reply carries. A reply of another type is an error.
func ask[R any](c *Client, ctx context.Context, msg any, errOf func(*R) error) (R, error) {
	var zero R
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	response, err := goakt.Ask(requestCtx, c.pid, msg, c.timeout)
	if err != nil {
		return zero, err
	}
	reply, ok := response.(*R)
	if !ok {
		return zero, fmt.Errorf("unexpected persistence response %T", response)
	}
	if replyErr := errOf(reply); replyErr != nil {
		return *reply, &ReplyError{Err: replyErr}
	}
	return *reply, nil
}

// ReplyError is an error the persistence actor itself reported, such as a
// constraint violation, as opposed to a request that never got an answer. Its
// text is the reported error's text.
type ReplyError struct{ Err error }

func (e *ReplyError) Error() string { return e.Err.Error() }
func (e *ReplyError) Unwrap() error { return e.Err }

// IsReply reports whether err is an error that the persistence actor reported,
// and not a transport failure such as a timeout.
func IsReply(err error) bool {
	var reply *ReplyError
	return errors.As(err, &reply)
}

func (c *Client) CreateLocalUser(ctx context.Context, user CreateLocalUser) error {
	_, err := ask(c, ctx, &user, func(r *LocalUserCreated) error { return r.Err })
	return err
}

func (c *Client) FindLocalUser(ctx context.Context, email string) (LocalUserFound, error) {
	return ask(c, ctx, &FindLocalUser{Email: email}, func(r *LocalUserFound) error { return r.Err })
}

func (c *Client) FindUser(ctx context.Context, id string) (UserFound, error) {
	return ask(c, ctx, &FindUser{ID: id}, func(r *UserFound) error { return r.Err })
}

func (c *Client) ResolveGoogleIdentity(ctx context.Context, identity ResolveGoogleIdentity) (GoogleIdentityResolved, error) {
	return ask(c, ctx, &identity, func(r *GoogleIdentityResolved) error { return r.Err })
}

func (c *Client) ResolveGitHubLogin(ctx context.Context, login ResolveGitHubLogin) (GitHubLoginResolved, error) {
	return ask(c, ctx, &login, func(r *GitHubLoginResolved) error { return r.Err })
}

func (c *Client) ConnectGitHub(ctx context.Context, connection ConnectGitHub) error {
	_, err := ask(c, ctx, &connection, func(r *GitHubConnected) error { return r.Err })
	return err
}

func (c *Client) FindGitHubToken(ctx context.Context, userID string) (string, bool, error) {
	found, err := ask(c, ctx, &FindGitHubToken{UserID: userID}, func(r *GitHubTokenFound) error { return r.Err })
	return found.Token, found.Connected, err
}

func (c *Client) DisconnectGitHub(ctx context.Context, userID string) error {
	_, err := ask(c, ctx, &DisconnectGitHub{UserID: userID}, func(r *GitHubDisconnected) error { return r.Err })
	return err
}

func (c *Client) CreateProject(ctx context.Context, project CreateProject) (Project, error) {
	created, err := ask(c, ctx, &project, func(r *ProjectCreated) error { return r.Err })
	return created.Project, err
}

func (c *Client) UpdateProject(ctx context.Context, project UpdateProject) (Project, error) {
	updated, err := ask(c, ctx, &project, func(r *ProjectUpdated) error { return r.Err })
	return updated.Project, err
}

func (c *Client) DeleteProject(ctx context.Context, id, ownerID string) (bool, error) {
	deleted, err := ask(c, ctx, &DeleteProject{ID: id, OwnerID: ownerID}, func(r *ProjectDeleted) error { return r.Err })
	return deleted.Deleted, err
}

func (c *Client) ListProjects(ctx context.Context, ownerID string) ([]Project, error) {
	listed, err := ask(c, ctx, &ListProjects{OwnerID: ownerID}, func(r *ProjectsListed) error { return r.Err })
	return listed.Projects, err
}

func (c *Client) GetProject(ctx context.Context, id, ownerID string) (Project, bool, error) {
	found, err := ask(c, ctx, &GetProject{ID: id, OwnerID: ownerID}, func(r *ProjectFound) error { return r.Err })
	return found.Project, found.Found, err
}

func (c *Client) ListSnapshots(ctx context.Context, ownerID, projectID string) ([]ProjectSnapshot, error) {
	listed, err := ask(c, ctx, &ListSnapshots{OwnerID: ownerID, ProjectID: projectID}, func(r *SnapshotsListed) error { return r.Err })
	return listed.Snapshots, err
}

func (c *Client) ActivateSnapshot(ctx context.Context, ownerID, projectID, snapshotID string) (bool, error) {
	activated, err := ask(c, ctx, &ActivateSnapshot{OwnerID: ownerID, ProjectID: projectID, SnapshotID: snapshotID}, func(r *SnapshotActivated) error { return r.Err })
	return activated.Activated, err
}

func (c *Client) SetProjectEntryPoint(ctx context.Context, entry SetProjectEntryPoint) error {
	_, err := ask(c, ctx, &entry, func(r *ProjectEntryPointSet) error { return r.Err })
	return err
}

func (c *Client) ListProjectFiles(ctx context.Context, page ListProjectFiles) ([]SnapshotFile, error) {
	listed, err := ask(c, ctx, &page, func(r *ProjectFilesListed) error { return r.Err })
	return listed.Files, err
}

func (c *Client) CreateImportJob(ctx context.Context, job CreateImportJob) error {
	_, err := ask(c, ctx, &job, func(r *ImportJobCreated) error { return r.Err })
	return err
}

func (c *Client) UpdateImportJob(ctx context.Context, update UpdateImportJob) error {
	_, err := ask(c, ctx, &update, func(r *ImportJobCreated) error { return r.Err })
	return err
}

func (c *Client) FindImportJob(ctx context.Context, id, ownerID string) (ImportJob, bool, error) {
	found, err := ask(c, ctx, &FindImportJob{ID: id, OwnerID: ownerID}, func(r *ImportJobFound) error { return r.Err })
	return found.Job, found.Found, err
}

func (c *Client) BeginSnapshot(ctx context.Context, snapshot BeginSnapshot) error {
	_, err := ask(c, ctx, &snapshot, func(r *BeginSnapshotResult) error { return r.Err })
	return err
}

func (c *Client) AddSnapshotFiles(ctx context.Context, snapshotID string, files []SnapshotFile) error {
	_, err := ask(c, ctx, &AddSnapshotFiles{SnapshotID: snapshotID, Files: files}, func(r *SnapshotFilesAdded) error { return r.Err })
	return err
}

func (c *Client) CompleteSnapshot(ctx context.Context, snapshot CompleteSnapshot) error {
	_, err := ask(c, ctx, &snapshot, func(r *SnapshotCompleted) error { return r.Err })
	return err
}

func (c *Client) DeleteStagingSnapshot(ctx context.Context, snapshotID string) error {
	_, err := ask(c, ctx, &DeleteStagingSnapshot{SnapshotID: snapshotID}, func(r *StagingSnapshotDeleted) error { return r.Err })
	return err
}

func (c *Client) FindStagingSnapshot(ctx context.Context, jobID string) (StagingSnapshotFound, error) {
	return ask(c, ctx, &FindStagingSnapshot{JobID: jobID}, func(r *StagingSnapshotFound) error { return r.Err })
}

func (c *Client) DiscardJobStaging(ctx context.Context, jobID string) error {
	_, err := ask(c, ctx, &DiscardJobStaging{JobID: jobID}, func(r *JobStagingDiscarded) error { return r.Err })
	return err
}

func (c *Client) CreateWASMBuild(ctx context.Context, build CreateWASMBuild) error {
	_, err := ask(c, ctx, &build, func(r *WASMBuildCreated) error { return r.Err })
	return err
}

func (c *Client) UpdateWASMBuild(ctx context.Context, update UpdateWASMBuild) error {
	_, err := ask(c, ctx, &update, func(r *WASMBuildCreated) error { return r.Err })
	return err
}

func (c *Client) CompleteWASMBuild(ctx context.Context, build CompleteWASMBuild) error {
	_, err := ask(c, ctx, &build, func(r *WASMBuildCreated) error { return r.Err })
	return err
}

func (c *Client) FindWASMBuild(ctx context.Context, id, ownerID string) (WASMBuild, bool, error) {
	found, err := ask(c, ctx, &FindWASMBuild{ID: id, OwnerID: ownerID}, func(r *WASMBuildFound) error { return r.Err })
	return found.Build, found.Found, err
}

func (c *Client) FindWASMBuildArtifact(ctx context.Context, id, ownerID string) (WASMBuildArtifact, error) {
	return ask(c, ctx, &FindWASMBuildArtifact{ID: id, OwnerID: ownerID}, func(r *WASMBuildArtifact) error { return r.Err })
}

func (c *Client) ListResumableImports(ctx context.Context) ([]ResumableImport, error) {
	listed, err := ask(c, ctx, &ListResumableImports{}, func(r *ResumableImportsListed) error { return r.Err })
	return listed.Jobs, err
}

func (c *Client) ListResumableBuilds(ctx context.Context) ([]ResumableBuild, error) {
	listed, err := ask(c, ctx, &ListResumableBuilds{}, func(r *ResumableBuildsListed) error { return r.Err })
	return listed.Builds, err
}

func (c *Client) ListActiveJobs(ctx context.Context, ownerID string) ([]ActiveJob, error) {
	listed, err := ask(c, ctx, &ListActiveJobs{OwnerID: ownerID}, func(r *ActiveJobsListed) error { return r.Err })
	return listed.Jobs, err
}

func (c *Client) DeleteAccount(ctx context.Context, userID string, cancelActiveJobs bool) (AccountDeleted, error) {
	return ask(c, ctx, &DeleteAccount{UserID: userID, CancelActiveJobs: cancelActiveJobs}, func(r *AccountDeleted) error { return r.Err })
}
