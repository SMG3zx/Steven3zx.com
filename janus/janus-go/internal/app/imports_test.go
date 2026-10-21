package app_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"janus/internal/adapters/audittest"
	"janus/internal/adapters/hosttest"
	"janus/internal/adapters/identitytest"
	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

const importMainGo = "package main\n\nfunc main() {}\n"

// importFixture serves a one-file repository, alice/repo, from a local
// directory and a fake host, with an in-memory Store holding alice and a Project.
type importFixture struct {
	t          *testing.T
	store      app.Store
	identities *identitytest.Memory
	audit      *audittest.Recorder
	source     *countingSource
	imports    *app.Imports
	commit     string
	blob       string
	dir        string
}

// countingSource counts how many times the repository was opened, and can be
// told to block or panic.
type countingSource struct {
	inner   app.Source
	opened  atomic.Int32
	started chan struct{}
	release chan struct{}
	panics  bool
}

func (s *countingSource) Open(ctx context.Context, request app.SourceRequest) (app.SourceTree, error) {
	s.opened.Add(1)
	if s.panics {
		panic("source exploded")
	}
	if s.started != nil {
		s.started <- struct{}{}
		<-s.release
	}
	return s.inner.Open(ctx, request)
}

func newImportFixture(t *testing.T) *importFixture {
	t.Helper()
	commit := strings.Repeat("a", 40)
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(importMainGo), 0o600))
	branch := app.Branch{Name: "main"}
	branch.Commit.SHA = commit
	host := hosttest.NewHost(hosttest.World{Token: "token", Repositories: []hosttest.Repo{{
		Repository: app.Repository{ID: 42, FullName: "alice/repo", DefaultBranch: "main", Language: "Go", Stars: 3, Forks: 1},
		Branches:   []app.Branch{branch},
		Commits:    map[string][]app.Commit{"main": {{SHA: commit, Message: "fixture"}}},
	}}})
	store := storetest.NewMemory()
	ctx := context.Background()
	must(t, store.AddOwner(ctx, "alice"))
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "project-1", OwnerID: "alice", Name: "Project"})
	must(t, err)
	identities := users(t, "alice")
	must(t, identities.ConnectGitHub(ctx, app.ConnectGitHub{UserID: "alice", Subject: "github-alice", Token: "token"}))
	source := &countingSource{inner: hosttest.NewDirSource(dir, commit)}
	audit := audittest.NewRecorder()
	return &importFixture{t: t, store: store, identities: identities, audit: audit, source: source, imports: app.NewImports(store, identities, host, source, audit), commit: commit, blob: hosttest.BlobHash([]byte(importMainGo)), dir: dir}
}

func (f *importFixture) wait() {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := f.imports.Wait(ctx); err != nil {
		f.t.Fatal(err)
	}
}

func (f *importFixture) job(id string) app.ImportJob {
	f.t.Helper()
	job, found, err := f.store.FindImportJob(context.Background(), id, "alice")
	must(f.t, err)
	if !found {
		f.t.Fatalf("import %s not found", id)
	}
	return job
}

func (f *importFixture) snapshots() []app.ProjectSnapshot {
	f.t.Helper()
	snapshots, err := f.store.ListSnapshots(context.Background(), "alice", "project-1")
	must(f.t, err)
	return snapshots
}

func (f *importFixture) request() app.ImportRequest {
	return app.ImportRequest{OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: f.commit}
}

// manifestFor returns the scan manifest of the fixture repository, with main.go
// recorded under hash.
func (f *importFixture) manifestFor(hash string) string {
	return fmt.Sprintf(`[{"path":"main.go","mode":"-rw-r--r--","hash":%q,"size":%d}]`, hash, len(importMainGo))
}

