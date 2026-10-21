package catalogtest

import (
	"context"
	"strings"
	"testing"

	"janus/internal/app"
)

// Fixture is one adapter under test and the GitHub it imports from.
type Fixture struct {
	Catalog app.Catalog
	// Connect gives an Owner a GitHub Connection.
	Connect func(owner string)
	// Publish makes files the newest commit of a repository's main branch
	// and returns the commit's SHA.
	Publish func(t *testing.T, repository string, files map[string]string) string
	// Wait returns once the Import with the job ID has finished.
	Wait func(t *testing.T, jobID string)
}

// Run runs the Catalog contract against adapters made by newFixture. Each case
// gets a fresh, empty adapter.
func Run(t *testing.T, newFixture func(t *testing.T) Fixture) {
	t.Helper()
	for _, c := range []struct {
		name string
		test func(t *testing.T, f Fixture)
	}{
		{"ProjectsAreOwned", testProjectsAreOwned},
		{"AnImportBecomesTheActiveSnapshot", testImport},
		{"ImportsThatCannotRunAreRefused", testRefusedImports},
		{"TheNewestFiveSnapshotsAreKept", testRetention},
		{"DeletingAnOwnerDeletesItsProjects", testDeleteOwner},
		{"GitHubListsNeedAConnection", testGitHubLists},
	} {
		t.Run(c.name, func(t *testing.T) { c.test(t, newFixture(t)) })
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

const mainGo = "package main\n\nfunc main() {}\n"

// imported imports files into a new Project of alice and returns the Project.
func imported(t *testing.T, f Fixture, project string, files map[string]string) app.Project {
	t.Helper()
	ctx := context.Background()
	f.Connect("alice")
	_, err := f.Catalog.CreateProject(ctx, app.CreateProject{ID: project, OwnerID: "alice", Name: project})
	must(t, err)
	commit := f.Publish(t, "alice/"+project, files)
	jobID, err := f.Catalog.RequestImport(ctx, app.ImportRequest{OwnerID: "alice", ProjectID: project, Repository: "alice/" + project, Branch: "main", Commit: commit})
	must(t, err)
	f.Wait(t, jobID)
	job, found, err := f.Catalog.FindImportJob(ctx, jobID, "alice")
	if err != nil || !found || job.Status != "complete" {
		t.Fatalf("import = %+v, %v, %v; want it complete", job, found, err)
	}
	got, _, err := f.Catalog.GetProject(ctx, project, "alice")
	must(t, err)
	return got
}

func testProjectsAreOwned(t *testing.T, f Fixture) {
	ctx := context.Background()
	created, err := f.Catalog.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "  First  ", Description: " one "})
	must(t, err)
	if created.Name != "First" || created.Description != "one" || created.OwnerID != "alice" || created.CreatedAt == "" || created.MetadataJSON != "{}" || created.SnapshotCount != 0 {
		t.Fatalf("created project = %#v", created)
	}
	if _, err := f.Catalog.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "Again"}); !app.IsReply(err) {
		t.Fatalf("a duplicate ID = %v, want a refusal", err)
	}
	if _, err := f.Catalog.CreateProject(ctx, app.CreateProject{ID: "p2", OwnerID: "alice", Name: " "}); !app.IsReply(err) {
		t.Fatalf("a blank name = %v, want a refusal", err)
	}
	if _, found, err := f.Catalog.GetProject(ctx, "p1", "bob"); err != nil || found {
		t.Fatalf("bob read alice's project: %v, %v", found, err)
	}
	if _, err := f.Catalog.UpdateProject(ctx, app.UpdateProject{ID: "p1", OwnerID: "bob", Name: "Mine"}); !app.IsReply(err) || err.Error() != "project not found" {
		t.Fatalf("bob updating alice's project = %v", err)
	}
	if _, err := f.Catalog.ListSnapshots(ctx, "bob", "p1"); !app.IsReply(err) {
		t.Fatalf("bob listing alice's snapshots = %v", err)
	}
	if deleted, err := f.Catalog.DeleteProject(ctx, "p1", "bob"); err != nil || deleted {
		t.Fatalf("bob deleting alice's project = %v, %v", deleted, err)
	}
	updated, err := f.Catalog.UpdateProject(ctx, app.UpdateProject{ID: "p1", OwnerID: "alice", Name: "Renamed"})
	must(t, err)
	if updated.Name != "Renamed" || updated.Description != "" {
		t.Fatalf("updated = %#v", updated)
	}
	if listed, err := f.Catalog.ListProjects(ctx, "alice"); err != nil || len(listed) != 1 || listed[0].ID != "p1" {
		t.Fatalf("alice's projects = %#v, %v", listed, err)
	}
	if listed, err := f.Catalog.ListProjects(ctx, "bob"); err != nil || len(listed) != 0 {
		t.Fatalf("bob's projects = %#v, %v", listed, err)
	}
	if deleted, err := f.Catalog.DeleteProject(ctx, "p1", "alice"); err != nil || !deleted {
		t.Fatalf("deleting = %v, %v", deleted, err)
	}
	if _, found, _ := f.Catalog.GetProject(ctx, "p1", "alice"); found {
		t.Fatal("the deleted project is still there")
	}
}

