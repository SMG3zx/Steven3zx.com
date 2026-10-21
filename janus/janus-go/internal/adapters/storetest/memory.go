package storetest

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"janus/internal/app"
	"janus/internal/domain/jobs"
)

// Memory is an app.Store that keeps its data in maps, for tests of callers that need
// no database. It follows the rules the SQL store enforces: ownership,
// uniqueness, the job state machines and the cascade when something is deleted.
// Like the SQL store, every error it returns is a *ReplyError, and a constraint
// violation reads "constraint failed: ..." as SQLite's does.
//
// It stores GitHub tokens as given, since there is no database to protect.
type Memory struct {
	mu   sync.Mutex
	last time.Time

	users       map[string]*memUser
	creds       map[string]memCredential // by user ID
	identities  map[memIdentity]string   // provider and subject to user ID
	github      map[string]memGitHub     // by user ID
	projects    map[string]*memProject
	snapshots   map[string]*memSnapshot
	imports     map[string]*memImport
	builds      map[string]*memBuild
	codes       map[string]memCode // by code hash
	agents      map[string]app.AgentRecord
	runs        map[string]app.RunUsageRecord // by run ID
	checks      map[string]*memCheck          // by WASM Build ID
	deployments map[string]*memDeployment     // by Deployment ID
	deploySeq   int                           // orders Deployments as they are created
}

var _ app.Store = (*Memory)(nil)

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{
		users:       map[string]*memUser{},
		creds:       map[string]memCredential{},
		identities:  map[memIdentity]string{},
		github:      map[string]memGitHub{},
		projects:    map[string]*memProject{},
		snapshots:   map[string]*memSnapshot{},
		imports:     map[string]*memImport{},
		builds:      map[string]*memBuild{},
		codes:       map[string]memCode{},
		agents:      map[string]app.AgentRecord{},
		runs:        map[string]app.RunUsageRecord{},
		checks:      map[string]*memCheck{},
		deployments: map[string]*memDeployment{},
	}
}

type memUser struct{ id, email, displayName string }
type memCredential struct{ email, passwordHash string }
type memIdentity struct{ provider, subject string }
type memGitHub struct{ subject, token string }
type memCode struct {
	ownerID   string
	expiresAt time.Time
	used      bool
}

type memProject struct {
	id, ownerID, name, description string
	createdAt, updatedAt           string
}

type memSnapshot struct {
	app.ProjectSnapshot
	ownerID, projectID, jobID string
	repositoryID              int64
	metadataJSON, manifest    string
	state                     string
	files                     map[string]app.SnapshotFile
}

type memImport struct {
	app.ImportJob
	repositoryID int64
}

type memBuild struct {
	app.WASMBuild
	ownerID  string
	artifact []byte
}

// activeImportStatuses and activeBuildStatuses are the statuses of a job that
// has not finished.
var (
	activeImportStatuses = []string{"queued", "cloning", "scanning", "storing"}
	activeBuildStatuses  = []string{"queued", "building"}
)

func isIn(status string, set []string) bool { return slices.Contains(set, status) }

// fail wraps a message the way the SQL store reports an error.
func fail(format string, args ...any) error {
	return &app.ReplyError{Err: fmt.Errorf(format, args...)}
}

func constraint(what string) error { return fail("constraint failed: %s", what) }

// now returns a timestamp in SQLite's format that is later than the last one,
// so that ordering by time never ties.
func (m *Memory) now() string {
	t := time.Now().UTC().Truncate(time.Millisecond)
	if !t.After(m.last) {
		t = m.last.Add(time.Millisecond)
	}
	m.last = t
	return t.Format("2006-01-02 15:04:05.000")
}

// Identity.

func (m *Memory) CreateLocalUser(_ context.Context, user app.CreateLocalUser) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	email := strings.TrimSpace(user.Email)
	if _, ok := m.users[user.ID]; ok {
		return constraint("UNIQUE constraint failed: users.id")
	}
	for _, c := range m.creds {
		if strings.EqualFold(c.email, email) {
			return constraint("UNIQUE constraint failed: local_credentials.email")
		}
	}
	m.users[user.ID] = &memUser{id: user.ID, email: email}
	m.creds[user.ID] = memCredential{email: email, passwordHash: user.PasswordHash}
	return nil
}

