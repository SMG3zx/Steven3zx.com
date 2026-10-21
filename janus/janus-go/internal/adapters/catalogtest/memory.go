// Package catalogtest holds an in-memory app.Catalog and the contract suite
// every app.Catalog adapter, real or test, must pass.
package catalogtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"janus/internal/app"
	"janus/internal/domain/scan"
)

// Memory is an app.Catalog over maps, safe for concurrent use. An Import it
// accepts runs at once: by the time RequestImport returns, the Snapshot is
// ready. The repositories it can import from are the ones Publish wrote, and an
// Owner can import only after Connect.
type Memory struct {
	mu        sync.Mutex
	clock     int
	projects  map[string]*project
	snapshots map[string]*snapshot
	imports   map[string]*app.ImportJob
	repos     map[string]*repository
	connected map[string]bool
}

type project struct {
	app.Project
	updated int
}

type snapshot struct {
	app.ProjectSnapshot
	projectID, ownerID, metadataJSON string
	repositoryID                     int64
	order                            int
	files                            []app.SnapshotFile
}

type repository struct {
	app.Repository
	commits []commit // newest first
}

type commit struct {
	sha   string
	files map[string]string
}

var _ app.Catalog = (*Memory)(nil)

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	return &Memory{projects: map[string]*project{}, snapshots: map[string]*snapshot{}, imports: map[string]*app.ImportJob{},
		repos: map[string]*repository{}, connected: map[string]bool{}}
}

// Connect gives owner a GitHub Connection, so it can list and import.
func (m *Memory) Connect(owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected[owner] = true
}

// Publish makes files the newest commit of fullName's main branch, creating
// the repository on first use, and returns the commit's SHA.
func (m *Memory) Publish(fullName string, files map[string]string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.repos[fullName]
	if r == nil {
		r = &repository{Repository: app.Repository{ID: int64(len(m.repos) + 1), FullName: fullName, DefaultBranch: "main", Language: "Go", Stars: 3, Forks: 1}}
		m.repos[fullName] = r
	}
	sha := fmt.Sprintf("%040x", len(m.repos)*1000+len(r.commits)+1)
	r.commits = append([]commit{{sha: sha, files: files}}, r.commits...)
	return sha
}

// Seed is a ready Snapshot SeedSnapshot adds without an Import.
type Seed struct {
	ID, Repository, Commit string
	Language, Runtime      string
	MetadataJSON           string
	TotalBytes             int64
	Files                  []app.SnapshotFile
}

// SeedSnapshot adds a ready Snapshot to an Owner's Project and makes it the
// active one, as a finished Import would.
func (m *Memory) SeedSnapshot(owner, projectID string, seed Seed) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if seed.MetadataJSON == "" {
		seed.MetadataJSON = "{}"
	}
	total := seed.TotalBytes
	if total == 0 {
		for _, f := range seed.Files {
			total += int64(len(f.Content))
		}
	}
	m.addSnapshot(owner, projectID, &snapshot{ProjectSnapshot: app.ProjectSnapshot{ID: seed.ID, Repository: seed.Repository, Branch: "main", Commit: seed.Commit,
		Language: seed.Language, Runtime: seed.Runtime, DefaultBranch: "main", FileCount: len(seed.Files), TotalBytes: total},
		metadataJSON: seed.MetadataJSON, files: append([]app.SnapshotFile(nil), seed.Files...)})
}

// SeedImport adds an Import of an Owner's Project in status, as one a worker
// is running.
func (m *Memory) SeedImport(owner, projectID, jobID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.tick()
	m.imports[jobID] = &app.ImportJob{ID: jobID, OwnerID: owner, ProjectID: projectID, Repository: "seeded/repo", Branch: "main", Commit: strings.Repeat("0", 40), Status: status, CreatedAt: now, UpdatedAt: now}
}

func (m *Memory) tick() string {
	m.clock++
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(m.clock) * time.Millisecond).Format("2006-01-02 15:04:05.000")
}

func refused(format string, args ...any) error {
	return &app.ReplyError{Err: fmt.Errorf(format, args...)}
}

