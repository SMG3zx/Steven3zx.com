package persistence

import "janus/internal/app"

// The Store types live in app. They are aliased here so that callers of this
// package keep compiling until the persistence actor is replaced.
type (
	AccountDeleted = app.AccountDeleted
	ActivateSnapshot = app.ActivateSnapshot
	ActiveJob = app.ActiveJob
	ActiveJobsListed = app.ActiveJobsListed
	AddSnapshotFiles = app.AddSnapshotFiles
	BeginSnapshot = app.BeginSnapshot
	BeginSnapshotResult = app.BeginSnapshotResult
	CompleteSnapshot = app.CompleteSnapshot
	CompleteWASMBuild = app.CompleteWASMBuild
	ConnectGitHub = app.ConnectGitHub
	CreateImportJob = app.CreateImportJob
	CreateLocalUser = app.CreateLocalUser
	CreateProject = app.CreateProject
	CreateWASMBuild = app.CreateWASMBuild
	DeleteAccount = app.DeleteAccount
	DeleteProject = app.DeleteProject
	DeleteStagingSnapshot = app.DeleteStagingSnapshot
	DiscardJobStaging = app.DiscardJobStaging
	DisconnectGitHub = app.DisconnectGitHub
	FindGitHubToken = app.FindGitHubToken
	FindImportJob = app.FindImportJob
	FindLocalUser = app.FindLocalUser
	FindStagingSnapshot = app.FindStagingSnapshot
	FindUser = app.FindUser
	FindWASMBuild = app.FindWASMBuild
	FindWASMBuildArtifact = app.FindWASMBuildArtifact
	GetProject = app.GetProject
	GitHubConnected = app.GitHubConnected
	GitHubDisconnected = app.GitHubDisconnected
	GitHubLoginResolved = app.GitHubLoginResolved
	GitHubTokenFound = app.GitHubTokenFound
	GoogleIdentityResolved = app.GoogleIdentityResolved
	ImportJob = app.ImportJob
	ImportJobCreated = app.ImportJobCreated
	ImportJobFound = app.ImportJobFound
	JobStagingDiscarded = app.JobStagingDiscarded
	ListActiveJobs = app.ListActiveJobs
	ListProjectFiles = app.ListProjectFiles
	ListProjects = app.ListProjects
	ListResumableBuilds = app.ListResumableBuilds
	ListResumableImports = app.ListResumableImports
	ListSnapshots = app.ListSnapshots
	LocalUserCreated = app.LocalUserCreated
	LocalUserFound = app.LocalUserFound
	Project = app.Project
	ProjectCreated = app.ProjectCreated
	ProjectDeleted = app.ProjectDeleted
	ProjectEntryPointSet = app.ProjectEntryPointSet
	ProjectFilesListed = app.ProjectFilesListed
	ProjectFound = app.ProjectFound
	ProjectSnapshot = app.ProjectSnapshot
	ProjectUpdated = app.ProjectUpdated
	ProjectsListed = app.ProjectsListed
	ReplyError = app.ReplyError
	ResolveGitHubLogin = app.ResolveGitHubLogin
	ResolveGoogleIdentity = app.ResolveGoogleIdentity
	ResumableBuild = app.ResumableBuild
	ResumableBuildsListed = app.ResumableBuildsListed
	ResumableImport = app.ResumableImport
	ResumableImportsListed = app.ResumableImportsListed
	SetProjectEntryPoint = app.SetProjectEntryPoint
	SnapshotActivated = app.SnapshotActivated
	SnapshotCompleted = app.SnapshotCompleted
	SnapshotFile = app.SnapshotFile
	SnapshotFilesAdded = app.SnapshotFilesAdded
	SnapshotsListed = app.SnapshotsListed
	StagingSnapshotDeleted = app.StagingSnapshotDeleted
	StagingSnapshotFound = app.StagingSnapshotFound
	UpdateImportJob = app.UpdateImportJob
	UpdateProject = app.UpdateProject
	UpdateWASMBuild = app.UpdateWASMBuild
	UserFound = app.UserFound
	WASMBuild = app.WASMBuild
	WASMBuildArtifact = app.WASMBuildArtifact
	WASMBuildCreated = app.WASMBuildCreated
	WASMBuildFound = app.WASMBuildFound
	// Store is the port; Client and the SQL actor implement it.
	Store = app.Store
)

const MaxResumes = app.MaxResumes

// IsReply reports whether err is an error that the persistence actor reported,
// and not a transport failure such as a timeout.
func IsReply(err error) bool { return app.IsReply(err) }

type CloseDatabase struct{}

type DatabaseClosed struct{ Err error }
