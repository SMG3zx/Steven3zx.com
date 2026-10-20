package hosttest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"janus/internal/app"
)

// RunHost runs the GitHost contract against adapters made by newHost. The
// factory returns a host that serves world and accepts only world.Token. Each
// case gets a fresh GitHost.
func RunHost(t *testing.T, newHost func(t *testing.T, world World) app.GitHost) {
	t.Helper()
	world := testWorld()
	for _, c := range []struct {
		name string
		test func(t *testing.T, host app.GitHost, world World)
	}{
		{"ListsRepositories", testListsRepositories},
		{"ReturnsOneRepository", testReturnsOneRepository},
		{"ListsBranches", testListsBranches},
		{"ListsTheNewestCommitsFirst", testListsCommits},
		{"ListsAtMostFiftyCommits", testCapsCommits},
		{"RefusesAnUnknownRepositoryOrBranch", testRefusesUnknown},
		{"RefusesANameThatIsNotOwnerSlashName", testRefusesInvalidName},
		{"RefusesAnotherToken", testRefusesToken},
	} {
		t.Run(c.name, func(t *testing.T) { c.test(t, newHost(t, world), world) })
	}
}

const (
	repoName = "alice/private"
	bigName  = "alice/busy"
)

func testWorld() World {
	date := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	branch := func(name, sha string) app.Branch {
		b := app.Branch{Name: name}
		b.Commit.SHA = sha
		return b
	}
	busy := make([]app.Commit, 60)
	for i := range busy {
		busy[i] = app.Commit{SHA: fmt.Sprintf("%040d", i), Message: fmt.Sprintf("commit %d", i), Date: date.Add(-time.Duration(i) * time.Hour)}
	}
	return World{Token: "test-token", Repositories: []Repo{
		{
			Repository: app.Repository{ID: 7, FullName: repoName, Private: true, DefaultBranch: "main", Language: "Rust", Stars: 3, Forks: 1, Size: 20},
			Branches:   []app.Branch{branch("main", "abc"), branch("dev", "def")},
			Commits: map[string][]app.Commit{
				"main": {{SHA: "deadbeef", Message: "second", Date: date}, {SHA: "cafe", Message: "initial commit", Date: date.Add(-time.Hour)}},
				"dev":  {{SHA: "f00d", Message: "wip", Date: date}},
			},
		},
		{
			Repository: app.Repository{ID: 8, FullName: bigName, DefaultBranch: "main", Language: "Go"},
			Branches:   []app.Branch{branch("main", busy[0].SHA)},
			Commits:    map[string][]app.Commit{"main": busy},
		},
	}}
}

func testListsRepositories(t *testing.T, host app.GitHost, world World) {
	got, err := host.Repositories(context.Background(), world.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("repositories = %+v", got)
	}
	for i, repo := range world.Repositories {
		if got[i].ID != repo.Repository.ID || got[i].FullName != repo.Repository.FullName || got[i].Private != repo.Repository.Private {
			t.Errorf("repository %d = %+v, want %+v", i, got[i], repo.Repository)
		}
	}
}

func testReturnsOneRepository(t *testing.T, host app.GitHost, world World) {
	got, err := host.Repository(context.Background(), world.Token, repoName)
	if err != nil {
		t.Fatal(err)
	}
	want := world.Repositories[0].Repository
	if got.ID != want.ID || got.FullName != want.FullName || got.DefaultBranch != "main" || got.Language != "Rust" || got.Stars != 3 || got.Forks != 1 {
		t.Fatalf("repository = %+v, want %+v", got, want)
	}
}

func testListsBranches(t *testing.T, host app.GitHost, world World) {
	got, err := host.Branches(context.Background(), world.Token, repoName)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "main" || got[0].Commit.SHA != "abc" || got[1].Name != "dev" {
		t.Fatalf("branches = %+v", got)
	}
}

func testListsCommits(t *testing.T, host app.GitHost, world World) {
	got, err := host.Commits(context.Background(), world.Token, repoName, "main")
	if err != nil {
		t.Fatal(err)
	}
	want := world.Repositories[0].Commits["main"]
	if len(got) != len(want) {
		t.Fatalf("commits = %+v", got)
	}
	for i := range want {
		if got[i].SHA != want[i].SHA || got[i].Message != want[i].Message || !got[i].Date.Equal(want[i].Date) {
			t.Errorf("commit %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func testCapsCommits(t *testing.T, host app.GitHost, world World) {
	got, err := host.Commits(context.Background(), world.Token, bigName, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 || got[0].SHA != world.Repositories[1].Commits["main"][0].SHA {
		t.Fatalf("got %d commits, first %q; want the newest 50", len(got), got[0].SHA)
	}
}

func testRefusesUnknown(t *testing.T, host app.GitHost, world World) {
	ctx := context.Background()
	if _, err := host.Repository(ctx, world.Token, "alice/missing"); err == nil {
		t.Error("Repository found a repository that does not exist")
	}
	if _, err := host.Branches(ctx, world.Token, "alice/missing"); err == nil {
		t.Error("Branches found a repository that does not exist")
	}
	if _, err := host.Commits(ctx, world.Token, "alice/missing", "main"); err == nil {
		t.Error("Commits found a repository that does not exist")
	}
	if _, err := host.Commits(ctx, world.Token, repoName, "no-such-branch"); err == nil {
		t.Error("Commits found a branch that does not exist")
	}
}

func testRefusesInvalidName(t *testing.T, host app.GitHost, world World) {
	ctx := context.Background()
	for _, name := range []string{"", "alice", "/repo", "alice/", "a/b/c"} {
		if _, err := host.Repository(ctx, world.Token, name); err == nil {
			t.Errorf("Repository accepted %q", name)
		}
		if _, err := host.Branches(ctx, world.Token, name); err == nil {
			t.Errorf("Branches accepted %q", name)
		}
		if _, err := host.Commits(ctx, world.Token, name, "main"); err == nil {
			t.Errorf("Commits accepted %q", name)
		}
	}
}

func testRefusesToken(t *testing.T, host app.GitHost, _ World) {
	ctx := context.Background()
	if _, err := host.Repositories(ctx, "another-token"); err == nil {
		t.Error("Repositories accepted another token")
	}
	if _, err := host.Repository(ctx, "another-token", repoName); err == nil {
		t.Error("Repository accepted another token")
	}
}