// interruptedImport leaves an import job queued, with a Staging Snapshot that
// already holds stored files, as a restart would find it. An empty manifest
// stands for a Staging Snapshot that was interrupted before its scan finished.
func (f *importFixture) interruptedImport(job, staging string, stored map[string]string, manifest string) {
	f.t.Helper()
	ctx := context.Background()
	must(f.t, f.store.CreateImportJob(ctx, app.CreateImportJob{ID: job, OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: f.commit}))
	storetest.AdvanceImport(f.t, f.store, job, "queued", "cloning", "scanning", "storing")
	must(f.t, f.store.BeginSnapshot(ctx, app.BeginSnapshot{ID: staging, JobID: job, OwnerID: "alice", ProjectID: "project-1", RepositoryID: 42, Repository: "alice/repo", Branch: "main", Commit: f.commit, DefaultBranch: "main", MetadataJSON: "{}", Manifest: manifest}))
	var files []app.SnapshotFile
	for path, hash := range stored {
		files = append(files, app.SnapshotFile{Path: path, Mode: "100644", Content: []byte(importMainGo), Hash: hash})
	}
	if len(files) > 0 {
		must(f.t, f.store.AddSnapshotFiles(ctx, staging, files))
	}
	must(f.t, f.store.TransitionImportJob(ctx, app.JobTransition{ID: job, From: "storing", To: "queued"}))
}

func (f *importFixture) resume() {
	f.t.Helper()
	if _, err := f.imports.Resume(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	f.wait()
}

func TestRequestImportsTheCommitAsTheActiveSnapshot(t *testing.T) {
	f := newImportFixture(t)
	id, err := f.imports.Request(context.Background(), f.request())
	must(t, err)
	f.wait()

	if job := f.job(id); job.Status != "complete" || job.Progress != 100 {
		t.Fatalf("job = %+v", job)
	}
	snapshots := f.snapshots()
	if len(snapshots) != 1 || !snapshots[0].Active || snapshots[0].Language != "Go" || snapshots[0].FileCount != 1 || snapshots[0].Commit != f.commit {
		t.Fatalf("snapshots = %+v", snapshots)
	}
	project, _, err := f.store.GetProject(context.Background(), "project-1", "alice")
	must(t, err)
	if !strings.Contains(project.MetadataJSON, `"full_name":"alice/repo"`) || project.Stars != 3 || project.DefaultBranch != "main" {
		t.Fatalf("project = %+v", project)
	}
	events := f.audit.Events()
	if len(events) != 2 || events[0].Name != "project.import.queued" || events[1].Name != "project.import.completed" {
		t.Fatalf("audit events = %+v", events)
	}
}

func TestRequestRefusesWhatCannotBeImported(t *testing.T) {
	f := newImportFixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		change func(*app.ImportRequest)
		want   string
	}{
		{"short commit", func(r *app.ImportRequest) { r.Commit = "abc" }, "full commit SHA are required"},
		{"no branch", func(r *app.ImportRequest) { r.Branch = "" }, "full commit SHA are required"},
		{"another owner", func(r *app.ImportRequest) { r.OwnerID = "mallory" }, "project not found"},
		{"unknown branch", func(r *app.ImportRequest) { r.Branch = "dev" }, "selected branch does not belong to this repository"},
		{"commit not in the newest fifty", func(r *app.ImportRequest) { r.Commit = strings.Repeat("c", 40) }, "not among the branch's 50 most recent commits"},
		{"unknown repository", func(r *app.ImportRequest) { r.Repository = "alice/other" }, "repository not found"},
	} {
		request := f.request()
		c.change(&request)
		if _, err := f.imports.Request(ctx, request); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want %q", c.name, err, c.want)
		}
	}
	must(t, f.identities.DisconnectGitHub(ctx, "alice"))
	if _, err := f.imports.Request(ctx, f.request()); err == nil || err.Error() != "connect GitHub before importing a repository" {
		t.Errorf("without a connection: %v", err)
	}
	if f.source.opened.Load() != 0 {
		t.Error("the repository was fetched for a refused request")
	}
}

