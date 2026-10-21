// Package app holds the Janus use cases and the ports they need.
package app

import (
	"context"
	"time"
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

	// Deployments. A Project has at most one Deployment in flight, one that is
	// queued or starting; CreateDeployment fails with a plain error while another
	// one is. TransitionDeployment is a compare and swap on the state, following
	// domain/deployments.
	CreateDeployment(ctx context.Context, deployment CreateDeployment) error
	TransitionDeployment(ctx context.Context, transition DeploymentTransition) error
	FindDeployment(ctx context.Context, id, ownerID string) (deployment Deployment, found bool, err error)
	// PromoteDeployment makes a starting Deployment healthy and stops the
	// Project's other healthy Deployments with the reason it names, in one change.
	// It returns the Deployments it stopped, with their Agents, so the Agents can
	// be told. A Deployment that is not starting is refused.
	PromoteDeployment(ctx context.Context, promotion PromoteDeployment) (superseded []Deployment, err error)
	// ListDeployments returns the Owner's Deployments of a Project, newest first.
	ListDeployments(ctx context.Context, projectID, ownerID string) ([]Deployment, error)

	// Agents and Enrollment Codes. A code is stored by hash and can be consumed
	// once; every Agent lookup is scoped to its owner.
	CreateEnrollmentCode(ctx context.Context, code CreateEnrollmentCode) error
	// ConsumeEnrollmentCode marks the code used and returns its owner, if the
	// code exists, is unused and expires after now. Otherwise consumed is false and
	// the code, if any, is left as it is.
	ConsumeEnrollmentCode(ctx context.Context, codeHash string, now time.Time) (ownerID string, consumed bool, err error)
	CreateAgent(ctx context.Context, agent CreateAgent) error
	ListAgents(ctx context.Context, ownerID string) ([]AgentRecord, error)
	FindAgent(ctx context.Context, id, ownerID string) (agent AgentRecord, found bool, err error)
	DeleteAgent(ctx context.Context, id, ownerID string) (deleted bool, err error)
	// ListAllAgents returns every Owner's Agents, oldest first. Only the control
	// plane's own connection upkeep uses it.
	ListAllAgents(ctx context.Context) ([]AgentRecord, error)

	// Run Usage that Agents report. A run is kept once: recording a RunID again
	// changes nothing. Usage outlives the Agent and the Deployment it ran for,
	// and goes with the Account.
	RecordRunUsage(ctx context.Context, record RunUsageRecord) error
	// ListRunUsage returns the Owner's runs that match query, newest first.
	ListRunUsage(ctx context.Context, query RunUsageQuery) ([]RunUsageRecord, error)

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
	// Artifact returns the Artifact of a complete Build; Found is false when
	// it has none.
	Artifact(ctx context.Context, owner, buildID string) (WASMBuildArtifact, error)
	// DeleteOwner cancels the Owner's running Builds and deletes its Builds and
	// Artifacts. It is idempotent.
	DeleteOwner(ctx context.Context, owner string) error
}

// BuildArtifacts reads the Artifacts of an Account's WASM Builds, which Runs and
// the Fleet deploy.
type BuildArtifacts interface {
	Artifact(ctx context.Context, owner, buildID string) (WASMBuildArtifact, error)
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

// ControlTag is the tag of the control plane's own node on the tailnet.
const ControlTag = "tag:ctrl"

// Tailnet is the private network Agents join, run by a control server such as
// Headscale. Janus asks it for the keys an Agent enrolls with and for the nodes
// that joined, and removes a node to cut an Agent off. It never reaches an
// Agent itself: that is a connection over the network, made by other code.
//
// Whoever implements Tailnet enforces one access rule: nodes tagged ControlTag
// may open connections to every other tagged node, and no other connection is
// allowed. So an Agent can reach neither the control plane nor another
// Account's Agent, and enrolling an Account needs no change to the rule.
//
// An ID is whatever the control server calls a key or a node, as a string. A
// key is single-use: once a node has joined with it, or after it is expired, a
// second join fails. Removing or expiring something that does not exist is not
// an error, so a retry after a partial failure finishes the job.
type Tailnet interface {
	// CreatePreAuthKey makes a single-use key that enrolls one node carrying tag,
	// valid until expires.
	CreatePreAuthKey(ctx context.Context, tag string, expires time.Time) (TailnetKey, error)
	// ExpirePreAuthKey ends a key's validity at once. Nodes that already joined
	// with it stay.
	ExpirePreAuthKey(ctx context.Context, keyID string) error
	// Nodes lists every node on the tailnet. Online reflects the control
	// server's last report and may lag a disconnect by several seconds.
	Nodes(ctx context.Context) ([]TailnetNode, error)
	// DeleteNode removes a node, so it can no longer be reached.
	DeleteNode(ctx context.Context, nodeID string) error
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

// AgentLink carries the control plane's commands to Agents over the tailnet
// and their events back. It holds one connection per Agent.
//
// Connect keeps a connection open to the Agent at address, the IP address of
// its tailnet node, until Disconnect, reconnecting whenever it drops.
// Connecting an Agent that is already connected to the same address does
// nothing; a new address replaces the old connection. Events from the Agent go
// to events one at a time, in the order the Agent sent them, on a goroutine the
// link owns. An Agent sends its AgentState first on every new connection.
//
// Send delivers one command and waits for the Agent's answer. It fails with an
// error matching ErrAgentUnreachable when there is no connection or the
// connection drops before the answer, and with an *AgentError when the Agent
// refused the command. Close disconnects every Agent and waits for the link's
// goroutines.
type AgentLink interface {
	Connect(agentID, address string, events func(AgentEvent))
	Disconnect(agentID string)
	Send(ctx context.Context, agentID string, command AgentCommand) error
	Close() error
}
