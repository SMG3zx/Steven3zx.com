package persistence

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is a Store that keeps its data in maps, for tests of callers that need
// no database. It follows the rules the persistence actor enforces: ownership,
// uniqueness, the job state machines and the cascade when something is deleted.
// Like the Client, every error it returns is a *ReplyError, and a constraint
// violation reads "constraint failed: ..." as SQLite's does.
//
// It stores GitHub tokens as given, since there is no database to protect.
type Memory struct {
	mu   sync.Mutex
	last time.Time

	users      map[string]*memUser
	creds      map[string]memCredential // by user ID
	identities map[memIdentity]string    // provider and subject to user ID
	github     map[string]memGitHub      // by user ID
	projects   map[string]*memProject
	snapshots  map[string]*memSnapshot
	imports    map[string]*memImport
	builds     map[string]*memBuild
}

var _ Store = (*Memory)(nil)

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{
		users:      map[string]*memUser{},
		creds:      map[string]memCredential{},
		identities: map[memIdentity]string{},
		github:     map[string]memGitHub{},
		projects:   map[string]*memProject{},
		snapshots:  map[string]*memSnapshot{},
		imports:    map[string]*memImport{},
		builds:     map[string]*memBuild{},
	}
}

type memUser struct{ id, email, displayName string }
type memCredential struct{ email, passwordHash string }
type memIdentity struct{ provider, subject string }
type memGitHub struct{ subject, token string }

type memProject struct {
	id, ownerID, name, description string
	createdAt, updatedAt           string
}

type memSnapshot struct {
	ProjectSnapshot
	ownerID, projectID, jobID string
	repositoryID              int64
	metadataJSON, manifest    string
	state                     string
	files                     map[string]SnapshotFile
}

type memImport struct {
	ImportJob
	repositoryID int64
}