func TestResumedImportReusesItsStagingSnapshot(t *testing.T) {
	f := newImportFixture(t)
	f.interruptedImport("job-1", "staging-1", map[string]string{"main.go": f.blob}, f.manifestFor(f.blob))
	f.resume()

	if job := f.job("job-1"); job.Status != "complete" {
		t.Fatalf("resumed import status = %q (%s), want complete", job.Status, job.Error)
	}
	snapshots := f.snapshots()
	if len(snapshots) != 1 || snapshots[0].ID != "staging-1" || !snapshots[0].Active || snapshots[0].FileCount != 1 {
		t.Fatalf("resumed import should complete the reused staging snapshot, got %#v", snapshots)
	}
}

func TestResumeDiscardsStagingThatDoesNotMatchTheCommit(t *testing.T) {
	f := newImportFixture(t)
	f.interruptedImport("job-1", "staging-1", map[string]string{"main.go": "not-the-blob-hash"}, f.manifestFor(f.blob))
	f.resume()

	if job := f.job("job-1"); job.Status != "complete" {
		t.Fatalf("status = %q (%s), want complete", job.Status, job.Error)
	}
	snapshots := f.snapshots()
	if len(snapshots) != 1 || snapshots[0].ID == "staging-1" {
		t.Fatalf("corrupt staging snapshot was reused: %#v", snapshots)
	}
	if snapshots[0].Language != "Go" {
		t.Fatalf("snapshot language = %q, want the scan's dominant language Go", snapshots[0].Language)
	}
}

func TestResumeWithoutGitHubConnectionFailsAndDiscardsStaging(t *testing.T) {
	f := newImportFixture(t)
	f.interruptedImport("job-1", "staging-1", map[string]string{"main.go": f.blob}, f.manifestFor(f.blob))
	must(t, f.identities.DisconnectGitHub(context.Background(), "alice"))
	f.resume()

	if job := f.job("job-1"); job.Status != "failed" || !strings.Contains(job.Error, "reconnect GitHub") {
		t.Fatalf("status=%q error=%q, want failure asking to reconnect GitHub", job.Status, job.Error)
	}
	if staging, err := f.store.FindStagingSnapshot(context.Background(), "job-1"); err != nil || staging.Found {
		t.Fatal("failed resume kept its staging snapshot")
	}
}

func TestResumeWithEveryFileStoredDoesNotFetchTheRepository(t *testing.T) {
	f := newImportFixture(t)
	f.interruptedImport("job-1", "staging-1", map[string]string{"main.go": f.blob}, f.manifestFor(f.blob))
	f.resume()

	if job := f.job("job-1"); job.Status != "complete" {
		t.Fatalf("status = %q (%s), want complete without the clone", job.Status, job.Error)
	}
	if f.source.opened.Load() != 0 {
		t.Fatalf("the repository was fetched %d times", f.source.opened.Load())
	}
	if snapshots := f.snapshots(); len(snapshots) != 1 || snapshots[0].ID != "staging-1" || snapshots[0].FileCount != 1 {
		t.Fatalf("resumed import should complete the staging snapshot from its manifest, got %#v", snapshots)
	}
}

func TestResumeStoresTheFilesNotYetStoredFromTheClone(t *testing.T) {
	f := newImportFixture(t)
	f.interruptedImport("job-1", "staging-1", nil, f.manifestFor(f.blob))
	f.resume()

	if job := f.job("job-1"); job.Status != "complete" {
		t.Fatalf("status = %q (%s), want complete", job.Status, job.Error)
	}
	if snapshots := f.snapshots(); len(snapshots) != 1 || snapshots[0].ID != "staging-1" || snapshots[0].FileCount != 1 {
		t.Fatalf("resumed import should store the missing file into the staging snapshot, got %#v", snapshots)
	}
}

func TestResumeWithoutManifestRestartsFromTheClone(t *testing.T) {
	f := newImportFixture(t)
	f.interruptedImport("job-1", "staging-1", map[string]string{"main.go": f.blob}, "")
	f.resume()

	if job := f.job("job-1"); job.Status != "complete" {
		t.Fatalf("status = %q (%s), want complete", job.Status, job.Error)
	}
	if snapshots := f.snapshots(); len(snapshots) != 1 || snapshots[0].ID == "staging-1" {
		t.Fatalf("staging snapshot without a manifest was reused: %#v", snapshots)
	}
}

