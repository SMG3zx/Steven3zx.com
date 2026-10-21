package storetest

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"janus/internal/app"
)

// Memory is an app.Store that keeps its data in maps, for tests of callers that need
// no database. It follows the rules the SQL store enforces: ownership,
// uniqueness, the job state machines and the cascade when something is deleted.
// Like the SQL store, every error it returns is a *ReplyError, and a constraint
// violation reads "constraint failed: ..." as SQLite's does.
type Memory struct {
	mu   sync.Mutex
	last time.Time

	users       map[string]struct{} // the Owners
	projects    map[string]*memProject
	snapshots   map[string]*memSnapshot
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
		users:       map[string]struct{}{},
		projects:    map[string]*memProject{},
		snapshots:   map[string]*memSnapshot{},
		builds:      map[string]*memBuild{},
		codes:       map[string]memCode{},
		agents:      map[string]app.AgentRecord{},
		runs:        map[string]app.RunUsageRecord{},
		checks:      map[string]*memCheck{},
		deployments: map[string]*memDeployment{},
	}
}

type memCode struct {
	ownerID   string
	expiresAt time.Time
	used      bool
}

type memProject struct{ id, ownerID string }

type memSnapshot struct{ app.SnapshotRef }

type memBuild struct {
	app.WASMBuild
	ownerID string
}

// activeBuildStatuses are the statuses of a WASM build that has not finished.
var activeBuildStatuses = []string{"queued", "building"}

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

// Owners.

func (m *Memory) AddOwner(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[id] = struct{}{}
	return nil
}

// References to the projects service's Projects and Snapshots.

func (m *Memory) AddProject(_ context.Context, project app.ProjectRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[project.OwnerID]; !ok {
		return constraint("FOREIGN KEY constraint failed")
	}
	if _, ok := m.projects[project.ID]; !ok {
		m.projects[project.ID] = &memProject{id: project.ID, ownerID: project.OwnerID}
	}
	return nil
}

func (m *Memory) AddSnapshot(_ context.Context, snapshot app.SnapshotRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[snapshot.ProjectID]; !ok {
		return constraint("FOREIGN KEY constraint failed")
	}
	if _, ok := m.snapshots[snapshot.ID]; !ok {
		m.snapshots[snapshot.ID] = &memSnapshot{SnapshotRef: snapshot}
	}
	return nil
}

func (m *Memory) FindSnapshot(_ context.Context, id, ownerID string) (app.SnapshotRef, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.snapshots[id]
	if !ok || s.OwnerID != ownerID {
		return app.SnapshotRef{}, false, nil
	}
	return s.SnapshotRef, true, nil
}

func (m *Memory) ListOwnedProjects(_ context.Context, ownerID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0)
	for id, p := range m.projects {
		if p.ownerID == ownerID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (m *Memory) RemoveProject(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteProject(id)
	return nil
}

func (m *Memory) RemoveSnapshot(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteSnapshot(id)
	return nil
}

// deleteProject removes a project reference and everything that refers to it.
func (m *Memory) deleteProject(id string) {
	delete(m.projects, id)
	for sid, s := range m.snapshots {
		if s.ProjectID == id {
			m.deleteSnapshot(sid)
		}
	}
	for bid, build := range m.builds {
		if build.ProjectID == id {
			m.deleteBuild(bid)
		}
	}
	for did, dep := range m.deployments {
		if dep.ProjectID == id {
			delete(m.deployments, did)
		}
	}
}

// deleteSnapshot removes a Snapshot reference and the WASM builds that
// compile it.
func (m *Memory) deleteSnapshot(id string) {
	delete(m.snapshots, id)
	for bid, build := range m.builds {
		if build.SnapshotID == id {
			m.deleteBuild(bid)
		}
	}
}

// deleteBuild removes a WASM build with its Check and Deployments.
func (m *Memory) deleteBuild(id string) {
	delete(m.builds, id)
	delete(m.checks, id)
	for did, dep := range m.deployments {
		if dep.BuildID == id {
			delete(m.deployments, did)
		}
	}
}

// WASM Builds, as the references Deployments need. The builds service owns
// them; ApplyWASMBuild stores what it publishes, and a Build whose Project or
// Snapshot is not referenced is not kept.

func (m *Memory) ApplyWASMBuild(_ context.Context, change app.WASMBuildChange) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	project, known := m.projects[change.ProjectID]
	_, snapshotKnown := m.snapshots[change.SnapshotID]
	if !known || project.ownerID != change.OwnerID || !snapshotKnown {
		return nil
	}
	build, ok := m.builds[change.ID]
	if !ok {
		build = &memBuild{ownerID: change.OwnerID, WASMBuild: app.WASMBuild{ID: change.ID, ProjectID: change.ProjectID,
			SnapshotID: change.SnapshotID, Target: change.Target}}
		m.builds[change.ID] = build
	}
	build.Status, build.Error = change.Status, change.Error
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

func (m *Memory) ListActiveJobs(_ context.Context, ownerID string) ([]app.ActiveJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeJobs(ownerID), nil
}

func (m *Memory) activeJobs(ownerID string) []app.ActiveJob {
	jobs := make([]app.ActiveJob, 0)
	for _, build := range m.builds {
		if build.ownerID == ownerID && isIn(build.Status, activeBuildStatuses) {
			jobs = append(jobs, app.ActiveJob{Kind: "wasm_build", ID: build.ID, ProjectID: build.ProjectID, Status: build.Status})
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
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
	for id, build := range m.builds {
		if build.ownerID == userID {
			delete(m.builds, id)
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
