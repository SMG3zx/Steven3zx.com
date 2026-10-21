package app

import (
	"errors"

	domainfleet "janus/internal/domain/fleet"
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

// Check is the result of the checks run on one WASM Build.
type Check struct {
	BuildID string `json:"build_id"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Log     string `json:"log,omitempty"`
	NoTests bool   `json:"no_tests"`
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

// StartBuild asks the builds service for a Build of a Project's Snapshot. The
// caller chooses the ID.
type StartBuild struct {
	ID, OwnerID, ProjectID, SnapshotID, Target string
}

// WASMBuildChange is a Build's state as the builds service publishes it, which
// the Store keeps as its reference to the Build.
type WASMBuildChange struct {
	ID, OwnerID, ProjectID, SnapshotID, Target, Status, Error string
}

// CheckChange is a Check's state as the builds service publishes it, which the
// Store keeps for the Deployments that refer to its Build.
type CheckChange struct {
	BuildID, OwnerID, Status, Error, Log string
	NoTests                              bool
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

type AccountDeleted struct {
	Deleted    bool
	ActiveJobs []ActiveJob
	Err        error
}

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

// The fleet contract's types the single process uses as they are: enrollment,
// the Agents and the Usage they report, and the Deployments queued for them.
type (
	NewDeployment     = domainfleet.NewDeployment
	IssuedCode        = domainfleet.IssuedCode
	Enrollment        = domainfleet.Enrollment
	AgentRecord       = domainfleet.AgentRecord
	AgentStatus       = domainfleet.AgentStatus
	AgentStatusReport = domainfleet.AgentStatusReport
	RunUsageRecord    = domainfleet.RunUsageRecord
	RunUsageQuery     = domainfleet.RunUsageQuery
)
