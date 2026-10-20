package projects

import (
	"context"
	"encoding/json"
	"fmt"
	"janus/internal/adapters/storetest"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	goakt "github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"

	"janus/actors/github"
	"janus/actors/persistence"
)

const resumeMainGo = "package main\n\nfunc main() {}\n"

// resumeFixture is a local git repository served as alice/repo, plus the
// in-memory Store and projects actor that run against it.
type resumeFixture struct {
	t        *testing.T
	ctx      context.Context
	store    persistence.Store
	projects *goakt.PID
	commit   string
	blob     string
	repoDir  string
}

func newResumeFixture(t *testing.T) *resumeFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	repoDir := filepath.Join(root, "alice", "repo.git")
	commit, blob := writeFixtureRepository(t, repoDir)

	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/alice/repo":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "full_name": "alice/repo", "default_branch": "main", "language": "Go", "stargazers_count": 3, "forks_count": 1})
		case "/repos/alice/repo/branches":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "main"}})
		case "/repos/alice/repo/commits":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"sha": commit, "commit": map[string]any{"message": "fixture"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(githubServer.Close)

	system, err := goakt.NewActorSystem("projects-resume-test", goakt.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = system.Stop(stop)
	})
	store := storetest.NewMemory()
	gh := github.NewService(store)
	gh.API = &github.Client{HTTP: githubServer.Client(), BaseURL: githubServer.URL}
	actor := New(store, gh)
	actor.cloneBase = filepath.ToSlash(root) + "/"
	projectsPID, err := system.Spawn(ctx, "projects", actor)
	if err != nil {
		t.Fatal(err)
	}

	f := &resumeFixture{t: t, ctx: ctx, store: store, projects: projectsPID, commit: commit, blob: blob, repoDir: repoDir}
	f.must(store.CreateLocalUser(ctx, persistence.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "hash"}))
	_, err = store.CreateProject(ctx, persistence.CreateProject{ID: "project-1", OwnerID: "alice", Name: "Project"})
	f.must(err)
	f.must(store.ConnectGitHub(ctx, persistence.ConnectGitHub{UserID: "alice", Subject: "github-alice", Token: "token"}))
	return f
}