func (m *Memory) FindLocalUser(_ context.Context, email string) (app.LocalUserFound, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	email = strings.TrimSpace(email)
	for id, c := range m.creds {
		if strings.EqualFold(c.email, email) {
			return app.LocalUserFound{ID: id, Email: c.email, PasswordHash: c.passwordHash, Found: true}, nil
		}
	}
	return app.LocalUserFound{}, nil
}

func (m *Memory) FindUser(_ context.Context, id string) (app.UserFound, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return app.UserFound{}, nil
	}
	return app.UserFound{ID: u.id, Email: u.email, DisplayName: u.displayName, Found: true}, nil
}

func (m *Memory) ResolveGoogleIdentity(_ context.Context, identity app.ResolveGoogleIdentity) (app.GoogleIdentityResolved, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memIdentity{"google", identity.Subject}
	if id, ok := m.identities[key]; ok {
		u := m.users[id]
		return app.GoogleIdentityResolved{ID: u.id, Email: u.email, DisplayName: u.displayName}, nil
	}
	id := uuid.NewString()
	m.users[id] = &memUser{id: id, email: identity.Email, displayName: identity.DisplayName}
	m.identities[key] = id
	return app.GoogleIdentityResolved{ID: id, Email: identity.Email, DisplayName: identity.DisplayName}, nil
}

func (m *Memory) ResolveGitHubLogin(_ context.Context, login app.ResolveGitHubLogin) (app.GitHubLoginResolved, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memIdentity{"github", login.Subject}
	if id, ok := m.identities[key]; ok {
		u := m.users[id]
		return app.GitHubLoginResolved{ID: u.id, Email: u.email, DisplayName: u.displayName}, nil
	}
	email := strings.TrimSpace(login.Email)
	if email == "" {
		return app.GitHubLoginResolved{}, fail("GitHub did not return a verified primary email")
	}
	for _, u := range m.users {
		if u.email != "" && strings.EqualFold(u.email, email) {
			return app.GitHubLoginResolved{EmailInUse: true}, nil
		}
	}
	id := uuid.NewString()
	name := strings.TrimSpace(login.DisplayName)
	m.users[id] = &memUser{id: id, email: email, displayName: name}
	m.identities[key] = id
	return app.GitHubLoginResolved{ID: id, Email: email, DisplayName: name}, nil
}

func (m *Memory) ConnectGitHub(_ context.Context, connection app.ConnectGitHub) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if connection.UserID == "" || connection.Subject == "" || connection.Token == "" {
		return fail("incomplete GitHub connection")
	}
	key := memIdentity{"github", connection.Subject}
	if linked, ok := m.identities[key]; ok && linked != connection.UserID {
		return fail("GitHub account is connected to another Janus account")
	}
	if existing, ok := m.github[connection.UserID]; ok && existing.subject != connection.Subject {
		return fail("a different GitHub account is already connected")
	}
	if _, ok := m.users[connection.UserID]; !ok {
		return constraint("FOREIGN KEY constraint failed")
	}
	if _, ok := m.identities[key]; !ok {
		m.identities[key] = connection.UserID
	}
	m.github[connection.UserID] = memGitHub{subject: connection.Subject, token: connection.Token}
	return nil
}

func (m *Memory) FindGitHubToken(_ context.Context, userID string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	connection, ok := m.github[userID]
	return connection.token, ok, nil
}

func (m *Memory) DisconnectGitHub(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.github, userID)
	return nil
}

// Projects, Snapshots, files and Entrypoints.

// view builds the Project an Account sees: the project's own fields and those
// of its active Snapshot.
func (m *Memory) view(p *memProject) app.Project {
	out := app.Project{ID: p.id, OwnerID: p.ownerID, Name: p.name, Description: p.description,
		CreatedAt: p.createdAt, UpdatedAt: p.updatedAt, MetadataJSON: "{}"}
	for _, s := range m.snapshots {
		if s.projectID != p.id || s.state != "ready" {
			continue
		}
		out.SnapshotCount++
		if s.Active {
			out.SnapshotID, out.MetadataJSON = s.ID, s.metadataJSON
			out.RepositoryID, out.Repository = s.repositoryID, s.Repository
			out.DefaultBranch, out.Branch, out.Commit = s.DefaultBranch, s.Branch, s.Commit
			out.EntryPoint, out.Language, out.Runtime = s.EntryPoint, s.Language, s.Runtime
			out.Stars, out.Forks, out.FileCount, out.TotalBytes = s.Stars, s.Forks, s.FileCount, s.TotalBytes
		}
	}
	return out
}

