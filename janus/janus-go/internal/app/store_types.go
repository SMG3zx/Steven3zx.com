package app

import (
	"errors"
	"time"
)

type CreateLocalUser struct {
	ID           string
	Email        string
	PasswordHash string
}

type LocalUserFound struct {
	ID           string
	Email        string
	PasswordHash string
	Found        bool
	Err          error
}

type ResolveGoogleIdentity struct {
	Subject     string
	Email       string
	DisplayName string
}

type ResolveGitHubLogin struct {
	Subject     string
	Email       string
	DisplayName string
}

type GitHubLoginResolved struct {
	ID          string
	Email       string
	DisplayName string
	EmailInUse  bool
	Err         error
}

type ConnectGitHub struct {
	UserID      string
	Subject     string
	Email       string
	DisplayName string
	Token       string
}

type GitHubConnected struct {
	Err error
}

type Project struct {
	ID            string `json:"id"`
	OwnerID       string `json:"owner_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	SnapshotID    string `json:"snapshot_id,omitempty"`
	MetadataJSON  string `json:"metadata_json,omitempty"`
	RepositoryID  int64  `json:"repository_id,omitempty"`
	Repository    string `json:"repository,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Commit        string `json:"commit,omitempty"`
	EntryPoint    string `json:"entry_point,omitempty"`
	Language      string `json:"language,omitempty"`
	Runtime       string `json:"runtime"`
	Stars         int    `json:"stars,omitempty"`
	Forks         int    `json:"forks,omitempty"`
	FileCount     int    `json:"file_count,omitempty"`
	TotalBytes    int64  `json:"total_bytes,omitempty"`
	SnapshotCount int    `json:"snapshot_count"`
}

type CreateProject struct {
	ID, OwnerID, Name, Description string
}

type UpdateProject struct{ ID, OwnerID, Name, Description string }

// JobTransition moves a job from the status From to the status To, if the job
// is still in From. Error is the reason shown when To is failed, and Log is the
// build log a WASM build keeps; an import ignores it.
type JobTransition struct {
	ID, From, To, Error, Log string
	Progress                 int
}

type ImportJob struct {
	ID         string `json:"id"`
	OwnerID    string `json:"owner_id"`
	ProjectID  string `json:"project_id"`
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Commit     string `json:"commit"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	Progress   int    `json:"progress"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type SnapshotFile struct {
	Path, Mode, Hash string
	Content          []byte
}

// ProjectRef is the Store's reference to a Project of the projects service.
type ProjectRef struct{ ID, OwnerID string }

// SnapshotRef is the Store's reference to a ready Snapshot of the projects
// service, with what WASM Builds need to know of it.
type SnapshotRef struct {
	ID, ProjectID, OwnerID string
	Language, Runtime      string
}

type ListProjectFiles struct {
	OwnerID, ProjectID, SnapshotID string
	Offset, Limit                  int
}

type CreateWASMBuild struct {
	ID, OwnerID, ProjectID, SnapshotID, Target string
}
type CompleteWASMBuild struct {
	ID, ArtifactName string
	Artifact, Log    []byte
}
type WASMBuild struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	SnapshotID   string `json:"snapshot_id"`
	Target       string `json:"target"`
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	Error        string `json:"error,omitempty"`
	ArtifactName string `json:"artifact_name,omitempty"`
	Log          string `json:"log,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// Check statuses. A Check is running until its commands end, then passed or failed.
const (
	CheckRunning = "running"
	CheckPassed  = "passed"
	CheckFailed  = "failed"
)

// StartCheck begins the Check of a completed WASM Build.
type StartCheck struct{ BuildID string }

// FinishCheck records how the Check of a WASM Build ended. Status is CheckPassed
// or CheckFailed; Error is set when the checks could not run to an answer.
type FinishCheck struct {
	BuildID, Status, Error, Log string
	NoTests                     bool
}

// Check is the result of the checks run on one WASM Build.
type Check struct {
	BuildID string `json:"build_id"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Log     string `json:"log,omitempty"`
	NoTests bool   `json:"no_tests"`
}

// CreateDeployment queues a Deployment of a WASM Build on one of the Owner's
// Agents. DeployAnyway records that the Owner chose to deploy despite failing
// checks.
type CreateDeployment struct {
	ID, OwnerID, ProjectID, BuildID, AgentID string
	DeployAnyway                             bool
}

