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
	// TransitionImportJob and TransitionWASMBuild compare and swap a job's status.
	// The move must be legal under the job's state machine in domain/jobs, and the
	// stored status must still be transition.From; otherwise the job is left as it
	// is and the error says "job cannot move from X to Y", "job not found" or
	// "job changed while it was being updated". Staying in a non-terminal status
	// is a legal move, so progress can be reported.
	TransitionImportJob(ctx context.Context, transition JobTransition) error
	FindImportJob(ctx context.Context, id, ownerID string) (job ImportJob, found bool, err error)
	BeginSnapshot(ctx context.Context, snapshot BeginSnapshot) error
	AddSnapshotFiles(ctx context.Context, snapshotID string, files []SnapshotFile) error
	CompleteSnapshot(ctx context.Context, snapshot CompleteSnapshot) error
	DeleteStagingSnapshot(ctx context.Context, snapshotID string) error
	FindStagingSnapshot(ctx context.Context, jobID string) (StagingSnapshotFound, error)
	DiscardJobStaging(ctx context.Context, jobID string) error
	CreateWASMBuild(ctx context.Context, build CreateWASMBuild) error
	TransitionWASMBuild(ctx context.Context, transition JobTransition) error
	CompleteWASMBuild(ctx context.Context, build CompleteWASMBuild) error
	FindWASMBuild(ctx context.Context, id, ownerID string) (build WASMBuild, found bool, err error)
	FindWASMBuildArtifact(ctx context.Context, id, ownerID string) (WASMBuildArtifact, error)
	ListResumableImports(ctx context.Context) ([]ResumableImport, error)
	ListResumableBuilds(ctx context.Context) ([]ResumableBuild, error)
	ListActiveJobs(ctx context.Context, ownerID string) ([]ActiveJob, error)

	// Checks of WASM Builds. A Build has at most one Check, started when the Build
	// completes and finished when its commands end.
	StartCheck(ctx context.Context, check StartCheck) error
	FinishCheck(ctx context.Context, check FinishCheck) error
	FindCheck(ctx context.Context, buildID, ownerID string) (check Check, found bool, err error)

	// Deployments. A Project has at most one Deployment in flight, one that is
	// queued or starting; CreateDeployment fails with a plain error while another
	// one is. TransitionDeployment is a compare and swap on the state, following
	// domain/deployments.
	CreateDeployment(ctx context.Context, deployment CreateDeployment) error
	TransitionDeployment(ctx context.Context, transition DeploymentTransition) error
	FindDeployment(ctx context.Context, id, ownerID string) (deployment Deployment, found bool, err error)

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

// Checker runs the check and test commands of a compiled workspace in an
// isolated container, the same way Builder compiles it. A failing command is a
// Passed of false, not an error. An error means the checks could not run to an
// answer, such as a container that crashed or timed out.
type Checker interface {
	Check(ctx context.Context, language string, request CheckRequest) (CheckResult, error)
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