func (m *Memory) ownedProject(id, ownerID string) (*memProject, bool) {
	p, ok := m.projects[id]
	if !ok || p.ownerID != ownerID {
		return nil, false
	}
	return p, true
}

func (m *Memory) touch(projectID string) {
	if p, ok := m.projects[projectID]; ok {
		p.updatedAt = m.now()
	}
}

func (m *Memory) CreateProject(_ context.Context, project app.CreateProject) (app.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[project.ID]; ok {
		return app.Project{}, constraint("UNIQUE constraint failed: projects.id")
	}
	if _, ok := m.users[project.OwnerID]; !ok {
		return app.Project{}, constraint("FOREIGN KEY constraint failed")
	}
	now := m.now()
	p := &memProject{id: project.ID, ownerID: project.OwnerID, name: strings.TrimSpace(project.Name),
		description: strings.TrimSpace(project.Description), createdAt: now, updatedAt: now}
	m.projects[p.id] = p
	return m.view(p), nil
}

func (m *Memory) UpdateProject(_ context.Context, project app.UpdateProject) (app.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.ownedProject(project.ID, project.OwnerID)
	if !ok {
		return app.Project{}, fail("project not found")
	}
	p.name, p.description = strings.TrimSpace(project.Name), strings.TrimSpace(project.Description)
	p.updatedAt = m.now()
	return m.view(p), nil
}

func (m *Memory) DeleteProject(_ context.Context, id, ownerID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.imports {
		if job.ProjectID == id && job.OwnerID == ownerID && isIn(job.Status, activeImportStatuses) {
			return false, fail("project has an import in progress")
		}
	}
	if _, ok := m.ownedProject(id, ownerID); !ok {
		return false, nil
	}
	m.deleteProject(id)
	return true, nil
}

// deleteProject removes a project and everything that belongs to it.
func (m *Memory) deleteProject(id string) {
	delete(m.projects, id)
	for sid, s := range m.snapshots {
		if s.projectID == id {
			delete(m.snapshots, sid)
		}
	}
	for jid, job := range m.imports {
		if job.ProjectID == id {
			delete(m.imports, jid)
		}
	}
	for bid, build := range m.builds {
		if build.ProjectID == id {
			delete(m.builds, bid)
		}
	}
}

// deleteSnapshot removes a Snapshot and the WASM builds that compile it.
func (m *Memory) deleteSnapshot(id string) {
	delete(m.snapshots, id)
	for bid, build := range m.builds {
		if build.SnapshotID == id {
			delete(m.builds, bid)
		}
	}
}

func (m *Memory) ListProjects(_ context.Context, ownerID string) ([]app.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make([]*memProject, 0)
	for _, p := range m.projects {
		if p.ownerID == ownerID {
			owned = append(owned, p)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].updatedAt > owned[j].updatedAt })
	projects := make([]app.Project, 0, len(owned))
	for _, p := range owned {
		projects = append(projects, m.view(p))
	}
	return projects, nil
}

func (m *Memory) GetProject(_ context.Context, id, ownerID string) (app.Project, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.ownedProject(id, ownerID)
	if !ok {
		return app.Project{}, false, nil
	}
	return m.view(p), true, nil
}

// readySnapshots returns a project's ready Snapshots, newest first.
func (m *Memory) readySnapshots(projectID string) []*memSnapshot {
	var ready []*memSnapshot
	for _, s := range m.snapshots {
		if s.projectID == projectID && s.state == "ready" {
			ready = append(ready, s)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].CreatedAt != ready[j].CreatedAt {
			return ready[i].CreatedAt > ready[j].CreatedAt
		}
		return ready[i].ID > ready[j].ID
	})
	return ready
}

func (m *Memory) ListSnapshots(_ context.Context, ownerID, projectID string) ([]app.ProjectSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.ownedProject(projectID, ownerID); !ok {
		return nil, fail("project not found")
	}
	snapshots := make([]app.ProjectSnapshot, 0, 5)
	for _, s := range m.readySnapshots(projectID) {
		snapshots = append(snapshots, s.ProjectSnapshot)
	}
	return snapshots, nil
}

func (m *Memory) ActivateSnapshot(_ context.Context, ownerID, projectID, snapshotID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, ok := m.snapshots[snapshotID]
	if !ok || target.projectID != projectID || target.ownerID != ownerID || target.state != "ready" {
		return false, nil
	}
	for _, s := range m.readySnapshots(projectID) {
		s.Active = s.ID == snapshotID
	}
	m.touch(projectID)
	return true, nil
}