func testImport(t *testing.T, f Fixture) {
	ctx := context.Background()
	project := imported(t, f, "demo", map[string]string{"go.mod": "module demo\n", "cmd/server/main.go": mainGo})
	if project.SnapshotID == "" || project.Language != "Go" || project.FileCount != 2 || project.Repository != "alice/demo" || project.SnapshotCount != 1 {
		t.Fatalf("project after import = %#v", project)
	}
	if !strings.Contains(project.MetadataJSON, "cmd/server/main.go") {
		t.Fatalf("metadata does not list the entrypoint candidate: %s", project.MetadataJSON)
	}
	snapshots, err := f.Catalog.ListSnapshots(ctx, "alice", "demo")
	if err != nil || len(snapshots) != 1 || !snapshots[0].Active || snapshots[0].ID != project.SnapshotID {
		t.Fatalf("snapshots = %#v, %v", snapshots, err)
	}

	first, err := f.Catalog.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "alice", ProjectID: "demo", SnapshotID: project.SnapshotID, Limit: 1})
	must(t, err)
	rest, err := f.Catalog.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "alice", ProjectID: "demo", SnapshotID: project.SnapshotID, Offset: 1, Limit: 10})
	must(t, err)
	if len(first) != 1 || first[0].Path != "cmd/server/main.go" || string(first[0].Content) != mainGo || len(rest) != 1 || rest[0].Path != "go.mod" {
		t.Fatalf("file pages = %#v then %#v", first, rest)
	}
	if _, err := f.Catalog.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "bob", ProjectID: "demo", SnapshotID: project.SnapshotID}); !app.IsReply(err) {
		t.Fatalf("bob reading alice's files = %v", err)
	}

	if err := f.Catalog.SetEntryPoint(ctx, "alice", "demo", project.SnapshotID, "go.mod"); !app.IsReply(err) {
		t.Fatalf("an entrypoint that is not a candidate = %v", err)
	}
	must(t, f.Catalog.SetEntryPoint(ctx, "alice", "demo", project.SnapshotID, "cmd/server/main.go"))
	if got, _, _ := f.Catalog.GetProject(ctx, "demo", "alice"); got.EntryPoint != "cmd/server/main.go" {
		t.Fatalf("entrypoint = %q", got.EntryPoint)
	}
	if active, err := f.Catalog.ActiveImports(ctx, "alice"); err != nil || len(active) != 0 {
		t.Fatalf("active imports after the import = %#v, %v", active, err)
	}
}

func testRefusedImports(t *testing.T, f Fixture) {
	ctx := context.Background()
	_, err := f.Catalog.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "P"})
	must(t, err)
	commit := f.Publish(t, "alice/demo", map[string]string{"main.go": mainGo})
	request := app.ImportRequest{OwnerID: "alice", ProjectID: "p1", Repository: "alice/demo", Branch: "main", Commit: commit}
	if _, err := f.Catalog.RequestImport(ctx, request); !app.IsReply(err) || err.Error() != "connect GitHub before importing a repository" {
		t.Fatalf("without a connection = %v", err)
	}
	f.Connect("alice")
	f.Connect("bob")
	for name, c := range map[string]struct {
		change func(*app.ImportRequest)
		want   string
	}{
		"short commit":   {func(r *app.ImportRequest) { r.Commit = "abc" }, "full commit SHA"},
		"no project":     {func(r *app.ImportRequest) { r.ProjectID = "none" }, "project not found"},
		"another's":      {func(r *app.ImportRequest) { r.OwnerID = "bob" }, "project not found"},
		"unknown branch": {func(r *app.ImportRequest) { r.Branch = "dev" }, "selected branch"},
		"unknown commit": {func(r *app.ImportRequest) { r.Commit = strings.Repeat("f", 40) }, "50 most recent commits"},
	} {
		changed := request
		c.change(&changed)
		if _, err := f.Catalog.RequestImport(ctx, changed); !app.IsReply(err) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want a refusal containing %q", name, err, c.want)
		}
	}
	if _, err := f.Catalog.RetryImport(ctx, "alice", "no-such-job"); !app.IsReply(err) || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("retrying an unknown import = %v", err)
	}
}