func (m *Memory) owned(id, owner string) (*project, bool) {
	p, ok := m.projects[id]
	if !ok || p.OwnerID != owner {
		return nil, false
	}
	return p, true
}

// view is the Project with its active Snapshot's details.
func (m *Memory) view(p *project) app.Project {
	view := p.Project
	view.MetadataJSON = "{}"
	for _, s := range m.snapshots {
		if s.projectID != p.ID {
			continue
		}
		view.SnapshotCount++
		if s.Active {
			view.SnapshotID, view.MetadataJSON, view.RepositoryID = s.ID, s.metadataJSON, s.repositoryID
			view.Repository, view.DefaultBranch, view.Branch, view.Commit = s.Repository, s.DefaultBranch, s.Branch, s.Commit
			view.EntryPoint, view.Language, view.Runtime = s.EntryPoint, s.Language, s.Runtime
			view.Stars, view.Forks, view.FileCount, view.TotalBytes = s.Stars, s.Forks, s.FileCount, s.TotalBytes
		}
	}
	return view
}

func validProject(name, description string) error {
	if strings.TrimSpace(name) == "" || len(name) > 120 || len(description) > 2000 {
		return refused("project name (1-120 characters) and description (up to 2000 characters) are required")
	}
	return nil
}

func (m *Memory) CreateProject(_ context.Context, request app.CreateProject) (app.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validProject(request.Name, request.Description); err != nil {
		return app.Project{}, err
	}
	if _, exists := m.projects[request.ID]; exists {
		return app.Project{}, refused("project could not be created")
	}
	now := m.tick()
	p := &project{Project: app.Project{ID: request.ID, OwnerID: request.OwnerID, Name: strings.TrimSpace(request.Name), Description: strings.TrimSpace(request.Description), CreatedAt: now, UpdatedAt: now}, updated: m.clock}
	m.projects[p.ID] = p
	return m.view(p), nil
}

func (m *Memory) UpdateProject(_ context.Context, request app.UpdateProject) (app.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validProject(request.Name, request.Description); err != nil {
		return app.Project{}, err
	}
	p, ok := m.owned(request.ID, request.OwnerID)
	if !ok {
		return app.Project{}, refused("project not found")
	}
	p.Name, p.Description, p.UpdatedAt, p.updated = strings.TrimSpace(request.Name), strings.TrimSpace(request.Description), m.tick(), m.clock
	return m.view(p), nil
}

func (m *Memory) DeleteProject(_ context.Context, id, owner string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.owned(id, owner); !ok {
		return false, nil
	}
	for _, job := range m.imports {
		if job.ProjectID == id && active(job.Status) {
			return false, refused("project has an import in progress")
		}
	}
	m.deleteProject(id)
	return true, nil
}

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
}

func (m *Memory) ListProjects(_ context.Context, owner string) ([]app.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var mine []*project
	for _, p := range m.projects {
		if p.OwnerID == owner {
			mine = append(mine, p)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].updated > mine[j].updated })
	list := make([]app.Project, 0, len(mine))
	for _, p := range mine {
		list = append(list, m.view(p))
	}
	return list, nil
}

func (m *Memory) GetProject(_ context.Context, id, owner string) (app.Project, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.owned(id, owner)
	if !ok {
		return app.Project{}, false, nil
	}
	return m.view(p), true, nil
}

// ready returns a Project's Snapshots, newest first.
func (m *Memory) ready(projectID string) []*snapshot {
	var list []*snapshot
	for _, s := range m.snapshots {
		if s.projectID == projectID {
			list = append(list, s)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].order > list[j].order })
	return list
}

func (m *Memory) ListSnapshots(_ context.Context, owner, projectID string) ([]app.ProjectSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.owned(projectID, owner); !ok {
		return nil, refused("project not found")
	}
	list := make([]app.ProjectSnapshot, 0)
	for _, s := range m.ready(projectID) {
		list = append(list, s.ProjectSnapshot)
	}
	return list, nil
}

func (m *Memory) ActivateSnapshot(_ context.Context, owner, projectID, snapshotID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snapshots[snapshotID]
	if !ok || s.projectID != projectID || s.ownerID != owner {
		return false, nil
	}
	for _, other := range m.ready(projectID) {
		other.Active = false
	}
	s.Active = true
	return true, nil
}

