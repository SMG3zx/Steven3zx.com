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

	users     map[string]struct{} // the Owners
	projects  map[string]*memProject
	snapshots map[string]*memSnapshot
	builds    map[string]*memBuild
	checks    map[string]*memCheck // by WASM Build ID
}

var _ app.Store = (*Memory)(nil)

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{
		users:     map[string]struct{}{},
		projects:  map[string]*memProject{},
		snapshots: map[string]*memSnapshot{},
		builds:    map[string]*memBuild{},
		checks:    map[string]*memCheck{},
	}
}

// memCheck is a Check and the Owner of the WASM Build it checked.
type memCheck struct {
	app.Check
	ownerID string
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

// deleteBuild removes a WASM build with its Check.
func (m *Memory) deleteBuild(id string) {
	delete(m.builds, id)
	delete(m.checks, id)
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

func (m *Memory) DeleteAccount(_ context.Context, userID string) (app.AccountDeleted, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return app.AccountDeleted{}, nil
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
	delete(m.users, userID)
	return app.AccountDeleted{Deleted: true}, nil
}

// ApplyCheck stores the Check of a Build the builds service published. A Check
// of a Build that is not referenced here is not kept.
func (m *Memory) ApplyCheck(_ context.Context, change app.CheckChange) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[change.BuildID]
	if !ok || build.ownerID != change.OwnerID {
		return nil
	}
	m.checks[change.BuildID] = &memCheck{ownerID: change.OwnerID, Check: app.Check{BuildID: change.BuildID, Status: change.Status,
		Error: change.Error, Log: change.Log, NoTests: change.NoTests}}
	return nil
}

func (m *Memory) FindCheck(_ context.Context, buildID, ownerID string) (app.Check, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.checks[buildID]
	if !ok || stored.ownerID != ownerID {
		return app.Check{}, false, nil
	}
	return stored.Check, true, nil
}
