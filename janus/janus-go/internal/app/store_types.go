package app

import "errors"

type CreateLocalUser struct {
	ID           string
	Email        string
	PasswordHash string
}

type LocalUserCreated struct{ Err error }

type FindLocalUser struct{ Email string }

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

type FindGitHubToken struct{ UserID string }

type GitHubTokenFound struct {
	Token     string
	Connected bool
	Err       error
}

type DisconnectGitHub struct{ UserID string }

type GitHubDisconnected struct{ Err error }

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
type ProjectUpdated struct {
	Project Project
	Err     error
}
type DeleteProject struct{ ID, OwnerID string }
type ProjectDeleted struct {
	Deleted bool
	Err     error
}

type ProjectCreated struct {
	Project Project
	Err     error
}

type ListProjects struct{ OwnerID string }

type ProjectsListed struct {
	Projects []Project
	Err      error
}

type GetProject struct{ ID, OwnerID string }

type ProjectFound struct {
	Project Project
	Found   bool
	Err     error
}

type CreateImportJob struct {
	ID, OwnerID, ProjectID string
	RepositoryID           int64
	Repository             string
	Branch, Commit         string
	MetadataJSON           string
}

type ImportJobCreated struct {
	ID  string
	Err error
}

type UpdateImportJob struct {
	ID, Status, Error string
	Progress          int
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

type ImportJobFound struct {
	Job   ImportJob
	Found bool
	Err   error
}

type FindImportJob struct{ ID, OwnerID string }

type BeginSnapshot struct {
	ID, JobID, OwnerID, ProjectID string
	Repository                    string
	RepositoryID                  int64
	Branch, Commit                string
	DefaultBranch, Language       string
	Runtime                       string
	Stars, Forks                  int
	MetadataJSON                  string
	// Manifest lists every file of the commit, so that a resumed import can
	// finish without scanning or cloning the repository again.
	Manifest string
}

type BeginSnapshotResult struct{ Err error }

type SnapshotFile struct {
	Path, Mode, Hash string
	Content          []byte
}

type AddSnapshotFiles struct {
	SnapshotID string
	Files      []SnapshotFile
}

type SnapshotFilesAdded struct{ Err error }

type CompleteSnapshot struct {
	SnapshotID, JobID string
	EntryPoint        string
	FileCount         int
	TotalBytes        int64
}

type SnapshotCompleted struct{ Err error }

type DeleteStagingSnapshot struct{ SnapshotID string }

type StagingSnapshotDeleted struct{ Err error }

type SetProjectEntryPoint struct {
	OwnerID, ProjectID, SnapshotID, EntryPoint string
}

type ProjectEntryPointSet struct{ Err error }

type ListProjectFiles struct {
	OwnerID, ProjectID, SnapshotID string
	Offset, Limit                  int
}

type ProjectFilesListed struct {
	Files []SnapshotFile
	Err   error
}

type CreateWASMBuild struct {
	ID, OwnerID, ProjectID, SnapshotID, Target string
}
type WASMBuildCreated struct{ Err error }
type UpdateWASMBuild struct {
	ID, Status, Error, Log string
	Progress               int
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
type FindWASMBuild struct{ ID, OwnerID string }
type WASMBuildFound struct {
	Build WASMBuild
	Found bool
	Err   error
}
type WASMBuildArtifact struct {
	Name  string
	Data  []byte
	Found bool
	Err   error
}
type FindWASMBuildArtifact struct{ ID, OwnerID string }

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
type ListSnapshots struct{ OwnerID, ProjectID string }
type SnapshotsListed struct {
	Snapshots []ProjectSnapshot
	Err       error
}
type ActivateSnapshot struct{ OwnerID, ProjectID, SnapshotID string }
type SnapshotActivated struct {
	Activated bool
	Err       error
}

type GoogleIdentityResolved struct {
	ID          string
	Email       string
	DisplayName string
	Err         error
}

type FindUser struct{ ID string }

type UserFound struct {
	ID          string
	Email       string
	DisplayName string
	Found       bool
	Err         error
}

// ResumableImport is a queued import job found at startup. It is an interrupted
// job waiting to resume.
type ResumableImport struct {
	JobID      string
	OwnerID    string
	ProjectID  string
	Repository string
	Branch     string
	Commit     string
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

type ListResumableImports struct{}
type ResumableImportsListed struct {
	Jobs []ResumableImport
	Err  error
}

type ListResumableBuilds struct{}
type ResumableBuildsListed struct {
	Builds []ResumableBuild
	Err    error
}

// FindStagingSnapshot returns the Staging Snapshot an import job is writing, the
// hash of each file already stored in it keyed by path, and its manifest.
type FindStagingSnapshot struct{ JobID string }
type StagingSnapshotFound struct {
	SnapshotID string
	Found      bool
	Files      map[string]string
	Manifest   string
	Err        error
}

// DiscardJobStaging deletes the Staging Snapshot of an import job that has
// failed for good.
type DiscardJobStaging struct{ JobID string }
type JobStagingDiscarded struct{ Err error }

type ListActiveJobs struct{ OwnerID string }
type ActiveJobsListed struct {
	Jobs []ActiveJob
	Err  error
}

// DeleteAccount deletes an Account and everything it owns. Without
// CancelActiveJobs, an Account with unfinished jobs is left untouched and its
// active jobs are returned so the caller can ask the Owner what to do.
type DeleteAccount struct {
	UserID           string
	CancelActiveJobs bool
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
