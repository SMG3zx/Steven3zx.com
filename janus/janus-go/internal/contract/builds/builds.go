// Package builds is the contract of the builds service: WASM Builds of a
// Project's Snapshot, their Checks, and the Artifacts they produce.
//
// Every request about an Owner's records carries owner_id, and a record that is
// not the Owner's is answered as absent. Failures are contract.Error values:
// StatusNotFound for an absent Build, StatusConflict when a Build of the Project
// is already running, StatusInvalid for a request that cannot be carried out as
// asked; the message is a sentence for the Account.
package builds

// ServiceName is the service's name in NATS service discovery.
const ServiceName = "builds"

// The subjects the builds service answers.
const (
	SubjectRequestBuild    = "janus.builds.build.request"
	SubjectRetryBuild      = "janus.builds.build.retry"
	SubjectFindBuild       = "janus.builds.build.find"
	SubjectFindCheck       = "janus.builds.check.find"
	SubjectFindArtifact    = "janus.builds.artifact.find"
	SubjectRestoreArtifact = "janus.builds.artifact.restore"
	SubjectDeleteOwner     = "janus.builds.owner.delete"
	SubjectActiveBuilds    = "janus.builds.build.active"
)

// SubjectBuildJob carries the ID of a Build to run, in the JOBS stream.
const SubjectBuildJob = "janus.jobs.build"

// The events the builds service publishes, in the EVENTS stream. Each carries
// the whole state of the record it is about, so a consumer can store it as it
// is and apply it again without harm.
const (
	SubjectBuildChanged = "janus.events.builds.build_changed"
	SubjectCheckChanged = "janus.events.builds.check_changed"
	// SubjectEvents matches every event of the builds service.
	SubjectEvents = "janus.events.builds.>"
)

// Empty is the reply of a request that returns nothing.
type Empty struct{}

// Owner names the Owner a request is about.
type Owner struct {
	OwnerID string `json:"owner_id"`
}

// RequestBuild asks for a WASM Build of a Project. SnapshotID selects the
// Snapshot; when it is empty, the Project's active Snapshot is built. The caller
// chooses the ID.
type RequestBuild struct {
	ID         string `json:"id"`
	OwnerID    string `json:"owner_id"`
	ProjectID  string `json:"project_id"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	Target     string `json:"target"`
}

// BuildKey names one Build of an Owner.
type BuildKey struct {
	OwnerID string `json:"owner_id"`
	BuildID string `json:"build_id"`
}

// Build is a WASM Build as its Owner sees it, without its Artifact. Times are
// SQLite timestamps, as Janus has always shown them.
type Build struct {
	ID           string `json:"id"`
	OwnerID      string `json:"owner_id"`
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

// BuildFound answers SubjectFindBuild and SubjectRetryBuild's request.
type BuildFound struct {
	Found bool  `json:"found"`
	Build Build `json:"build"`
}

// Check is the result of the checks run on one Build. Status is "running",
// "passed" or "failed".
type Check struct {
	BuildID string `json:"build_id"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Log     string `json:"log,omitempty"`
	NoTests bool   `json:"no_tests"`
}

// CheckFound answers SubjectFindCheck.
type CheckFound struct {
	Found bool  `json:"found"`
	Check Check `json:"check"`
}

// ArtifactFound answers SubjectFindArtifact. The Artifact itself is not in the
// reply: it is an object of the artifacts store, named by its Build's ID, and
// may be larger than a message. Found is false when the Build has no Artifact
// yet, or none the Owner may read.
type ArtifactFound struct {
	Found        bool   `json:"found"`
	ObjectName   string `json:"object_name,omitempty"`
	ArtifactName string `json:"artifact_name,omitempty"`
}

// RestoreArtifact asks for the Artifact of a complete Build to be copied from
// builds.db back to the artifacts store, when the object is missing there.
type RestoreArtifact = BuildKey

// BuildJobMessage is the message of a Build in the JOBS stream.
type BuildJobMessage struct {
	JobID string `json:"job_id"`
}

// BuildChanged says a Build's record changed. It is published after the change
// is stored, and may arrive more than once.
type BuildChanged struct {
	BuildID    string `json:"build_id"`
	OwnerID    string `json:"owner_id"`
	ProjectID  string `json:"project_id"`
	SnapshotID string `json:"snapshot_id"`
	Target     string `json:"target"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

// CheckChanged says a Build's Check changed. Status is "running", "passed" or
// "failed". Log is the bounded output of the checks. It may arrive more than
// once.
type CheckChanged struct {
	BuildID string `json:"build_id"`
	OwnerID string `json:"owner_id"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Log     string `json:"log,omitempty"`
	NoTests bool   `json:"no_tests"`
}

// The Build statuses.
const (
	StatusQueued    = "queued"
	StatusBuilding  = "building"
	StatusComplete  = "complete"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// ActiveBuild is a WASM Build that is queued or building.
type ActiveBuild struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Status    string `json:"status"`
}

// ActiveBuilds answers SubjectActiveBuilds.
type ActiveBuilds struct {
	Builds []ActiveBuild `json:"builds"`
}