func testRetention(t *testing.T, f Fixture) {
	ctx := context.Background()
	f.Connect("alice")
	_, err := f.Catalog.CreateProject(ctx, app.CreateProject{ID: "p1", OwnerID: "alice", Name: "P"})
	must(t, err)
	var commits []string
	for i := range 6 {
		commits = append(commits, f.Publish(t, "alice/demo", map[string]string{"main.go": mainGo, "n.txt": strings.Repeat("x", i+1)}))
	}
	for _, commit := range commits {
		jobID, err := f.Catalog.RequestImport(ctx, app.ImportRequest{OwnerID: "alice", ProjectID: "p1", Repository: "alice/demo", Branch: "main", Commit: commit})
		must(t, err)
		f.Wait(t, jobID)
	}
	snapshots, err := f.Catalog.ListSnapshots(ctx, "alice", "p1")
	if err != nil || len(snapshots) != 5 || !snapshots[0].Active || snapshots[0].Commit != commits[5] || snapshots[4].Commit != commits[1] {
		t.Fatalf("snapshots = %#v, %v; want the newest five, newest first", snapshots, err)
	}
	activated, err := f.Catalog.ActivateSnapshot(ctx, "alice", "p1", snapshots[4].ID)
	if err != nil || !activated {
		t.Fatalf("activating an older Snapshot = %v, %v", activated, err)
	}
	if got, _, _ := f.Catalog.GetProject(ctx, "p1", "alice"); got.Commit != commits[1] || got.SnapshotCount != 5 {
		t.Fatalf("project after activation = %#v", got)
	}
	if activated, err := f.Catalog.ActivateSnapshot(ctx, "bob", "p1", snapshots[0].ID); err != nil || activated {
		t.Fatalf("bob activating alice's Snapshot = %v, %v", activated, err)
	}
}

func testDeleteOwner(t *testing.T, f Fixture) {
	ctx := context.Background()
	imported(t, f, "kept", map[string]string{"main.go": mainGo})
	for _, p := range []app.CreateProject{{ID: "b1", OwnerID: "bob", Name: "B"}} {
		_, err := f.Catalog.CreateProject(ctx, p)
		must(t, err)
	}
	must(t, f.Catalog.DeleteOwner(ctx, "alice"))
	if listed, err := f.Catalog.ListProjects(ctx, "alice"); err != nil || len(listed) != 0 {
		t.Fatalf("alice's projects after deletion = %#v, %v", listed, err)
	}
	if _, found, _ := f.Catalog.GetProject(ctx, "b1", "bob"); !found {
		t.Fatal("deleting alice removed bob's project")
	}
	must(t, f.Catalog.DeleteOwner(ctx, "alice")) // again: nothing left, no error
}

func testGitHubLists(t *testing.T, f Fixture) {
	ctx := context.Background()
	commit := f.Publish(t, "alice/demo", map[string]string{"main.go": mainGo})
	if list, err := f.Catalog.Repositories(ctx, "alice"); err != nil || list.Connected || len(list.Repositories) != 0 {
		t.Fatalf("repositories without a connection = %#v, %v", list, err)
	}
	if _, err := f.Catalog.Branches(ctx, "alice", "alice/demo"); err == nil || !strings.Contains(err.Error(), "connect GitHub") {
		t.Fatalf("branches without a connection = %v", err)
	}
	f.Connect("alice")
	list, err := f.Catalog.Repositories(ctx, "alice")
	if err != nil || !list.Connected || len(list.Repositories) != 1 || list.Repositories[0].FullName != "alice/demo" {
		t.Fatalf("repositories = %#v, %v", list, err)
	}
	branches, err := f.Catalog.Branches(ctx, "alice", "alice/demo")
	if err != nil || len(branches) != 1 || branches[0].Name != "main" || branches[0].Commit.SHA != commit {
		t.Fatalf("branches = %#v, %v", branches, err)
	}
	commits, err := f.Catalog.Commits(ctx, "alice", "alice/demo", "main")
	if err != nil || len(commits) != 1 || commits[0].SHA != commit {
		t.Fatalf("commits = %#v, %v", commits, err)
	}
}