// DeploymentTransition moves a Deployment to a new state, when it is still in
// From. Error and Reason are set when the move ends the Deployment.
type DeploymentTransition struct {
	ID, From, To, Error, Reason string
}

// PromoteDeployment makes a starting Deployment healthy, the moment it is ready
// to serve. Supersedes is the reason given to the Project's other healthy
// Deployments, which the promotion stops (replaced or rolled back).
type PromoteDeployment struct {
	ID, OwnerID, ProjectID, Supersedes string
}

// Deployment is a Deployment as its Owner sees it.
type Deployment struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	BuildID      string `json:"build_id"`
	AgentID      string `json:"agent_id"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	Error        string `json:"error,omitempty"`
	DeployAnyway bool   `json:"deploy_anyway"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	// Check is the Check of the Deployment's WASM Build, when it has one. Its
	// error is the checks' own stage error.
	Check *Check `json:"check,omitempty"`
}

type WASMBuildArtifact struct {
	Name  string
	Data  []byte
	Found bool
	Err   error
}

type ProjectSnapshot struct {
	ID            string `json:"id"`
	Repository    string `json:"repository"`
	Branch        string `json:"branch"`
	Commit        string `json:"commit"`
	EntryPoint    string `json:"entry_point,omitempty"`
	Language      string `json:"language,omitempty"`
	Runtime       string `json:"runtime"`
	DefaultBranch string `json:"default_branch"`
	Stars         int    `json:"stars"`
	Forks         int    `json:"forks"`
	FileCount     int    `json:"file_count"`
	TotalBytes    int64  `json:"total_bytes"`
	CreatedAt     string `json:"created_at"`
	Active        bool   `json:"active"`
}

type GoogleIdentityResolved struct {
	ID          string
	Email       string
	DisplayName string
	Err         error
}

type UserFound struct {
	ID          string
	Email       string
	DisplayName string
	Found       bool
	Err         error
}

// ResumableBuild is a queued WASM build found at startup, with the language of
// the Snapshot it compiles.
type ResumableBuild struct {
	JobID      string
	OwnerID    string
	ProjectID  string
	SnapshotID string
	Target     string
	Language   string
}

type AccountDeleted struct {
	Deleted    bool
	ActiveJobs []ActiveJob
	Err        error
}

// MaxResumes is how many times an interrupted job may resume after a restart.
// The interruption after the last allowed resume fails the job.
const MaxResumes = 3

// ActiveJob describes an import or WASM build that has not finished yet.
type ActiveJob struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Status    string `json:"status"`
}

// ReplyError is an error the database itself reported, such as a constraint
// violation, as opposed to a request that was cancelled or timed out. Its text
// is the reported error's text.
type ReplyError struct{ Err error }

func (e *ReplyError) Error() string { return e.Err.Error() }
func (e *ReplyError) Unwrap() error { return e.Err }

// IsReply reports whether err is an error that the database reported, and not
// a cancelled or expired context.
func IsReply(err error) bool {
	var reply *ReplyError
	return errors.As(err, &reply)
}

// CreateEnrollmentCode stores an Enrollment Code by its hash, so the Store
// never holds a code that could be used. It can be consumed once, before
// ExpiresAt.
type CreateEnrollmentCode struct {
	CodeHash, OwnerID string
	ExpiresAt         time.Time
}

// CreateAgent records an Agent that enrolled with KeyID, the pre-auth key it
// was given. Tag is the tailnet tag its node carries.
type CreateAgent struct {
	ID, OwnerID, Name, Tag, KeyID string
	CreatedAt                     time.Time
}

// AgentRecord is a stored Agent.
type AgentRecord struct {
	ID, OwnerID, Name, Tag, KeyID string
	CreatedAt                     time.Time
}

// RunUsageRecord is one run an Agent reported, with the Owner and the Agent it
// came from. The Owner is the Agent's, never a value the Agent sent.
type RunUsageRecord struct {
	RunReport
	OwnerID, AgentID string
}

// RunUsageQuery selects an Owner's runs. AgentID and DeploymentID narrow it
// when set. Limit caps how many come back; zero or less means 100.
type RunUsageQuery struct {
	OwnerID, AgentID, DeploymentID string
	Limit                          int
}