func (m *Memory) SetProjectEntryPoint(_ context.Context, entry app.SetProjectEntryPoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snapshots[entry.SnapshotID]
	if !ok || s.projectID != entry.ProjectID || s.ownerID != entry.OwnerID || s.state != "ready" {
		return fail("project snapshot not found")
	}
	s.EntryPoint = entry.EntryPoint
	m.touch(entry.ProjectID)
	return nil
}

func (m *Memory) ListProjectFiles(_ context.Context, page app.ListProjectFiles) ([]app.SnapshotFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if page.Offset < 0 {
		return nil, fail("file page offset cannot be negative")
	}
	limit := page.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if _, ok := m.ownedProject(page.ProjectID, page.OwnerID); !ok {
		return nil, fail("project not found")
	}
	files := make([]app.SnapshotFile, 0)
	s, ok := m.snapshots[page.SnapshotID]
	if !ok || s.projectID != page.ProjectID || s.ownerID != page.OwnerID || s.state != "ready" {
		return files, nil
	}
	paths := make([]string, 0, len(s.files))
	for path := range s.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if page.Offset >= len(paths) {
		return files, nil
	}
	paths = paths[page.Offset:]
	if len(paths) > limit {
		paths = paths[:limit]
	}
	for _, path := range paths {
		files = append(files, copyFile(s.files[path]))
	}
	return files, nil
}

func copyFile(f app.SnapshotFile) app.SnapshotFile {
	f.Content = append([]byte(nil), f.Content...)
	return f
}

// Jobs: imports, Staging Snapshots, WASM Builds and Resume.

func (m *Memory) CreateImportJob(_ context.Context, job app.CreateImportJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.ownedProject(job.ProjectID, job.OwnerID); !ok {
		return fail("project not found")
	}
	if _, ok := m.imports[job.ID]; ok {
		return constraint("UNIQUE constraint failed: project_import_jobs.id")
	}
	for _, other := range m.imports {
		if other.ProjectID == job.ProjectID && isIn(other.Status, activeImportStatuses) {
			return constraint("UNIQUE constraint failed: project_import_jobs.project_id")
		}
	}
	now := m.now()
	m.imports[job.ID] = &memImport{ImportJob: app.ImportJob{ID: job.ID, OwnerID: job.OwnerID, ProjectID: job.ProjectID,
		Repository: job.Repository, Branch: job.Branch, Commit: job.Commit, Status: "queued", CreatedAt: now, UpdatedAt: now}}
	return nil
}

func (m *Memory) TransitionImportJob(_ context.Context, t app.JobTransition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.imports[t.ID]
	if !ok {
		return fail("job not found")
	}
	if !jobs.Import.CanMove(t.From, t.To) {
		return fail("job cannot move from %s to %s", t.From, t.To)
	}
	if job.Status != t.From {
		return fail("job changed while it was being updated")
	}
	job.Status, job.Progress, job.Error, job.UpdatedAt = t.To, t.Progress, t.Error, m.now()
	return nil
}

func (m *Memory) FindImportJob(_ context.Context, id, ownerID string) (app.ImportJob, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.imports[id]
	if !ok || job.OwnerID != ownerID {
		return app.ImportJob{}, false, nil
	}
	return job.ImportJob, true, nil
}

func (m *Memory) BeginSnapshot(_ context.Context, snapshot app.BeginSnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.ownedProject(snapshot.ProjectID, snapshot.OwnerID); !ok {
		return fail("project not found")
	}
	if _, ok := m.snapshots[snapshot.ID]; ok {
		return constraint("UNIQUE constraint failed: project_snapshots.id")
	}
	m.snapshots[snapshot.ID] = &memSnapshot{
		ProjectSnapshot: app.ProjectSnapshot{ID: snapshot.ID, Repository: snapshot.Repository, Branch: snapshot.Branch,
			Commit: snapshot.Commit, Language: snapshot.Language, Runtime: snapshot.Runtime,
			DefaultBranch: snapshot.DefaultBranch, Stars: snapshot.Stars, Forks: snapshot.Forks, CreatedAt: m.now()},
		ownerID: snapshot.OwnerID, projectID: snapshot.ProjectID, jobID: snapshot.JobID,
		repositoryID: snapshot.RepositoryID, metadataJSON: snapshot.MetadataJSON, manifest: snapshot.Manifest,
		state: "staging", files: map[string]app.SnapshotFile{},
	}
	return nil
}

