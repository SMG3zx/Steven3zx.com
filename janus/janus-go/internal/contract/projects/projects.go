// Package projects is the contract of the projects service: Projects, their
// Snapshots and files, Imports, and the GitHub repositories an Account can
// import from.
//
// Every request about an Owner's records carries owner_id, and a record that is
// not the Owner's is answered as absent. Failures are contract.Error values:
// StatusNotFound for an absent Project or Snapshot, StatusConflict when the
// request clashes with what is running (an import in progress), StatusInvalid
// for a request that cannot be carried out as asked; the message is a sentence
// for the Account.
package projects

import "time"

// ServiceName is the service's name in NATS service discovery.
const ServiceName = "projects"

// The subjects the projects service answers.
const (
	SubjectCreateProject    = "janus.projects.project.create"
	SubjectUpdateProject    = "janus.projects.project.update"
	SubjectDeleteProject    = "janus.projects.project.delete"
	SubjectListProjects     = "janus.projects.project.list"
	SubjectGetProject       = "janus.projects.project.get"
	SubjectListSnapshots    = "janus.projects.snapshot.list"
	SubjectActivateSnapshot = "janus.projects.snapshot.activate"
	SubjectSetEntryPoint    = "janus.projects.snapshot.entrypoint"
	SubjectSnapshotFiles    = "janus.projects.snapshot.files"
	SubjectRequestImport    = "janus.projects.import.request"
	SubjectRetryImport      = "janus.projects.import.retry"
	SubjectFindImport       = "janus.projects.import.find"
	SubjectActiveImports    = "janus.projects.import.active"
	SubjectDeleteOwner      = "janus.projects.owner.delete"
	SubjectRepositories     = "janus.projects.github.repositories"
	SubjectBranches         = "janus.projects.github.branches"
	SubjectCommits          = "janus.projects.github.commits"
)

// SubjectImportJob carries the ID of an Import to run, in the JOBS stream.
const SubjectImportJob = "janus.jobs.import"

// The events the projects service publishes, in the EVENTS stream.
const (
	SubjectProjectDeleted  = "janus.events.projects.project_deleted"
	SubjectSnapshotDeleted = "janus.events.projects.snapshot_deleted"
	// SubjectEvents matches every event of the projects service.
	SubjectEvents = "janus.events.projects.>"
)

// Empty is the reply of a request that returns nothing.
type Empty struct{}

// Owner names the Owner a request is about.
type Owner struct {
	OwnerID string `json:"owner_id"`
}

// ProjectKey names one Project of an Owner.
type ProjectKey struct {
	OwnerID   string `json:"owner_id"`
	ProjectID string `json:"project_id"`
}

