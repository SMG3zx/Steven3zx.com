package projects

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/contract"
	contractaudit "janus/internal/contract/audit"
	contractid "janus/internal/contract/identity"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
	"janus/internal/services/audit"
	"janus/internal/services/projects/githubtest"
)

// mainGo is a Go entrypoint the scan detects.
const mainGo = "package main\n\nfunc main() {}\n"

// harness is a projects service on its own NATS server, with a fake identity
// service and a fake GitHub.
type harness struct {
	t      *testing.T
	conn   *nats.Conn
	github *githubtest.GitHub
	svc    *Service
	path   string

	mu        sync.Mutex
	connected map[string]bool
	// audits receives the import outcomes the service audits.
	audits chan contractaudit.Event
	events chan *nats.Msg
}

func start(t *testing.T) *harness {
	t.Helper()
	hub := natstest.Start(t)
	h := &harness{t: t, conn: hub.Conn(), github: githubtest.New(t), path: filepath.Join(t.TempDir(), "projects.db"),
		connected: map[string]bool{"alice": true, "bob": true}, audits: make(chan contractaudit.Event, 100), events: make(chan *nats.Msg, 100)}
	ctx := context.Background()
	identity, err := micro.AddService(h.conn, micro.Config{Name: "identity-fake", Version: "0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = identity.Stop() })
	if err := natsx.AddEndpoint(identity, "token", contractid.SubjectGitHubToken, func(_ context.Context, request contractid.GitHubToken) (contractid.GitHubTokenFound, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if !h.connected[request.UserID] {
			return contractid.GitHubTokenFound{}, nil
		}
		return contractid.GitHubTokenFound{Connected: true, Token: githubtest.Token}, nil
	}); err != nil {
		t.Fatal(err)
	}
	js, err := hub.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.DeclareStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	if _, err := h.conn.Subscribe(contractaudit.Subject(contractprojects.ServiceName), func(msg *nats.Msg) {
		var event contractaudit.Event
		if json.Unmarshal(msg.Data, &event) == nil && strings.HasPrefix(event.Name, "project.import.") && event.Name != "project.import.queued" {
			h.audits <- event
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.conn.Subscribe(contractprojects.SubjectEvents, func(msg *nats.Msg) { h.events <- msg }); err != nil {
		t.Fatal(err)
	}
	h.startService()
	return h
}

func (h *harness) startService() {
	h.t.Helper()
	h.svc = New(Config{DatabasePath: h.path, GitHubAPI: h.github.APIURL(), GitBase: h.github.GitBase(), AckWait: 3 * time.Second})
	if err := h.svc.Start(context.Background(), h.conn); err != nil {
		h.t.Fatal(err)
	}
	svc := h.svc
	h.t.Cleanup(func() { _ = svc.Stop(context.Background()) })
}

func (h *harness) call(subject string, request, reply any) error {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := natsx.Call(ctx, h.conn, subject, request, reply)
	var remote *natsx.RemoteError
	if errors.As(err, &remote) {
		return contract.FromStatus(remote.Status, remote.Message)
	}
	return err
}

func (h *harness) must(subject string, request, reply any) {
	h.t.Helper()
	if err := h.call(subject, request, reply); err != nil {
		h.t.Fatalf("%s: %v", subject, err)
	}
}

func (h *harness) project(owner, id string) {
	h.t.Helper()
	h.must(contractprojects.SubjectCreateProject, contractprojects.CreateProject{ID: id, OwnerID: owner, Name: id}, nil)
}

// importCommit requests an Import and waits until the service audits how it
// ended. It returns the job and that event's name.
func (h *harness) importCommit(owner, project, repository, commit string) (string, string) {
	h.t.Helper()
	var queued contractprojects.ImportQueued
	h.must(contractprojects.SubjectRequestImport, contractprojects.RequestImport{OwnerID: owner, ProjectID: project, Repository: repository, Branch: "main", Commit: commit}, &queued)
	return queued.JobID, h.waitImport(queued.JobID)
}

func (h *harness) waitImport(jobID string) string {
	h.t.Helper()
	for {
		select {
		case event := <-h.audits:
			if event.Fields["job_id"] == jobID {
				return event.Name
			}
		case <-time.After(30 * time.Second):
			h.t.Fatalf("import %s did not finish", jobID)
		}
	}
}

func (h *harness) event(subject string) *nats.Msg {
	h.t.Helper()
	for {
		select {
		case msg := <-h.events:
			if msg.Subject == subject {
				return msg
			}
		case <-time.After(10 * time.Second):
			h.t.Fatalf("no %s event", subject)
		}
	}
}

func (h *harness) getProject(owner, id string) contractprojects.ProjectFound {
	h.t.Helper()
	var found contractprojects.ProjectFound
	h.must(contractprojects.SubjectGetProject, contractprojects.ProjectKey{OwnerID: owner, ProjectID: id}, &found)
	return found
}

func TestProjectsAreOwnedAndValidated(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	if err := h.call(contractprojects.SubjectCreateProject, contractprojects.CreateProject{ID: "p1", OwnerID: "alice", Name: "again"}, nil); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("duplicate ID = %v, want a conflict", err)
	}
	if err := h.call(contractprojects.SubjectCreateProject, contractprojects.CreateProject{ID: "p2", OwnerID: "alice", Name: " "}, nil); !errors.Is(err, contract.ErrInvalid) {
		t.Fatalf("blank name = %v, want invalid", err)
	}
	if found := h.getProject("bob", "p1"); found.Found {
		t.Fatal("bob sees alice's project")
	}
	if err := h.call(contractprojects.SubjectUpdateProject, contractprojects.UpdateProject{ID: "p1", OwnerID: "bob", Name: "mine"}, nil); !errors.Is(err, contract.ErrNotFound) || err.Error() != "project not found" {
		t.Fatalf("bob renaming alice's project = %v, want not found", err)
	}
	var updated contractprojects.Project
	h.must(contractprojects.SubjectUpdateProject, contractprojects.UpdateProject{ID: "p1", OwnerID: "alice", Name: " Renamed ", Description: "d"}, &updated)
	if updated.Name != "Renamed" || updated.Description != "d" {
		t.Fatalf("updated = %+v", updated)
	}
	var list contractprojects.Projects
	h.must(contractprojects.SubjectListProjects, contractprojects.Owner{OwnerID: "bob"}, &list)
	if len(list.Projects) != 0 {
		t.Fatalf("bob's projects = %+v", list.Projects)
	}
	if err := h.call(contractprojects.SubjectListSnapshots, contractprojects.ProjectKey{OwnerID: "bob", ProjectID: "p1"}, nil); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("bob listing alice's snapshots = %v, want not found", err)
	}
}

func TestAnImportBecomesTheActiveSnapshot(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	commit := h.github.Commit(t, "alice/demo", map[string]string{"go.mod": "module demo\n", "cmd/server/main.go": mainGo})
	jobID, outcome := h.importCommit("alice", "p1", "alice/demo", commit)
	if outcome != "project.import.completed" {
		t.Fatalf("import ended with %s", outcome)
	}
	var job contractprojects.ImportFound
	h.must(contractprojects.SubjectFindImport, contractprojects.ImportKey{OwnerID: "alice", JobID: jobID}, &job)
	if !job.Found || job.Job.Status != "complete" || job.Job.Progress != 100 {
		t.Fatalf("job = %+v", job)
	}
	project := h.getProject("alice", "p1").Project
	if project.SnapshotID == "" || project.Language != "Go" || project.Commit != commit || project.FileCount != 2 || project.Repository != "alice/demo" || project.SnapshotCount != 1 {
		t.Fatalf("project after import = %+v", project)
	}
	if !strings.Contains(project.MetadataJSON, "cmd/server/main.go") || !strings.Contains(project.MetadataJSON, `"stargazers_count":3`) {
		t.Fatalf("metadata = %s", project.MetadataJSON)
	}
	var files contractprojects.Files
	h.must(contractprojects.SubjectSnapshotFiles, contractprojects.SnapshotFiles{OwnerID: "alice", ProjectID: "p1", SnapshotID: project.SnapshotID}, &files)
	if len(files.Files) != 2 || files.More || files.Files[0].Path != "cmd/server/main.go" || string(files.Files[0].Content) != mainGo || files.Files[0].Size != int64(len(mainGo)) {
		t.Fatalf("files = %+v", files)
	}
	if err := h.call(contractprojects.SubjectSnapshotFiles, contractprojects.SnapshotFiles{OwnerID: "bob", ProjectID: "p1", SnapshotID: project.SnapshotID}, nil); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("bob reading alice's files = %v", err)
	}

	if err := h.call(contractprojects.SubjectSetEntryPoint, contractprojects.SetEntryPoint{OwnerID: "alice", ProjectID: "p1", SnapshotID: project.SnapshotID, EntryPoint: "go.mod"}, nil); !errors.Is(err, contract.ErrInvalid) {
		t.Fatalf("an entrypoint that is not a candidate = %v", err)
	}
	h.must(contractprojects.SubjectSetEntryPoint, contractprojects.SetEntryPoint{OwnerID: "alice", ProjectID: "p1", SnapshotID: project.SnapshotID, EntryPoint: "cmd/server/main.go"}, nil)
	if got := h.getProject("alice", "p1").Project.EntryPoint; got != "cmd/server/main.go" {
		t.Fatalf("entrypoint = %q", got)
	}
}

func TestAnImportIsRefusedWhenItCannotRun(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	commit := h.github.Commit(t, "alice/demo", map[string]string{"main.go": mainGo})
	for name, c := range map[string]struct {
		request contractprojects.RequestImport
		want    string
	}{
		"short commit":   {contractprojects.RequestImport{OwnerID: "alice", ProjectID: "p1", Repository: "alice/demo", Branch: "main", Commit: "abc"}, "full commit SHA"},
		"no project":     {contractprojects.RequestImport{OwnerID: "alice", ProjectID: "nope", Repository: "alice/demo", Branch: "main", Commit: commit}, "project not found"},
		"another's":      {contractprojects.RequestImport{OwnerID: "bob", ProjectID: "p1", Repository: "alice/demo", Branch: "main", Commit: commit}, "project not found"},
		"unknown branch": {contractprojects.RequestImport{OwnerID: "alice", ProjectID: "p1", Repository: "alice/demo", Branch: "dev", Commit: commit}, "selected branch"},
		"unknown commit": {contractprojects.RequestImport{OwnerID: "alice", ProjectID: "p1", Repository: "alice/demo", Branch: "main", Commit: strings.Repeat("f", 40)}, "50 most recent commits"},
		"missing repo":   {contractprojects.RequestImport{OwnerID: "alice", ProjectID: "p1", Repository: "alice/none", Branch: "main", Commit: commit}, "status 404"},
	} {
		if err := h.call(contractprojects.SubjectRequestImport, c.request, nil); !errors.Is(err, contract.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want invalid containing %q", name, err, c.want)
		}
	}
	h.mu.Lock()
	h.connected["alice"] = false
	h.mu.Unlock()
	if err := h.call(contractprojects.SubjectRequestImport, contractprojects.RequestImport{OwnerID: "alice", ProjectID: "p1", Repository: "alice/demo", Branch: "main", Commit: commit}, nil); err == nil || err.Error() != "connect GitHub before importing a repository" {
		t.Fatalf("without a GitHub Connection = %v", err)
	}
}

func TestAProjectKeepsItsFiveNewestSnapshots(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	var commits []string
	for i := range 6 {
		commits = append(commits, h.github.Commit(t, "alice/demo", map[string]string{"main.go": mainGo, "n.txt": strings.Repeat("x", i+1)}))
	}
	var first string
	for i, commit := range commits {
		if _, outcome := h.importCommit("alice", "p1", "alice/demo", commit); outcome != "project.import.completed" {
			t.Fatalf("import %d ended with %s", i, outcome)
		}
		if i == 0 {
			first = h.getProject("alice", "p1").Project.SnapshotID
		}
	}
	var snapshots contractprojects.Snapshots
	h.must(contractprojects.SubjectListSnapshots, contractprojects.ProjectKey{OwnerID: "alice", ProjectID: "p1"}, &snapshots)
	if len(snapshots.Snapshots) != 5 || !snapshots.Snapshots[0].Active || snapshots.Snapshots[0].Commit != commits[5] {
		t.Fatalf("snapshots = %+v", snapshots.Snapshots)
	}
	var event contractprojects.SnapshotDeletedEvent
	if err := json.Unmarshal(h.event(contractprojects.SubjectSnapshotDeleted).Data, &event); err != nil || event.SnapshotID != first || event.ProjectID != "p1" || event.OwnerID != "alice" {
		t.Fatalf("snapshot_deleted = %+v, %v; want the first Snapshot", event, err)
	}

	// An older Snapshot can be made active again.
	var activated contractprojects.Activated
	h.must(contractprojects.SubjectActivateSnapshot, contractprojects.SnapshotKey{OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshots.Snapshots[4].ID}, &activated)
	if !activated.Activated || h.getProject("alice", "p1").Project.Commit != commits[1] {
		t.Fatalf("activating an older Snapshot = %+v", activated)
	}
	h.must(contractprojects.SubjectActivateSnapshot, contractprojects.SnapshotKey{OwnerID: "bob", ProjectID: "p1", SnapshotID: snapshots.Snapshots[0].ID}, &activated)
	if activated.Activated {
		t.Fatal("bob activated alice's Snapshot")
	}
}

func TestDeletingAProjectWaitsForItsImportAndTellsTheOtherServices(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	if _, err := h.svc.db.Exec(`INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status) VALUES('running','alice','p1','alice/demo','main',?, 'cloning')`, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if err := h.call(contractprojects.SubjectDeleteProject, contractprojects.ProjectKey{OwnerID: "alice", ProjectID: "p1"}, nil); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("deleting with an import in progress = %v, want a conflict", err)
	}
	var active contractprojects.ActiveImports
	h.must(contractprojects.SubjectActiveImports, contractprojects.Owner{OwnerID: "alice"}, &active)
	if len(active.Imports) != 1 || active.Imports[0].ID != "running" || active.Imports[0].Status != "cloning" {
		t.Fatalf("active imports = %+v", active)
	}
	if _, err := h.svc.db.Exec(`UPDATE project_import_jobs SET status='failed' WHERE id='running'`); err != nil {
		t.Fatal(err)
	}
	var deleted contractprojects.ProjectDeleted
	h.must(contractprojects.SubjectDeleteProject, contractprojects.ProjectKey{OwnerID: "bob", ProjectID: "p1"}, &deleted)
	if deleted.Deleted {
		t.Fatal("bob deleted alice's project")
	}
	h.must(contractprojects.SubjectDeleteProject, contractprojects.ProjectKey{OwnerID: "alice", ProjectID: "p1"}, &deleted)
	if !deleted.Deleted || h.getProject("alice", "p1").Found {
		t.Fatalf("deleted = %+v", deleted)
	}
	var event contractprojects.ProjectDeletedEvent
	if err := json.Unmarshal(h.event(contractprojects.SubjectProjectDeleted).Data, &event); err != nil || event.ProjectID != "p1" || event.OwnerID != "alice" {
		t.Fatalf("project_deleted = %+v, %v", event, err)
	}
}

func TestAnInterruptedImportIsResumedOnRedelivery(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	commit := h.github.Commit(t, "alice/demo", map[string]string{"main.go": mainGo})
	// A worker died while cloning: the job is not finished and its message is
	// handed out again.
	if _, err := h.svc.db.Exec(`INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status,progress) VALUES('interrupted','alice','p1','alice/demo','main',?,'cloning',15)`, commit); err != nil {
		t.Fatal(err)
	}
	if err := queueImportNow(h.svc, "interrupted"); err != nil {
		t.Fatal(err)
	}
	if outcome := h.waitImport("interrupted"); outcome != "project.import.completed" {
		t.Fatalf("resumed import ended with %s", outcome)
	}
	if h.getProject("alice", "p1").Project.Commit != commit {
		t.Fatal("the resumed import did not produce the Snapshot")
	}
}

func TestImportsSurviveARestartOfTheService(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	commit := h.github.Commit(t, "alice/demo", map[string]string{"main.go": mainGo})
	if _, err := h.svc.db.Exec(`INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status) VALUES('waiting','alice','p1','alice/demo','main',?,'queued')`, commit); err != nil {
		t.Fatal(err)
	}
	// The job message is still in the outbox when the service stops.
	if err := addOutboxOnly(h.svc, "waiting"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.startService()
	if outcome := h.waitImport("waiting"); outcome != "project.import.completed" {
		t.Fatalf("import queued before the restart ended with %s", outcome)
	}
}

func TestDeletingAnOwnerCancelsItsImportsAndDeletesItsProjects(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	h.project("alice", "p2")
	h.project("bob", "b1")
	if _, err := h.svc.db.Exec(`INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status) VALUES('pending','alice','p1','alice/demo','main',?,'scanning')`, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	h.must(contractprojects.SubjectDeleteOwner, contractprojects.DeleteOwner{OwnerID: "alice"}, nil)
	var list contractprojects.Projects
	h.must(contractprojects.SubjectListProjects, contractprojects.Owner{OwnerID: "alice"}, &list)
	if len(list.Projects) != 0 {
		t.Fatalf("alice's projects after deletion = %+v", list.Projects)
	}
	var found contractprojects.ImportFound
	h.must(contractprojects.SubjectFindImport, contractprojects.ImportKey{OwnerID: "alice", JobID: "pending"}, &found)
	if found.Found {
		t.Fatalf("the import survived: %+v", found)
	}
	if !h.getProject("bob", "b1").Found {
		t.Fatal("deleting alice removed bob's project")
	}
	seen := map[string]bool{}
	for range 2 {
		var event contractprojects.ProjectDeletedEvent
		if err := json.Unmarshal(h.event(contractprojects.SubjectProjectDeleted).Data, &event); err != nil {
			t.Fatal(err)
		}
		seen[event.ProjectID] = true
	}
	if !seen["p1"] || !seen["p2"] {
		t.Fatalf("project_deleted events for %v, want p1 and p2", seen)
	}
}

func TestALargeFileComesInPieces(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	large := strings.Repeat("0123456789", 30_000) // 300 KB
	commit := h.github.Commit(t, "alice/demo", map[string]string{"a.txt": "small", "b.bin": large, "main.go": mainGo})
	if _, outcome := h.importCommit("alice", "p1", "alice/demo", commit); outcome != "project.import.completed" {
		t.Fatalf("import ended with %s", outcome)
	}
	snapshot := h.getProject("alice", "p1").Project.SnapshotID
	got := map[string][]byte{}
	page := contractprojects.SnapshotFiles{OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, MaxBytes: 100_000}
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging does not end")
		}
		var files contractprojects.Files
		h.must(contractprojects.SubjectSnapshotFiles, page, &files)
		for _, f := range files.Files {
			if f.ContentOffset != len(got[f.Path]) {
				t.Fatalf("%s piece at %d, have %d bytes", f.Path, f.ContentOffset, len(got[f.Path]))
			}
			got[f.Path] = append(got[f.Path], f.Content...)
		}
		if !files.More {
			break
		}
		last := files.Files[len(files.Files)-1]
		if int64(last.ContentOffset+len(last.Content)) < last.Size {
			page.Offset += len(files.Files) - 1
			page.ContentOffset = last.ContentOffset + len(last.Content)
		} else {
			page.Offset += len(files.Files)
			page.ContentOffset = 0
		}
	}
	if string(got["b.bin"]) != large || string(got["a.txt"]) != "small" || string(got["main.go"]) != mainGo {
		t.Fatalf("reassembled sizes: a=%d b=%d main=%d", len(got["a.txt"]), len(got["b.bin"]), len(got["main.go"]))
	}
}

func TestGitHubListsGoThroughTheOwnersConnection(t *testing.T) {
	h := start(t)
	commit := h.github.Commit(t, "alice/demo", map[string]string{"main.go": mainGo})
	var repos contractprojects.Repositories
	h.must(contractprojects.SubjectRepositories, contractprojects.Owner{OwnerID: "alice"}, &repos)
	if !repos.Connected || len(repos.Repositories) != 1 || repos.Repositories[0].FullName != "alice/demo" {
		t.Fatalf("repositories = %+v", repos)
	}
	var branches contractprojects.Branches
	h.must(contractprojects.SubjectBranches, contractprojects.RepositoryKey{OwnerID: "alice", Repository: "alice/demo"}, &branches)
	if len(branches.Branches) != 1 || branches.Branches[0].Commit.SHA != commit {
		t.Fatalf("branches = %+v", branches)
	}
	var commits contractprojects.Commits
	h.must(contractprojects.SubjectCommits, contractprojects.RepositoryKey{OwnerID: "alice", Repository: "alice/demo", Branch: "main"}, &commits)
	if len(commits.Commits) != 1 || commits.Commits[0].SHA != commit {
		t.Fatalf("commits = %+v", commits)
	}
	h.mu.Lock()
	h.connected["carol"] = false
	h.mu.Unlock()
	h.must(contractprojects.SubjectRepositories, contractprojects.Owner{OwnerID: "carol"}, &repos)
	if repos.Connected || len(repos.Repositories) != 0 {
		t.Fatalf("a disconnected Account's repositories = %+v", repos)
	}
	if err := h.call(contractprojects.SubjectBranches, contractprojects.RepositoryKey{OwnerID: "carol", Repository: "alice/demo"}, nil); err == nil || !strings.Contains(err.Error(), "connect GitHub") {
		t.Fatalf("branches without a connection = %v", err)
	}
}

func TestAProjectHasOneImportAtATime(t *testing.T) {
	h := start(t)
	h.project("alice", "p1")
	commit := h.github.Commit(t, "alice/demo", map[string]string{"main.go": mainGo})
	if _, err := h.svc.db.Exec(`INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status) VALUES('running','alice','p1','alice/demo','main',?,'scanning')`, commit); err != nil {
		t.Fatal(err)
	}
	err := h.call(contractprojects.SubjectRequestImport, contractprojects.RequestImport{OwnerID: "alice", ProjectID: "p1", Repository: "alice/demo", Branch: "main", Commit: commit}, nil)
	if !errors.Is(err, contract.ErrInvalid) || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("a second import while one runs = %v", err)
	}
	if _, err := h.svc.db.Exec(`UPDATE project_import_jobs SET status='failed' WHERE id='running'`); err != nil {
		t.Fatal(err)
	}
	var retried contractprojects.ImportQueued
	h.must(contractprojects.SubjectRetryImport, contractprojects.ImportKey{OwnerID: "alice", JobID: "running"}, &retried)
	if outcome := h.waitImport(retried.JobID); outcome != "project.import.completed" {
		t.Fatalf("the retried import ended with %s", outcome)
	}
}