func (m *Memory) AddSnapshotFiles(_ context.Context, snapshotID string, files []app.SnapshotFile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snapshots[snapshotID]
	if !ok {
		return constraint("FOREIGN KEY constraint failed")
	}
	// All or nothing, as the database inserts the batch in one transaction.
	seen := map[string]bool{}
	for _, f := range files {
		if _, exists := s.files[f.Path]; exists || seen[f.Path] {
			return constraint("UNIQUE constraint failed: project_files.snapshot_id, project_files.path")
		}
		seen[f.Path] = true
	}
	for _, f := range files {
		s.files[f.Path] = copyFile(f)
	}
	return nil
}

func (m *Memory) CompleteSnapshot(_ context.Context, snapshot app.CompleteSnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snapshots[snapshot.SnapshotID]
	if !ok || s.state != "staging" {
		return fail("staging snapshot not found")
	}
	job, ok := m.imports[snapshot.JobID]
	if !ok || job.Status != "storing" {
		return fail("import was cancelled before it completed")
	}
	for _, other := range m.readySnapshots(s.projectID) {
		other.Active = false
	}
	s.EntryPoint, s.FileCount, s.TotalBytes = snapshot.EntryPoint, snapshot.FileCount, snapshot.TotalBytes
	s.state, s.Active = "ready", true
	m.touch(s.projectID)
	job.Status, job.Progress, job.Error, job.UpdatedAt = "complete", 100, "", m.now()
	// Only the five newest ready Snapshots are kept.
	if ready := m.readySnapshots(s.projectID); len(ready) > 5 {
		for _, old := range ready[5:] {
			m.deleteSnapshot(old.ID)
		}
	}
	return nil
}

func (m *Memory) DeleteStagingSnapshot(_ context.Context, snapshotID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.snapshots[snapshotID]; ok && s.state == "staging" {
		m.deleteSnapshot(snapshotID)
	}
	return nil
}

func (m *Memory) FindStagingSnapshot(_ context.Context, jobID string) (app.StagingSnapshotFound, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.snapshots {
		if s.jobID != jobID || s.state != "staging" {
			continue
		}
		files := make(map[string]string, len(s.files))
		for path, f := range s.files {
			files[path] = f.Hash
		}
		return app.StagingSnapshotFound{SnapshotID: s.ID, Found: true, Files: files, Manifest: s.manifest}, nil
	}
	return app.StagingSnapshotFound{}, nil
}

func (m *Memory) DiscardJobStaging(_ context.Context, jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.snapshots {
		if s.jobID == jobID && s.state == "staging" {
			m.deleteSnapshot(id)
		}
	}
	return nil
}

func (m *Memory) CreateWASMBuild(_ context.Context, build app.CreateWASMBuild) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snapshots[build.SnapshotID]
	if _, owned := m.ownedProject(build.ProjectID, build.OwnerID); !owned || !ok || s.projectID != build.ProjectID || s.state != "ready" {
		return fail("project snapshot not found")
	}
	if _, exists := m.builds[build.ID]; exists {
		return constraint("UNIQUE constraint failed: project_wasm_builds.id")
	}
	for _, other := range m.builds {
		if other.ProjectID == build.ProjectID && isIn(other.Status, activeBuildStatuses) {
			return constraint("UNIQUE constraint failed: project_wasm_builds.project_id")
		}
	}
	now := m.now()
	m.builds[build.ID] = &memBuild{ownerID: build.OwnerID, WASMBuild: app.WASMBuild{ID: build.ID, ProjectID: build.ProjectID,
		SnapshotID: build.SnapshotID, Target: build.Target, Status: "queued", CreatedAt: now, UpdatedAt: now}}
	return nil
}

func (m *Memory) TransitionWASMBuild(_ context.Context, t app.JobTransition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[t.ID]
	if !ok {
		return fail("job not found")
	}
	if !jobs.WASM.CanMove(t.From, t.To) {
		return fail("WASM build cannot move from %s to %s", t.From, t.To)
	}
	if build.Status != t.From {
		return fail("WASM build changed while it was being updated")
	}
	build.Status, build.Progress, build.Error, build.Log, build.UpdatedAt = t.To, t.Progress, t.Error, t.Log, m.now()
	return nil
}