// CreateProject creates a Project. The caller chooses the ID.
type CreateProject struct {
	ID          string `json:"id"`
	OwnerID     string `json:"owner_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// UpdateProject renames a Project and changes its description.
type UpdateProject struct {
	ID          string `json:"id"`
	OwnerID     string `json:"owner_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Project is a Project with the details of its active Snapshot, when it has
// one. Times are SQLite timestamps, as Janus has always shown them.
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

// ProjectFound answers SubjectGetProject.
type ProjectFound struct {
	Found   bool    `json:"found"`
	Project Project `json:"project"`
}

// Projects answers SubjectListProjects, most recently updated first.
type Projects struct {
	Projects []Project `json:"projects"`
}

// ProjectDeleted answers SubjectDeleteProject. Deleted is false when there was
// no such Project.
type ProjectDeleted struct {
	Deleted bool `json:"deleted"`
}

// Snapshot is a ready Snapshot of a Project.
type Snapshot struct {
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

// Snapshots answers SubjectListSnapshots, newest first.
type Snapshots struct {
	Snapshots []Snapshot `json:"snapshots"`
}

// SnapshotKey names one Snapshot of a Project of an Owner.
type SnapshotKey struct {
	OwnerID    string `json:"owner_id"`
	ProjectID  string `json:"project_id"`
	SnapshotID string `json:"snapshot_id"`
}

// Activated answers SubjectActivateSnapshot. Activated is false when there is
// no such ready Snapshot.
type Activated struct {
	Activated bool `json:"activated"`
}

// SetEntryPoint records the Entrypoint the Account confirmed for the Project's
// active Snapshot. It must be one of the candidates the scan found.
type SetEntryPoint struct {
	OwnerID    string `json:"owner_id"`
	ProjectID  string `json:"project_id"`
	SnapshotID string `json:"snapshot_id"`
	EntryPoint string `json:"entry_point"`
}

// SnapshotFiles asks for one page of a ready Snapshot's files, ordered by
// path, starting at the Offset-th file and, in it, at byte ContentOffset.
// Limit is at most MaxFilePage; zero means DefaultFilePage. A page carries at
// most MaxBytes of content (zero or more than DefaultPageBytes means
// DefaultPageBytes), so it fits in one NATS message: a file that does not fit
// is cut, and the caller asks again at the same Offset with ContentOffset
// where the cut was.
type SnapshotFiles struct {
	OwnerID       string `json:"owner_id"`
	ProjectID     string `json:"project_id"`
	SnapshotID    string `json:"snapshot_id"`
	Offset        int    `json:"offset"`
	ContentOffset int    `json:"content_offset,omitempty"`
	Limit         int    `json:"limit"`
	MaxBytes      int    `json:"max_bytes,omitempty"`
}

// The paging limits of SubjectSnapshotFiles.
const (
	DefaultFilePage  = 200
	MaxFilePage      = 500
	DefaultPageBytes = 512 << 10
)

// File is one file of a Snapshot, or part of it: Content holds the bytes from
// ContentOffset on, and the file is whole once ContentOffset+len(Content)
// reaches Size.
type File struct {
	Path          string `json:"path"`
	Mode          string `json:"mode"`
	Hash          string `json:"hash"`
	Size          int64  `json:"size"`
	ContentOffset int    `json:"content_offset,omitempty"`
	Content       []byte `json:"content"`
}

// Files answers SubjectSnapshotFiles. More is true when more of the Snapshot
// follows this page, including the rest of a cut file.
type Files struct {
	Files []File `json:"files"`
	More  bool   `json:"more"`
}

// RequestImport asks for one commit of a GitHub repository to be imported into
// a Project. Commit is a full 40-character SHA among the branch's 50 newest.
type RequestImport struct {
	OwnerID    string `json:"owner_id"`
	ProjectID  string `json:"project_id"`
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Commit     string `json:"commit"`
}

// ImportKey names one Import of an Owner.
type ImportKey struct {
	OwnerID string `json:"owner_id"`
	JobID   string `json:"job_id"`
}

// ImportQueued answers SubjectRequestImport and SubjectRetryImport.
type ImportQueued struct {
	JobID string `json:"job_id"`
}

// ImportJob is an Import and where it stands.
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

// ImportFound answers SubjectFindImport.
type ImportFound struct {
	Found bool      `json:"found"`
	Job   ImportJob `json:"job"`
}

// ActiveImport is an Import that has not finished.
type ActiveImport struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Status    string `json:"status"`
}

// ActiveImports answers SubjectActiveImports.
type ActiveImports struct {
	Imports []ActiveImport `json:"imports"`
}

// Repository is a GitHub repository, with GitHub's field names.
type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Language      string `json:"language"`
	Stars         int    `json:"stargazers_count"`
	Forks         int    `json:"forks_count"`
	CloneURL      string `json:"clone_url"`
	HTMLURL       string `json:"html_url"`
	Size          int64  `json:"size"`
}

// Repositories answers SubjectRepositories. Connected is false when the
// Account has no GitHub Connection; the list is empty then.
type Repositories struct {
	Repositories []Repository `json:"repositories"`
	Connected    bool         `json:"connected"`
}

// RepositoryKey names a repository an Owner can read, and for commits a
// branch of it.
type RepositoryKey struct {
	OwnerID    string `json:"owner_id"`
	Repository string `json:"repository"`
	Branch     string `json:"branch,omitempty"`
}

// Branch is a branch of a repository and the commit it points at.
type Branch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// Branches answers SubjectBranches, the default branch first.
type Branches struct {
	Branches []Branch `json:"branches"`
}

// Commit is one commit of a branch.
type Commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Date    time.Time `json:"date"`
}

// Commits answers SubjectCommits with the branch's 50 newest commits.
type Commits struct {
	Commits []Commit `json:"commits"`
}

// DeleteOwner cancels the Owner's Imports and deletes its Projects. It is part
// of deleting an Account.
type DeleteOwner struct {
	OwnerID string `json:"owner_id"`
}

// ProjectDeletedEvent says a Project and its Snapshots are gone. It is
// published after the deletion is stored, and may arrive more than once.
type ProjectDeletedEvent struct {
	ProjectID string `json:"project_id"`
	OwnerID   string `json:"owner_id"`
}

// SnapshotDeletedEvent says a ready Snapshot is gone: it fell out of the
// five a Project keeps. It may arrive more than once.
type SnapshotDeletedEvent struct {
	SnapshotID string `json:"snapshot_id"`
	ProjectID  string `json:"project_id"`
	OwnerID    string `json:"owner_id"`
}

// ImportJobMessage is the message of an Import in the JOBS stream.
type ImportJobMessage struct {
	JobID string `json:"job_id"`
}