func TestResumeRestartsFromTheCloneWhenTheManifestDoesNotMatchTheRepository(t *testing.T) {
	f := newImportFixture(t)
	f.interruptedImport("job-1", "staging-1", nil, f.manifestFor("not-the-blob-hash"))
	f.resume()

	if job := f.job("job-1"); job.Status != "complete" {
		t.Fatalf("status = %q (%s), want complete", job.Status, job.Error)
	}
	if snapshots := f.snapshots(); len(snapshots) != 1 || snapshots[0].ID == "staging-1" {
		t.Fatalf("staging snapshot with a mismatched manifest was reused: %#v", snapshots)
	}
}

func TestRetryStartsANewImportFromAFailedOne(t *testing.T) {
	f := newImportFixture(t)
	ctx := context.Background()
	must(t, f.store.CreateImportJob(ctx, app.CreateImportJob{ID: "job-failed", OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: f.commit}))
	must(t, f.store.TransitionImportJob(ctx, app.JobTransition{ID: "job-failed", From: "queued", To: "failed", Progress: 100, Error: "clone failed"}))

	id, err := f.imports.Retry(ctx, "alice", "job-failed")
	if err != nil || id == "" || id == "job-failed" {
		t.Fatalf("retry = %q, %v; want a new job", id, err)
	}
	f.wait()
	if job := f.job(id); job.Status != "complete" {
		t.Fatalf("retried import status = %q (%s)", job.Status, job.Error)
	}
}

func TestRetryRejectsAnImportThatHasNotFailedOrDoesNotExist(t *testing.T) {
	f := newImportFixture(t)
	ctx := context.Background()
	must(t, f.store.CreateImportJob(ctx, app.CreateImportJob{ID: "job-running", OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: f.commit}))
	if _, err := f.imports.Retry(ctx, "alice", "job-running"); err == nil || !strings.Contains(err.Error(), "only a failed import") {
		t.Fatalf("retry of a queued job = %v, want rejection", err)
	}
	if _, err := f.imports.Retry(ctx, "alice", "nope"); err == nil || err.Error() != "import job not found" {
		t.Fatalf("retry of an unknown job = %v", err)
	}
}

func TestAnImportThatCannotOpenTheRepositoryFailsWithItsPublicMessage(t *testing.T) {
	f := newImportFixture(t)
	request := f.request()
	f.source.inner = hosttest.NewDirSource(f.dir, strings.Repeat("9", 40)) // serves another commit
	id, err := f.imports.Request(context.Background(), request)
	must(t, err)
	f.wait()
	if job := f.job(id); job.Status != "failed" || job.Error != "selected commit was not found in the fetched history" {
		t.Fatalf("job = %+v", job)
	}
}