func (m *Memory) SetEntryPoint(_ context.Context, owner, projectID, snapshotID, entryPoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.owned(projectID, owner)
	if !ok {
		return refused("project not found")
	}
	if view := m.view(p); view.SnapshotID != snapshotID {
		return refused("entrypoint selection must target the latest snapshot")
	}
	s := m.snapshots[snapshotID]
	var metadata struct {
		Scan scan.Result `json:"scan"`
	}
	if err := json.Unmarshal([]byte(s.metadataJSON), &metadata); err != nil {
		return refused("project scan metadata is unavailable")
	}
	if !metadata.Scan.AcceptsEntrypoint(entryPoint) {
		return refused("entrypoint must match a detected candidate")
	}
	s.EntryPoint = entryPoint
	return nil
}

func (m *Memory) ListProjectFiles(_ context.Context, page app.ListProjectFiles) ([]app.SnapshotFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.owned(page.ProjectID, page.OwnerID); !ok {
		return nil, refused("project not found")
	}
	s, ok := m.snapshots[page.SnapshotID]
	if !ok || s.projectID != page.ProjectID {
		return []app.SnapshotFile{}, nil
	}
	files := append([]app.SnapshotFile(nil), s.files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	limit := page.Limit
	if limit <= 0 {
		limit = 200
	}
	start := min(page.Offset, len(files))
	end := min(start+limit, len(files))
	out := make([]app.SnapshotFile, 0, end-start)
	for _, f := range files[start:end] {
		f.Content = append([]byte(nil), f.Content...)
		out = append(out, f)
	}
	return out, nil
}

func active(status string) bool {
	switch status {
	case "queued", "cloning", "scanning", "storing":
		return true
	}
	return false
}

// RequestImport checks the request as the projects service does and, when it
// can run, imports the commit at once.
func (m *Memory) RequestImport(_ context.Context, request app.ImportRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if request.OwnerID == "" || request.ProjectID == "" || request.Repository == "" || request.Branch == "" || len(request.Commit) != 40 {
		return "", refused("project, repository, branch and full commit SHA are required")
	}
	if _, ok := m.owned(request.ProjectID, request.OwnerID); !ok {
		return "", refused("project not found")
	}
	if !m.connected[request.OwnerID] {
		return "", refused("connect GitHub before importing a repository")
	}
	r, ok := m.repos[request.Repository]
	if !ok {
		return "", refused("GitHub API returned status 404")
	}
	if request.Branch != "main" {
		return "", refused("selected branch does not belong to this repository")
	}
	var chosen *commit
	for i := range r.commits {
		if strings.EqualFold(r.commits[i].sha, request.Commit) {
			chosen = &r.commits[i]
		}
	}
	if chosen == nil {
		return "", refused("selected commit is not among the branch's 50 most recent commits")
	}
	for _, job := range m.imports {
		if job.ProjectID == request.ProjectID && active(job.Status) {
			return "", refused("an import of this project is already in progress")
		}
	}
	jobID := uuid.NewString()
	now := m.tick()
	m.imports[jobID] = &app.ImportJob{ID: jobID, OwnerID: request.OwnerID, ProjectID: request.ProjectID, Repository: r.FullName, Branch: request.Branch,
		Commit: chosen.sha, Status: "complete", Progress: 100, CreatedAt: now, UpdatedAt: now}
	files := make([]app.SnapshotFile, 0, len(chosen.files))
	input := make([]scan.File, 0, len(chosen.files))
	var total int64
	for path, content := range chosen.files {
		files = append(files, app.SnapshotFile{Path: path, Mode: "100644", Hash: fmt.Sprintf("%x", len(content)), Content: []byte(content)})
		input = append(input, scan.File{Path: path, Content: []byte(content)})
		total += int64(len(content))
	}
	found := scan.Files(input)
	metadata, err := json.Marshal(struct {
		Repository app.Repository `json:"repository"`
		Scan       scan.Result    `json:"scan"`
	}{r.Repository, found})
	if err != nil {
		return "", err
	}
	m.addSnapshot(request.OwnerID, request.ProjectID, &snapshot{ProjectSnapshot: app.ProjectSnapshot{ID: uuid.NewString(), Repository: r.FullName, Branch: "main", Commit: chosen.sha,
		Language: scan.DominantLanguage(found.Languages), Runtime: found.Runtime, DefaultBranch: "main", Stars: r.Stars, Forks: r.Forks, FileCount: len(files), TotalBytes: total},
		repositoryID: r.ID, metadataJSON: string(metadata), files: files})
	return jobID, nil
}

// addSnapshot makes s the active Snapshot of the Project and keeps the newest
// five.
func (m *Memory) addSnapshot(owner, projectID string, s *snapshot) {
	for _, other := range m.ready(projectID) {
		other.Active = false
	}
	s.projectID, s.ownerID, s.Active, s.order, s.CreatedAt = projectID, owner, true, m.clock+1, m.tick()
	m.snapshots[s.ID] = s
	if p := m.projects[projectID]; p != nil {
		p.updated = m.clock
	}
	for i, old := range m.ready(projectID) {
		if i >= 5 {
			delete(m.snapshots, old.ID)
		}
	}
}

func (m *Memory) RetryImport(ctx context.Context, owner, jobID string) (string, error) {
	m.mu.Lock()
	job, ok := m.imports[jobID]
	if !ok || job.OwnerID != owner {
		m.mu.Unlock()
		return "", refused("import job not found")
	}
	if job.Status != "failed" {
		m.mu.Unlock()
		return "", refused("only a failed import can be retried")
	}
	request := app.ImportRequest{OwnerID: owner, ProjectID: job.ProjectID, Repository: job.Repository, Branch: job.Branch, Commit: job.Commit}
	m.mu.Unlock()
	return m.RequestImport(ctx, request)
}

func (m *Memory) FindImportJob(_ context.Context, id, owner string) (app.ImportJob, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.imports[id]
	if !ok || job.OwnerID != owner {
		return app.ImportJob{}, false, nil
	}
	return *job, true, nil
}

func (m *Memory) ActiveImports(_ context.Context, owner string) ([]app.ActiveJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]app.ActiveJob, 0)
	for _, job := range m.imports {
		if job.OwnerID == owner && active(job.Status) {
			list = append(list, app.ActiveJob{Kind: "import", ID: job.ID, ProjectID: job.ProjectID, Status: job.Status})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

func (m *Memory) DeleteOwner(_ context.Context, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.projects {
		if p.OwnerID == owner {
			m.deleteProject(id)
		}
	}
	return nil
}

func (m *Memory) Repositories(_ context.Context, owner string) (app.RepositoryList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := app.RepositoryList{Repositories: []app.Repository{}, Connected: m.connected[owner]}
	if !list.Connected {
		return list, nil
	}
	names := make([]string, 0, len(m.repos))
	for name := range m.repos {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		list.Repositories = append(list.Repositories, m.repos[name].Repository)
	}
	return list, nil
}

func (m *Memory) connectedRepository(owner, name string) (*repository, error) {
	if !m.connected[owner] {
		return nil, refused("connect GitHub before importing a repository")
	}
	r, ok := m.repos[name]
	if !ok {
		return nil, errors.New("GitHub API returned status 404")
	}
	return r, nil
}

func (m *Memory) Branches(_ context.Context, owner, name string) ([]app.Branch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.connectedRepository(owner, name)
	if err != nil {
		return nil, err
	}
	branch := app.Branch{Name: "main"}
	branch.Commit.SHA = r.commits[0].sha
	return []app.Branch{branch}, nil
}

func (m *Memory) Commits(_ context.Context, owner, name, branch string) ([]app.Commit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.connectedRepository(owner, name)
	if err != nil {
		return nil, err
	}
	commits := make([]app.Commit, 0, len(r.commits))
	if branch != "main" {
		return commits, nil
	}
	for _, c := range r.commits {
		commits = append(commits, app.Commit{SHA: c.sha})
	}
	return commits, nil
}