func (m *Memory) CompleteWASMBuild(_ context.Context, complete app.CompleteWASMBuild) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[complete.ID]
	if !ok || !isIn(build.Status, activeBuildStatuses) {
		return fail("WASM build not found")
	}
	build.Status, build.Progress, build.Error = "complete", 100, ""
	build.ArtifactName, build.artifact, build.Log = complete.ArtifactName, append([]byte(nil), complete.Artifact...), string(complete.Log)
	build.UpdatedAt = m.now()
	return nil
}

func (m *Memory) FindWASMBuild(_ context.Context, id, ownerID string) (app.WASMBuild, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[id]
	if !ok || build.ownerID != ownerID {
		return app.WASMBuild{}, false, nil
	}
	return build.WASMBuild, true, nil
}

func (m *Memory) FindWASMBuildArtifact(_ context.Context, id, ownerID string) (app.WASMBuildArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[id]
	if !ok || build.ownerID != ownerID || build.Status != "complete" {
		return app.WASMBuildArtifact{}, nil
	}
	return app.WASMBuildArtifact{Name: build.ArtifactName, Data: append([]byte(nil), build.artifact...), Found: true}, nil
}

func (m *Memory) ListResumableImports(_ context.Context) ([]app.ResumableImport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var queued []*memImport
	for _, job := range m.imports {
		if job.Status == "queued" {
			queued = append(queued, job)
		}
	}
	sort.Slice(queued, func(i, j int) bool { return queued[i].CreatedAt < queued[j].CreatedAt })
	var jobs []app.ResumableImport
	for _, job := range queued {
		jobs = append(jobs, app.ResumableImport{JobID: job.ID, OwnerID: job.OwnerID, ProjectID: job.ProjectID,
			Repository: job.Repository, Branch: job.Branch, Commit: job.Commit})
	}
	return jobs, nil
}

func (m *Memory) ListResumableBuilds(_ context.Context) ([]app.ResumableBuild, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var queued []*memBuild
	for _, build := range m.builds {
		if build.Status == "queued" {
			queued = append(queued, build)
		}
	}
	sort.Slice(queued, func(i, j int) bool { return queued[i].CreatedAt < queued[j].CreatedAt })
	var builds []app.ResumableBuild
	for _, build := range queued {
		s, ok := m.snapshots[build.SnapshotID]
		if !ok {
			continue
		}
		builds = append(builds, app.ResumableBuild{JobID: build.ID, OwnerID: build.ownerID, ProjectID: build.ProjectID,
			SnapshotID: build.SnapshotID, Target: build.Target, Language: s.Language})
	}
	return builds, nil
}

func (m *Memory) ListActiveJobs(_ context.Context, ownerID string) ([]app.ActiveJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeJobs(ownerID), nil
}

func (m *Memory) activeJobs(ownerID string) []app.ActiveJob {
	jobs := make([]app.ActiveJob, 0)
	for _, job := range m.imports {
		if job.OwnerID == ownerID && isIn(job.Status, activeImportStatuses) {
			jobs = append(jobs, app.ActiveJob{Kind: "import", ID: job.ID, ProjectID: job.ProjectID, Status: job.Status})
		}
	}
	for _, build := range m.builds {
		if build.ownerID == ownerID && isIn(build.Status, activeBuildStatuses) {
			jobs = append(jobs, app.ActiveJob{Kind: "wasm_build", ID: build.ID, ProjectID: build.ProjectID, Status: build.Status})
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Kind != jobs[j].Kind {
			return jobs[i].Kind == "import"
		}
		return jobs[i].ID < jobs[j].ID
	})
	return jobs
}

func (m *Memory) DeleteAccount(_ context.Context, userID string, cancelActiveJobs bool) (app.AccountDeleted, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return app.AccountDeleted{}, nil
	}
	if active := m.activeJobs(userID); len(active) > 0 && !cancelActiveJobs {
		return app.AccountDeleted{ActiveJobs: active}, nil
	}
	for id, p := range m.projects {
		if p.ownerID == userID {
			m.deleteProject(id)
		}
	}
	for id, job := range m.imports {
		if job.OwnerID == userID {
			delete(m.imports, id)
		}
	}
	for id, build := range m.builds {
		if build.ownerID == userID {
			delete(m.builds, id)
		}
	}
	for key, owner := range m.identities {
		if owner == userID {
			delete(m.identities, key)
		}
	}
	for id, agent := range m.agents {
		if agent.OwnerID == userID {
			delete(m.agents, id)
		}
	}
	for hash, code := range m.codes {
		if code.ownerID == userID {
			delete(m.codes, hash)
		}
	}
	for id, run := range m.runs {
		if run.OwnerID == userID {
			delete(m.runs, id)
		}
	}
	delete(m.github, userID)
	delete(m.creds, userID)
	delete(m.users, userID)
	return app.AccountDeleted{Deleted: true}, nil
}