type memBuild struct {
	WASMBuild
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

// fail wraps a message the way the persistence actor reports an error.
func fail(format string, args ...any) error {
	return &ReplyError{Err: fmt.Errorf(format, args...)}
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

func (m *Memory) CreateLocalUser(_ context.Context, user CreateLocalUser) error {
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

func (m *Memory) FindLocalUser(_ context.Context, email string) (LocalUserFound, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	email = strings.TrimSpace(email)
	for id, c := range m.creds {
		if strings.EqualFold(c.email, email) {
			return LocalUserFound{ID: id, Email: c.email, PasswordHash: c.passwordHash, Found: true}, nil
		}
	}
	return LocalUserFound{}, nil
}

func (m *Memory) FindUser(_ context.Context, id string) (UserFound, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return UserFound{}, nil
	}
	return UserFound{ID: u.id, Email: u.email, DisplayName: u.displayName, Found: true}, nil
}

func (m *Memory) ResolveGoogleIdentity(_ context.Context, identity ResolveGoogleIdentity) (GoogleIdentityResolved, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memIdentity{"google", identity.Subject}
	if id, ok := m.identities[key]; ok {
		u := m.users[id]
		return GoogleIdentityResolved{ID: u.id, Email: u.email, DisplayName: u.displayName}, nil
	}
	id := newID()
	m.users[id] = &memUser{id: id, email: identity.Email, displayName: identity.DisplayName}
	m.identities[key] = id
	return GoogleIdentityResolved{ID: id, Email: identity.Email, DisplayName: identity.DisplayName}, nil
}

func (m *Memory) ResolveGitHubLogin(_ context.Context, login ResolveGitHubLogin) (GitHubLoginResolved, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memIdentity{"github", login.Subject}
	if id, ok := m.identities[key]; ok {
		u := m.users[id]
		return GitHubLoginResolved{ID: u.id, Email: u.email, DisplayName: u.displayName}, nil
	}
	email := strings.TrimSpace(login.Email)
	if email == "" {
		return GitHubLoginResolved{}, fail("GitHub did not return a verified primary email")
	}
	for _, u := range m.users {
		if u.email != "" && strings.EqualFold(u.email, email) {
			return GitHubLoginResolved{EmailInUse: true}, nil
		}
	}
	id := newID()
	name := strings.TrimSpace(login.DisplayName)
	m.users[id] = &memUser{id: id, email: email, displayName: name}
	m.identities[key] = id
	return GitHubLoginResolved{ID: id, Email: email, DisplayName: name}, nil
}

func (m *Memory) ConnectGitHub(_ context.Context, connection ConnectGitHub) error {
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
func (m *Memory) view(p *memProject) Project {
	out := Project{ID: p.id, OwnerID: p.ownerID, Name: p.name, Description: p.description,
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

func (m *Memory) CreateProject(_ context.Context, project CreateProject) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[project.ID]; ok {
		return Project{}, constraint("UNIQUE constraint failed: projects.id")
	}
	if _, ok := m.users[project.OwnerID]; !ok {
		return Project{}, constraint("FOREIGN KEY constraint failed")
	}
	now := m.now()
	p := &memProject{id: project.ID, ownerID: project.OwnerID, name: strings.TrimSpace(project.Name),
		description: strings.TrimSpace(project.Description), createdAt: now, updatedAt: now}
	m.projects[p.id] = p
	return m.view(p), nil
}

func (m *Memory) UpdateProject(_ context.Context, project UpdateProject) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.ownedProject(project.ID, project.OwnerID)
	if !ok {
		return Project{}, fail("project not found")
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

func (m *Memory) ListProjects(_ context.Context, ownerID string) ([]Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make([]*memProject, 0)
	for _, p := range m.projects {
		if p.ownerID == ownerID {
			owned = append(owned, p)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].updatedAt > owned[j].updatedAt })
	projects := make([]Project, 0, len(owned))
	for _, p := range owned {
		projects = append(projects, m.view(p))
	}
	return projects, nil
}

func (m *Memory) GetProject(_ context.Context, id, ownerID string) (Project, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.ownedProject(id, ownerID)
	if !ok {
		return Project{}, false, nil
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

func (m *Memory) ListSnapshots(_ context.Context, ownerID, projectID string) ([]ProjectSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.ownedProject(projectID, ownerID); !ok {
		return nil, fail("project not found")
	}
	snapshots := make([]ProjectSnapshot, 0, 5)
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

func (m *Memory) SetProjectEntryPoint(_ context.Context, entry SetProjectEntryPoint) error {
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

func (m *Memory) ListProjectFiles(_ context.Context, page ListProjectFiles) ([]SnapshotFile, error) {
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
	files := make([]SnapshotFile, 0)
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

func copyFile(f SnapshotFile) SnapshotFile {
	f.Content = append([]byte(nil), f.Content...)
	return f
}

// Jobs: imports, Staging Snapshots, WASM Builds and Resume.

func (m *Memory) CreateImportJob(_ context.Context, job CreateImportJob) error {
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
	m.imports[job.ID] = &memImport{ImportJob: ImportJob{ID: job.ID, OwnerID: job.OwnerID, ProjectID: job.ProjectID,
		Repository: job.Repository, Branch: job.Branch, Commit: job.Commit, Status: "queued", CreatedAt: now, UpdatedAt: now}}
	return nil
}

func (m *Memory) UpdateImportJob(_ context.Context, update UpdateImportJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.imports[update.ID]
	if !ok {
		return fail("job not found")
	}
	if !allowedTransition(importTransitions, job.Status, update.Status) {
		return fail("job cannot move from %s to %s", job.Status, update.Status)
	}
	job.Status, job.Progress, job.Error, job.UpdatedAt = update.Status, update.Progress, update.Error, m.now()
	return nil
}

func (m *Memory) FindImportJob(_ context.Context, id, ownerID string) (ImportJob, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.imports[id]
	if !ok || job.OwnerID != ownerID {
		return ImportJob{}, false, nil
	}
	return job.ImportJob, true, nil
}

func (m *Memory) BeginSnapshot(_ context.Context, snapshot BeginSnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.ownedProject(snapshot.ProjectID, snapshot.OwnerID); !ok {
		return fail("project not found")
	}
	if _, ok := m.snapshots[snapshot.ID]; ok {
		return constraint("UNIQUE constraint failed: project_snapshots.id")
	}
	m.snapshots[snapshot.ID] = &memSnapshot{
		ProjectSnapshot: ProjectSnapshot{ID: snapshot.ID, Repository: snapshot.Repository, Branch: snapshot.Branch,
			Commit: snapshot.Commit, Language: snapshot.Language, Runtime: snapshot.Runtime,
			DefaultBranch: snapshot.DefaultBranch, Stars: snapshot.Stars, Forks: snapshot.Forks, CreatedAt: m.now()},
		ownerID: snapshot.OwnerID, projectID: snapshot.ProjectID, jobID: snapshot.JobID,
		repositoryID: snapshot.RepositoryID, metadataJSON: snapshot.MetadataJSON, manifest: snapshot.Manifest,
		state: "staging", files: map[string]SnapshotFile{},
	}
	return nil
}

func (m *Memory) AddSnapshotFiles(_ context.Context, snapshotID string, files []SnapshotFile) error {
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

func (m *Memory) CompleteSnapshot(_ context.Context, snapshot CompleteSnapshot) error {
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

func (m *Memory) FindStagingSnapshot(_ context.Context, jobID string) (StagingSnapshotFound, error) {
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
		return StagingSnapshotFound{SnapshotID: s.ID, Found: true, Files: files, Manifest: s.manifest}, nil
	}
	return StagingSnapshotFound{}, nil
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

func (m *Memory) CreateWASMBuild(_ context.Context, build CreateWASMBuild) error {
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
	m.builds[build.ID] = &memBuild{ownerID: build.OwnerID, WASMBuild: WASMBuild{ID: build.ID, ProjectID: build.ProjectID,
		SnapshotID: build.SnapshotID, Target: build.Target, Status: "queued", CreatedAt: now, UpdatedAt: now}}
	return nil
}

func (m *Memory) UpdateWASMBuild(_ context.Context, update UpdateWASMBuild) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[update.ID]
	if !ok {
		return fail("job not found")
	}
	if !allowedTransition(wasmTransitions, build.Status, update.Status) {
		return fail("WASM build cannot move from %s to %s", build.Status, update.Status)
	}
	build.Status, build.Progress, build.Error, build.Log, build.UpdatedAt = update.Status, update.Progress, update.Error, update.Log, m.now()
	return nil
}

func (m *Memory) CompleteWASMBuild(_ context.Context, complete CompleteWASMBuild) error {
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

func (m *Memory) FindWASMBuild(_ context.Context, id, ownerID string) (WASMBuild, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[id]
	if !ok || build.ownerID != ownerID {
		return WASMBuild{}, false, nil
	}
	return build.WASMBuild, true, nil
}

func (m *Memory) FindWASMBuildArtifact(_ context.Context, id, ownerID string) (WASMBuildArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[id]
	if !ok || build.ownerID != ownerID || build.Status != "complete" {
		return WASMBuildArtifact{}, nil
	}
	return WASMBuildArtifact{Name: build.ArtifactName, Data: append([]byte(nil), build.artifact...), Found: true}, nil
}

func (m *Memory) ListResumableImports(_ context.Context) ([]ResumableImport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var queued []*memImport
	for _, job := range m.imports {
		if job.Status == "queued" {
			queued = append(queued, job)
		}
	}
	sort.Slice(queued, func(i, j int) bool { return queued[i].CreatedAt < queued[j].CreatedAt })
	var jobs []ResumableImport
	for _, job := range queued {
		jobs = append(jobs, ResumableImport{JobID: job.ID, OwnerID: job.OwnerID, ProjectID: job.ProjectID,
			Repository: job.Repository, Branch: job.Branch, Commit: job.Commit})
	}
	return jobs, nil
}

func (m *Memory) ListResumableBuilds(_ context.Context) ([]ResumableBuild, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var queued []*memBuild
	for _, build := range m.builds {
		if build.Status == "queued" {
			queued = append(queued, build)
		}
	}
	sort.Slice(queued, func(i, j int) bool { return queued[i].CreatedAt < queued[j].CreatedAt })
	var builds []ResumableBuild
	for _, build := range queued {
		s, ok := m.snapshots[build.SnapshotID]
		if !ok {
			continue
		}
		builds = append(builds, ResumableBuild{JobID: build.ID, OwnerID: build.ownerID, ProjectID: build.ProjectID,
			SnapshotID: build.SnapshotID, Target: build.Target, Language: s.Language})
	}
	return builds, nil
}

func (m *Memory) ListActiveJobs(_ context.Context, ownerID string) ([]ActiveJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeJobs(ownerID), nil
}

func (m *Memory) activeJobs(ownerID string) []ActiveJob {
	jobs := make([]ActiveJob, 0)
	for _, job := range m.imports {
		if job.OwnerID == ownerID && isIn(job.Status, activeImportStatuses) {
			jobs = append(jobs, ActiveJob{Kind: "import", ID: job.ID, ProjectID: job.ProjectID, Status: job.Status})
		}
	}
	for _, build := range m.builds {
		if build.ownerID == ownerID && isIn(build.Status, activeBuildStatuses) {
			jobs = append(jobs, ActiveJob{Kind: "wasm_build", ID: build.ID, ProjectID: build.ProjectID, Status: build.Status})
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

func (m *Memory) DeleteAccount(_ context.Context, userID string, cancelActiveJobs bool) (AccountDeleted, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return AccountDeleted{}, nil
	}
	if active := m.activeJobs(userID); len(active) > 0 && !cancelActiveJobs {
		return AccountDeleted{ActiveJobs: active}, nil
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
	delete(m.github, userID)
	delete(m.creds, userID)
	delete(m.users, userID)
	return AccountDeleted{Deleted: true}, nil
}