func TestAPanickingSourceFailsTheImportAndKeepsTheProcess(t *testing.T) {
	f := newImportFixture(t)
	f.source.panics = true
	id, err := f.imports.Request(context.Background(), f.request())
	must(t, err)
	f.wait()
	job := f.job(id)
	if job.Status != "failed" || !strings.Contains(job.Error, "source exploded") {
		t.Fatalf("job = %+v, want a failed import that keeps the panic", job)
	}
	f.source.panics = false
	if _, err = f.imports.Request(context.Background(), f.request()); err != nil {
		t.Fatalf("the slot was not freed after a panic: %v", err)
	}
	f.wait()
	var failures int
	for _, event := range f.audit.Events() {
		if event.Name == "project.import.failed" {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("failed audit events = %d, want 1", failures)
	}
}

func TestOnlyOneImportRunsPerProject(t *testing.T) {
	f := newImportFixture(t)
	f.source.started, f.source.release = make(chan struct{}, 2), make(chan struct{})
	ctx := context.Background()
	if _, err := f.imports.Request(ctx, f.request()); err != nil {
		t.Fatal(err)
	}
	<-f.source.started
	if _, err := f.imports.Request(ctx, f.request()); err == nil || !strings.Contains(err.Error(), "constraint failed") {
		t.Fatalf("a second import of the same project: %v", err)
	}
	close(f.source.release)
	f.wait()
}

func TestDrainRefusesNewImportsAndWaitsForRunningOnes(t *testing.T) {
	f := newImportFixture(t)
	f.source.started, f.source.release = make(chan struct{}, 1), make(chan struct{})
	id, err := f.imports.Request(context.Background(), f.request())
	must(t, err)
	<-f.source.started

	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err = f.imports.Drain(short); err == nil {
		t.Fatal("Drain returned with an import running")
	}
	if _, err = f.imports.Request(context.Background(), f.request()); err == nil || err.Error() != "project imports are shutting down" {
		t.Fatalf("request while draining: %v", err)
	}
	close(f.source.release)
	if err = f.imports.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if job := f.job(id); job.Status != "complete" {
		t.Fatalf("import after Drain = %+v", job)
	}
}

func TestCancelOwnerStopsARunningImport(t *testing.T) {
	f := newImportFixture(t)
	f.source.started, f.source.release = make(chan struct{}, 1), make(chan struct{})
	_, err := f.imports.Request(context.Background(), f.request())
	must(t, err)
	<-f.source.started
	if got := f.imports.CancelOwner("alice"); got != 1 {
		t.Fatalf("CancelOwner = %d, want 1", got)
	}
	close(f.source.release)
	f.wait()
	if got := f.imports.CancelOwner("alice"); got != 0 {
		t.Fatalf("CancelOwner after the import ended = %d", got)
	}
}

func TestSetEntryPointAcceptsOnlyADetectedCandidateOfTheLatestSnapshot(t *testing.T) {
	f := newImportFixture(t)
	ctx := context.Background()
	const metadata = `{"scan":{"entrypoint_candidates":[{"path":"cmd/server/main.go","ecosystem":"Go","reason":"Go service entrypoint"}]}}`
	must(t, f.store.CreateImportJob(ctx, app.CreateImportJob{ID: "job-1", OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: f.commit}))
	must(t, f.store.BeginSnapshot(ctx, app.BeginSnapshot{ID: "snapshot-1", JobID: "job-1", OwnerID: "alice", ProjectID: "project-1", RepositoryID: 42, Repository: "alice/repo", Branch: "main", Commit: f.commit, MetadataJSON: metadata}))
	storetest.AdvanceImport(t, f.store, "job-1", "queued", "cloning", "scanning", "storing")
	must(t, f.store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: "snapshot-1", JobID: "job-1"}))

	if err := f.imports.SetEntryPoint(ctx, "alice", "project-1", "snapshot-1", "cmd/server/main.go"); err != nil {
		t.Fatalf("confirming a candidate: %v", err)
	}
	if snapshots := f.snapshots(); len(snapshots) != 1 || snapshots[0].EntryPoint != "cmd/server/main.go" {
		t.Fatalf("snapshots = %+v", snapshots)
	}
	if err := f.imports.SetEntryPoint(ctx, "alice", "project-1", "snapshot-1", "not/a/candidate.go"); err == nil || err.Error() != "entrypoint must match a detected candidate" {
		t.Fatalf("a non-candidate: %v", err)
	}
	if err := f.imports.SetEntryPoint(ctx, "alice", "project-1", "other-snapshot", "cmd/server/main.go"); err == nil || err.Error() != "entrypoint selection must target the latest snapshot" {
		t.Fatalf("an old snapshot: %v", err)
	}
	if err := f.imports.SetEntryPoint(ctx, "mallory", "project-1", "snapshot-1", "cmd/server/main.go"); err == nil || err.Error() != "project not found" {
		t.Fatalf("another owner: %v", err)
	}
}