func (m *Memory) CreateEnrollmentCode(_ context.Context, code app.CreateEnrollmentCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.codes[code.CodeHash]; ok {
		return constraint("UNIQUE constraint failed: enrollment_codes.code_hash")
	}
	m.codes[code.CodeHash] = memCode{ownerID: code.OwnerID, expiresAt: code.ExpiresAt}
	return nil
}

func (m *Memory) ConsumeEnrollmentCode(_ context.Context, codeHash string, now time.Time) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	code, ok := m.codes[codeHash]
	if !ok || code.used || !now.Before(code.expiresAt) {
		return "", false, nil
	}
	code.used = true
	m.codes[codeHash] = code
	return code.ownerID, true, nil
}

func (m *Memory) CreateAgent(_ context.Context, agent app.CreateAgent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.agents[agent.ID]; ok {
		return constraint("UNIQUE constraint failed: agents.id")
	}
	for _, other := range m.agents {
		if other.KeyID == agent.KeyID {
			return constraint("UNIQUE constraint failed: agents.key_id")
		}
	}
	m.agents[agent.ID] = app.AgentRecord{ID: agent.ID, OwnerID: agent.OwnerID, Name: agent.Name, Tag: agent.Tag, KeyID: agent.KeyID, CreatedAt: agent.CreatedAt.UTC()}
	return nil
}

func (m *Memory) ListAgents(_ context.Context, ownerID string) ([]app.AgentRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	agents := make([]app.AgentRecord, 0)
	for _, agent := range m.agents {
		if agent.OwnerID == ownerID {
			agents = append(agents, agent)
		}
	}
	sort.Slice(agents, func(i, j int) bool {
		if !agents[i].CreatedAt.Equal(agents[j].CreatedAt) {
			return agents[i].CreatedAt.Before(agents[j].CreatedAt)
		}
		return agents[i].ID < agents[j].ID
	})
	return agents, nil
}

func (m *Memory) FindAgent(_ context.Context, id, ownerID string) (app.AgentRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	agent, ok := m.agents[id]
	if !ok || agent.OwnerID != ownerID {
		return app.AgentRecord{}, false, nil
	}
	return agent, true, nil
}

func (m *Memory) DeleteAgent(_ context.Context, id, ownerID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	agent, ok := m.agents[id]
	if !ok || agent.OwnerID != ownerID {
		return false, nil
	}
	delete(m.agents, id)
	return true, nil
}

func (m *Memory) ListAllAgents(context.Context) ([]app.AgentRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	agents := make([]app.AgentRecord, 0, len(m.agents))
	for _, agent := range m.agents {
		agents = append(agents, agent)
	}
	sort.Slice(agents, func(i, j int) bool {
		if !agents[i].CreatedAt.Equal(agents[j].CreatedAt) {
			return agents[i].CreatedAt.Before(agents[j].CreatedAt)
		}
		return agents[i].ID < agents[j].ID
	})
	return agents, nil
}

func (m *Memory) RecordRunUsage(_ context.Context, record app.RunUsageRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[record.OwnerID]; !ok {
		return constraint("FOREIGN KEY constraint failed")
	}
	if _, ok := m.runs[record.RunID]; ok {
		return nil
	}
	record.StartedAt = record.StartedAt.UTC()
	m.runs[record.RunID] = record
	return nil
}

func (m *Memory) ListRunUsage(_ context.Context, query app.RunUsageQuery) ([]app.RunUsageRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	records := make([]app.RunUsageRecord, 0)
	for _, record := range m.runs {
		if record.OwnerID != query.OwnerID ||
			(query.AgentID != "" && record.AgentID != query.AgentID) ||
			(query.DeploymentID != "" && record.DeploymentID != query.DeploymentID) {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].StartedAt.Equal(records[j].StartedAt) {
			return records[i].StartedAt.After(records[j].StartedAt)
		}
		return records[i].RunID > records[j].RunID
	})
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}