func (f *resumeFixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

// manifestFor returns the scan manifest of the fixture repository, with main.go
// recorded under hash.
func (f *resumeFixture) manifestFor(hash string) string {
	return fmt.Sprintf(`[{"path":"main.go","mode":"-rw-r--r--","hash":%q,"size":%d}]`, hash, len(resumeMainGo))
}

// interruptedImport leaves an import job queued, with a Staging Snapshot that
// already holds stored files, as a restart would find it. An empty manifest
// stands for a Staging Snapshot that was interrupted before its scan finished.
func (f *resumeFixture) interruptedImport(job, staging string, stored map[string]string, manifest string) {
	f.t.Helper()
	f.must(f.store.CreateImportJob(f.ctx, persistence.CreateImportJob{ID: job, OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: f.commit}))
	for _, status := range []string{"cloning", "scanning", "storing"} {
		f.must(f.store.UpdateImportJob(f.ctx, persistence.UpdateImportJob{ID: job, Status: status, Progress: 50}))
	}
	f.must(f.store.BeginSnapshot(f.ctx, persistence.BeginSnapshot{ID: staging, JobID: job, OwnerID: "alice", ProjectID: "project-1", RepositoryID: 42, Repository: "alice/repo", Branch: "main", Commit: f.commit, DefaultBranch: "main", MetadataJSON: "{}", Manifest: manifest}))
	var files []persistence.SnapshotFile
	for path, hash := range stored {
		files = append(files, persistence.SnapshotFile{Path: path, Mode: "100644", Content: []byte(resumeMainGo), Hash: hash})
	}
	if len(files) > 0 {
		f.must(f.store.AddSnapshotFiles(f.ctx, staging, files))
	}
	f.must(f.store.UpdateImportJob(f.ctx, persistence.UpdateImportJob{ID: job, Status: "queued", Progress: 0}))
}

func (f *resumeFixture) waitForImport(job string) persistence.ImportJob {
	f.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		found, _, err := f.store.FindImportJob(f.ctx, job, "alice")
		f.must(err)
		if found.Status == "complete" || found.Status == "failed" {
			return found
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.t.Fatalf("import %s did not finish", job)
	return persistence.ImportJob{}
}

func (f *resumeFixture) snapshots() []persistence.ProjectSnapshot {
	f.t.Helper()
	snapshots, err := f.store.ListSnapshots(f.ctx, "alice", "project-1")
	f.must(err)
	return snapshots
}

func (f *resumeFixture) resume() {
	f.t.Helper()
	response, err := goakt.Ask(f.ctx, f.projects, &ResumeImports{}, 30*time.Second)
	if err != nil {
		f.t.Fatal(err)
	}
	if result := response.(*ImportsResumed); result.Err != nil {
		f.t.Fatal(result.Err)
	}
}

func writeFixtureRepository(t *testing.T, dir string) (string, string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "main.go"), []byte(resumeMainGo), 0600); err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worktree.Add("main.go"); err != nil {
		t.Fatal(err)
	}
	hash, err := worktree.Commit("fixture", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	return hash.String(), plumbing.ComputeHash(plumbing.BlobObject, []byte(resumeMainGo)).String()
}

func TestResumedImportReusesItsStagingSnapshot(t *testing.T) {
	f := newResumeFixture(t)
	f.interruptedImport("job-1", "staging-1", map[string]string{"main.go": f.blob}, f.manifestFor(f.blob))

	f.resume()
	job := f.waitForImport("job-1")

	if job.Status != "complete" {
		t.Fatalf("resumed import status = %q (%s), want complete", job.Status, job.Error)
	}
	snapshots := f.snapshots()
	if len(snapshots) != 1 || snapshots[0].ID != "staging-1" || !snapshots[0].Active {
		t.Fatalf("resumed import should complete the reused staging snapshot, got %#v", snapshots)
	}
	if snapshots[0].FileCount != 1 {
		t.Fatalf("file count = %d, want 1", snapshots[0].FileCount)
	}
}

func TestResumeDiscardsStagingThatDoesNotMatchTheCommit(t *testing.T) {
	f := newResumeFixture(t)
	f.interruptedImport("job-1", "staging-1", map[string]string{"main.go": "not-the-blob-hash"}, f.manifestFor(f.blob))

	f.resume()
	job := f.waitForImport("job-1")

	if job.Status != "complete" {
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
	f := newResumeFixture(t)
	f.interruptedImport("job-1", "staging-1", map[string]string{"main.go": f.blob}, f.manifestFor(f.blob))
	f.must(f.store.DisconnectGitHub(f.ctx, "alice"))

	f.resume()
	job := f.waitForImport("job-1")

	if job.Status != "failed" || !strings.Contains(job.Error, "reconnect GitHub") {
		t.Fatalf("status=%q error=%q, want failure asking to reconnect GitHub", job.Status, job.Error)
	}
	if staging, err := f.store.FindStagingSnapshot(f.ctx, "job-1"); err != nil || staging.Found {
		t.Fatal("failed resume kept its staging snapshot")
	}
}

func TestResumeWithEveryFileStoredDoesNotCloneTheRepository(t *testing.T) {
	f := newResumeFixture(t)
	f.interruptedImport("job-1", "staging-1", map[string]string{"main.go": f.blob}, f.manifestFor(f.blob))
	if err := os.RemoveAll(f.repoDir); err != nil {
		t.Fatal(err)
	}

	f.resume()
	job := f.waitForImport("job-1")

	if job.Status != "complete" {
		t.Fatalf("status = %q (%s), want complete without the clone", job.Status, job.Error)
	}
	snapshots := f.snapshots()
	if len(snapshots) != 1 || snapshots[0].ID != "staging-1" || snapshots[0].FileCount != 1 {
		t.Fatalf("resumed import should complete the staging snapshot from its manifest, got %#v", snapshots)
	}
}

func TestResumeStoresTheFilesNotYetStoredFromTheClone(t *testing.T) {
	f := newResumeFixture(t)
	f.interruptedImport("job-1", "staging-1", nil, f.manifestFor(f.blob))

	f.resume()
	job := f.waitForImport("job-1")

	if job.Status != "complete" {
		t.Fatalf("status = %q (%s), want complete", job.Status, job.Error)
	}
	snapshots := f.snapshots()
	if len(snapshots) != 1 || snapshots[0].ID != "staging-1" || snapshots[0].FileCount != 1 {
		t.Fatalf("resumed import should store the missing file into the staging snapshot, got %#v", snapshots)
	}
}

func TestResumeWithoutManifestRestartsFromTheClone(t *testing.T) {
	f := newResumeFixture(t)
	f.interruptedImport("job-1", "staging-1", map[string]string{"main.go": f.blob}, "")

	f.resume()
	job := f.waitForImport("job-1")

	if job.Status != "complete" {
		t.Fatalf("status = %q (%s), want complete", job.Status, job.Error)
	}
	snapshots := f.snapshots()
	if len(snapshots) != 1 || snapshots[0].ID == "staging-1" {
		t.Fatalf("staging snapshot without a manifest was reused: %#v", snapshots)
	}
}

func TestResumeRestartsFromTheCloneWhenTheManifestDoesNotMatchTheRepository(t *testing.T) {
	f := newResumeFixture(t)
	f.interruptedImport("job-1", "staging-1", nil, f.manifestFor("not-the-blob-hash"))

	f.resume()
	job := f.waitForImport("job-1")

	if job.Status != "complete" {
		t.Fatalf("status = %q (%s), want complete", job.Status, job.Error)
	}
	snapshots := f.snapshots()
	if len(snapshots) != 1 || snapshots[0].ID == "staging-1" {
		t.Fatalf("staging snapshot with a mismatched manifest was reused: %#v", snapshots)
	}
}

func TestRetryStartsANewImportFromAFailedOne(t *testing.T) {
	f := newResumeFixture(t)
	f.must(f.store.CreateImportJob(f.ctx, persistence.CreateImportJob{ID: "job-failed", OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: f.commit}))
	f.must(f.store.UpdateImportJob(f.ctx, persistence.UpdateImportJob{ID: "job-failed", Status: "failed", Progress: 100, Error: "clone failed"}))

	response, err := goakt.Ask(f.ctx, f.projects, &RetryImport{OwnerID: "alice", JobID: "job-failed"}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	requested := response.(*ImportRequested)
	if requested.Err != nil || requested.JobID == "" || requested.JobID == "job-failed" {
		t.Fatalf("retry = %#v, want a new job", requested)
	}
	if job := f.waitForImport(requested.JobID); job.Status != "complete" {
		t.Fatalf("retried import status = %q (%s)", job.Status, job.Error)
	}
}

func TestRetryRejectsAnImportThatHasNotFailed(t *testing.T) {
	f := newResumeFixture(t)
	f.must(f.store.CreateImportJob(f.ctx, persistence.CreateImportJob{ID: "job-running", OwnerID: "alice", ProjectID: "project-1", Repository: "alice/repo", Branch: "main", Commit: f.commit}))

	response, err := goakt.Ask(f.ctx, f.projects, &RetryImport{OwnerID: "alice", JobID: "job-running"}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if requested := response.(*ImportRequested); requested.Err == nil || !strings.Contains(requested.Err.Error(), "only a failed import") {
		t.Fatalf("retry of a queued job = %#v, want rejection", requested)
	}
}

func TestDominantLanguagePicksTheMostFrequentLanguage(t *testing.T) {
	cases := []struct {
		name      string
		languages map[string]int
		want      string
	}{
		{"empty", map[string]int{}, ""},
		{"single", map[string]int{"Go": 2}, "Go"},
		{"most files wins", map[string]int{"Go": 2, "Rust": 5}, "Rust"},
		{"tie goes to the first name", map[string]int{"Rust": 3, "Go": 3}, "Go"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dominantLanguage(c.languages); got != c.want {
				t.Fatalf("dominantLanguage(%v) = %q, want %q", c.languages, got, c.want)
			}
		})
	}
}

func TestStoredFilesMatchRequiresEveryStoredFileToMatchTheCommit(t *testing.T) {
	manifest := []manifestEntry{{Path: "a.go", Hash: "h-a"}, {Path: "b.go", Hash: "h-b"}}
	if !storedFilesMatch(map[string]string{}, manifest) {
		t.Fatal("empty staging should match")
	}
	if !storedFilesMatch(map[string]string{"a.go": "h-a"}, manifest) {
		t.Fatal("matching stored file should match")
	}
	if storedFilesMatch(map[string]string{"a.go": "other"}, manifest) {
		t.Fatal("hash mismatch should not match")
	}
	if storedFilesMatch(map[string]string{"gone.go": "h-a"}, manifest) {
		t.Fatal("file not in the commit should not match")
	}
}
