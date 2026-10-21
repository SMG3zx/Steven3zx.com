// Package storetest holds the in-memory app.Store and the contract suite that
// every app.Store adapter, real or test, must pass.
package storetest

import (
	"context"
	"janus/internal/app"
	"janus/internal/domain/usage"
	"strings"
	"testing"
	"time"
)

// Run runs the Store contract against adapters made by newStore. Each case
// gets a fresh, empty Store.
func Run(t *testing.T, newStore func(t *testing.T) app.Store) {
	t.Helper()
	for _, c := range []struct {
		name string
		test func(t *testing.T, store app.Store)
	}{
		{"OwnersAreAddedOnce", testOwners},
		{"ReferencesGoWithWhatRefersToThem", testReferences},
		{"BuildsFollowTheirStateMachine", testBuildsFollowTheirStateMachine},
		{"ChecksAndDeploymentsFollowTheirRules", testChecksAndDeployments},
		{"RedeployAndStopFollowTheirRules", testRedeployAndStop},
		{"DeletingAnAccountCascades", testDeletingAnAccountCascades},
		{"EnrollmentCodesAreSingleUseAndExpire", testEnrollmentCodes},
		{"AgentsAreOwned", testAgents},
		{"DeletingAnAccountRemovesItsAgentsAndCodes", testDeletingAnAccountRemovesAgents},
		{"ListAllAgentsSpansOwners", testListAllAgents},
		{"RunUsageIsOwnedAndKeptOnce", testRunUsage},
		{"RunUsageGoesWithTheAccount", testRunUsageGoesWithTheAccount},
		{"ConcurrentCallersNeverDeadlock", testConcurrentCallers},
	} {
		t.Run(c.name, func(t *testing.T) { c.test(t, newStore(t)) })
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// ReadySnapshot records what a WASM Build needs to refer to: Owner, Project
// and a ready Snapshot of language, with the runtime the scan would give it.
// It returns the Snapshot's ID. Adding any of them again changes nothing.
func ReadySnapshot(t testing.TB, store app.Store, owner, project, snapshot, language string) string {
	t.Helper()
	ctx := context.Background()
	runtimes := map[string]string{"Rust": "rust", "Go": "go", "JavaScript": "node", "TypeScript": "node"}
	for _, err := range []error{
		store.AddOwner(ctx, owner),
		store.AddProject(ctx, app.ProjectRef{ID: project, OwnerID: owner}),
		store.AddSnapshot(ctx, app.SnapshotRef{ID: snapshot, ProjectID: project, OwnerID: owner, Language: language, Runtime: runtimes[language]}),
	} {
		if err != nil {
			t.Fatalf("record snapshot %s of %s: %v", snapshot, project, err)
		}
	}
	return snapshot
}

func testOwners(t *testing.T, store app.Store) {
	ctx := context.Background()
	if err := store.AddProject(ctx, app.ProjectRef{ID: "early", OwnerID: "alice"}); err == nil {
		t.Fatal("a Project was recorded for an Owner that was never added")
	}
	must(t, store.AddOwner(ctx, "alice"))
	must(t, store.AddOwner(ctx, "alice")) // adding again changes nothing
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "p1", OwnerID: "alice"}))
	must(t, store.AddOwner(ctx, "alice"))
	if projects, err := store.ListOwnedProjects(ctx, "alice"); err != nil || len(projects) != 1 || projects[0] != "p1" {
		t.Fatalf("adding an Owner again lost its Project: %v, %v", projects, err)
	}
	if deleted, err := store.DeleteAccount(ctx, "nobody", false); err != nil || deleted.Deleted {
		t.Fatalf("deleting an Owner that was never added = %#v, %v, want nothing deleted", deleted, err)
	}
}

func testReferences(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.AddOwner(ctx, "bob"))
	ReadySnapshot(t, store, "alice", "p1", "s1", "Rust")
	ReadySnapshot(t, store, "alice", "p1", "s2", "Go")
	ReadySnapshot(t, store, "alice", "p1", "s1", "Go") // already recorded: kept as it was
	if err := store.AddSnapshot(ctx, app.SnapshotRef{ID: "orphan", ProjectID: "unknown", OwnerID: "alice"}); err == nil {
		t.Fatal("a Snapshot was recorded for a Project that was not")
	}
	got, found, err := store.FindSnapshot(ctx, "s1", "alice")
	if err != nil || !found || got != (app.SnapshotRef{ID: "s1", ProjectID: "p1", OwnerID: "alice", Language: "Rust", Runtime: "rust"}) {
		t.Fatalf("FindSnapshot = %#v, %v, %v", got, found, err)
	}
	if _, found, _ := store.FindSnapshot(ctx, "s1", "bob"); found {
		t.Fatal("bob found alice's Snapshot")
	}
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: "s1", Target: "t"}))
	must(t, store.TransitionWASMBuild(ctx, app.JobTransition{ID: "b1", From: "queued", To: "failed", Progress: 100}))
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b2", OwnerID: "alice", ProjectID: "p1", SnapshotID: "s2", Target: "t"}))

	must(t, store.RemoveSnapshot(ctx, "s1"))
	must(t, store.RemoveSnapshot(ctx, "s1")) // removing again changes nothing
	if _, found, _ := store.FindWASMBuild(ctx, "b1", "alice"); found {
		t.Fatal("a Build outlived its Snapshot")
	}
	if _, found, _ := store.FindWASMBuild(ctx, "b2", "alice"); !found {
		t.Fatal("removing one Snapshot removed another's Build")
	}
	must(t, store.RemoveProject(ctx, "p1"))
	if _, found, _ := store.FindSnapshot(ctx, "s2", "alice"); found {
		t.Fatal("a Snapshot outlived its Project")
	}
	if _, found, _ := store.FindWASMBuild(ctx, "b2", "alice"); found {
		t.Fatal("a Build outlived its Project")
	}
	if projects, _ := store.ListOwnedProjects(ctx, "alice"); len(projects) != 0 {
		t.Fatalf("alice's Projects after removal = %v", projects)
	}
}

func testBuildsFollowTheirStateMachine(t *testing.T, store app.Store) {
	ctx := context.Background()
	must(t, store.AddOwner(ctx, "alice"))
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "p1", OwnerID: "alice"}))
	snapshot := ReadySnapshot(t, store, "alice", "p1", "one-snapshot", "Rust")

	var err error
	if err = store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b0", OwnerID: "bob", ProjectID: "p1", SnapshotID: snapshot, Target: "t"}); err == nil || !strings.Contains(err.Error(), "snapshot not found") {
		t.Fatalf("another Account building alice's Snapshot = %v", err)
	}
	if err = store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b0", OwnerID: "alice", ProjectID: "p1", SnapshotID: "missing", Target: "t"}); err == nil {
		t.Fatal("building a missing Snapshot succeeded")
	}
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown"}))
	err = store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b2", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown"})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "constraint failed") {
		t.Fatalf("a second active build = %v, want a constraint failure", err)
	}

	queued, err := store.ListResumableBuilds(ctx)
	must(t, err)
	if len(queued) != 1 || queued[0].JobID != "b1" || queued[0].Language != "Rust" || queued[0].SnapshotID != snapshot || queued[0].Target != "wasm32-unknown-unknown" {
		t.Fatalf("resumable builds = %#v", queued)
	}
	if artifact, err := store.FindWASMBuildArtifact(ctx, "b1", "alice"); err != nil || artifact.Found {
		t.Fatalf("artifact of an unfinished build = %#v, %v", artifact, err)
	}
	if err = store.TransitionWASMBuild(ctx, app.JobTransition{ID: "b1", From: "queued", To: "complete"}); err == nil || !strings.Contains(err.Error(), "cannot move from queued to complete") {
		t.Fatalf("queued to complete = %v", err)
	}
	if err = store.TransitionWASMBuild(ctx, app.JobTransition{ID: "b1", From: "building", To: "failed"}); err == nil || !strings.Contains(err.Error(), "changed while it was being updated") {
		t.Fatalf("a move from a status the build is not in = %v", err)
	}
	must(t, store.TransitionWASMBuild(ctx, app.JobTransition{ID: "b1", From: "queued", To: "building", Progress: 40, Log: "compiling"}))
	must(t, store.TransitionWASMBuild(ctx, app.JobTransition{ID: "b1", From: "building", To: "building", Progress: 60, Log: "still compiling"}))
	must(t, store.CompleteWASMBuild(ctx, app.CompleteWASMBuild{ID: "b1", ArtifactName: "wasm-artifacts.zip", Artifact: []byte("zip"), Log: []byte("done")}))
	if err = store.CompleteWASMBuild(ctx, app.CompleteWASMBuild{ID: "b1", ArtifactName: "x", Artifact: []byte("y")}); err == nil || !strings.Contains(err.Error(), "WASM build not found") {
		t.Fatalf("completing a finished build = %v", err)
	}

	build, found, err := store.FindWASMBuild(ctx, "b1", "alice")
	must(t, err)
	if !found || build.Status != "complete" || build.Progress != 100 || build.ArtifactName != "wasm-artifacts.zip" || build.Log != "done" || build.SnapshotID != snapshot {
		t.Fatalf("finished build = %#v", build)
	}
	if _, found, _ = store.FindWASMBuild(ctx, "b1", "bob"); found {
		t.Fatal("another Account found the build")
	}
	artifact, err := store.FindWASMBuildArtifact(ctx, "b1", "alice")
	must(t, err)
	if !artifact.Found || artifact.Name != "wasm-artifacts.zip" || string(artifact.Data) != "zip" {
		t.Fatalf("artifact = %#v", artifact)
	}
	if other, _ := store.FindWASMBuildArtifact(ctx, "b1", "bob"); other.Found {
		t.Fatal("another Account downloaded the artifact")
	}
	// With the first build finished, a new one may start.
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b3", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "wasm32-unknown-unknown"}))
}

func testDeletingAnAccountCascades(t *testing.T, store app.Store) {
	ctx := context.Background()
	for _, id := range []string{"alice", "bob"} {
		must(t, store.AddOwner(ctx, id))
	}
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "p1", OwnerID: "alice"}))
	must(t, store.AddProject(ctx, app.ProjectRef{ID: "pb", OwnerID: "bob"}))
	snapshot := ReadySnapshot(t, store, "alice", "p1", "one-snapshot", "Rust")
	must(t, store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "b1", OwnerID: "alice", ProjectID: "p1", SnapshotID: snapshot, Target: "t"}))

	active, err := store.ListActiveJobs(ctx, "alice")
	must(t, err)
	if len(active) != 1 || active[0] != (app.ActiveJob{Kind: "wasm_build", ID: "b1", ProjectID: "p1", Status: "queued"}) {
		t.Fatalf("active jobs = %#v", active)
	}
	refused, err := store.DeleteAccount(ctx, "alice", false)
	must(t, err)
	if refused.Deleted || len(refused.ActiveJobs) != 1 {
		t.Fatalf("deleting with an unfinished build = %#v, want a refusal listing it", refused)
	}
	if projects, _ := store.ListOwnedProjects(ctx, "alice"); len(projects) != 1 {
		t.Fatal("a refused deletion removed the Account's project")
	}

	deleted, err := store.DeleteAccount(ctx, "alice", true)
	must(t, err)
	if !deleted.Deleted {
		t.Fatalf("deleting with cancel = %#v", deleted)
	}
	if projects, _ := store.ListOwnedProjects(ctx, "alice"); len(projects) != 0 {
		t.Fatal("the project survived its Account")
	}
	if _, found, _ := store.FindWASMBuild(ctx, "b1", "alice"); found {
		t.Fatal("the build survived its Account")
	}
	if again, err := store.DeleteAccount(ctx, "alice", false); err != nil || again.Deleted {
		t.Fatalf("deleting the Owner again = %#v, %v, want nothing left to delete", again, err)
	}
	if projects, _ := store.ListOwnedProjects(ctx, "bob"); len(projects) != 1 {
		t.Fatal("deleting alice removed bob's project")
	}
	if missing, err := store.DeleteAccount(ctx, "ghost", false); err != nil || missing.Deleted {
		t.Fatalf("deleting a missing Account = %#v, %v", missing, err)
	}
}

func testEnrollmentCodes(t *testing.T, store app.Store) {
	ctx := context.Background()
	for _, id := range []string{"alice", "bob"} {
		must(t, store.AddOwner(ctx, id))
	}
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	expires := base.Add(15 * time.Minute)

	must(t, store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "h1", OwnerID: "alice", ExpiresAt: expires}))
	if err := store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "h1", OwnerID: "bob", ExpiresAt: expires}); err == nil {
		t.Fatal("a second code with the same hash was stored")
	}

	if owner, consumed, err := store.ConsumeEnrollmentCode(ctx, "unknown", base); err != nil || consumed || owner != "" {
		t.Fatalf("consuming an unknown code = %q, %v, %v, want no owner and not consumed", owner, consumed, err)
	}
	owner, consumed, err := store.ConsumeEnrollmentCode(ctx, "h1", base.Add(time.Minute))
	must(t, err)
	if !consumed || owner != "alice" {
		t.Fatalf("consuming a valid code = %q, %v, want alice and consumed", owner, consumed)
	}
	if owner, consumed, err = store.ConsumeEnrollmentCode(ctx, "h1", base.Add(2*time.Minute)); err != nil || consumed || owner != "" {
		t.Fatalf("consuming a code twice = %q, %v, %v, want not consumed the second time", owner, consumed, err)
	}

	must(t, store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "h2", OwnerID: "alice", ExpiresAt: expires}))
	if _, consumed, err = store.ConsumeEnrollmentCode(ctx, "h2", expires); err != nil || consumed {
		t.Fatalf("consuming a code at the moment it expires = %v, %v, want not consumed", consumed, err)
	}
	if _, consumed, err = store.ConsumeEnrollmentCode(ctx, "h2", expires.Add(time.Hour)); err != nil || consumed {
		t.Fatalf("consuming an expired code = %v, %v, want not consumed", consumed, err)
	}
	must(t, store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "h3", OwnerID: "bob", ExpiresAt: expires}))
	if owner, consumed, err = store.ConsumeEnrollmentCode(ctx, "h3", expires.Add(-time.Nanosecond)); err != nil || !consumed || owner != "bob" {
		t.Fatalf("consuming a code just before it expires = %q, %v, %v, want bob and consumed", owner, consumed, err)
	}
}

func testAgents(t *testing.T, store app.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"alice", "bob"} {
		must(t, store.AddOwner(ctx, id))
	}
	laptop := app.CreateAgent{ID: "a1", OwnerID: "alice", Name: "laptop", Tag: "tag:acct-alice", KeyID: "k1", CreatedAt: base}
	must(t, store.CreateAgent(ctx, laptop))
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "a2", OwnerID: "alice", Name: "desktop", Tag: "tag:acct-alice", KeyID: "k2", CreatedAt: base.Add(time.Minute)}))
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "b1", OwnerID: "bob", Name: "server", Tag: "tag:acct-bob", KeyID: "k3", CreatedAt: base}))
	if err := store.CreateAgent(ctx, app.CreateAgent{ID: "a3", OwnerID: "alice", Name: "again", Tag: "tag:acct-alice", KeyID: "k1", CreatedAt: base}); err == nil {
		t.Fatal("two Agents were stored for the same key")
	}

	agents, err := store.ListAgents(ctx, "alice")
	must(t, err)
	if len(agents) != 2 || agents[0].ID != "a1" || agents[1].ID != "a2" {
		t.Fatalf("alice's Agents = %#v, want a1 then a2 by creation time", agents)
	}
	if got := agents[0]; got.OwnerID != "alice" || got.Name != "laptop" || got.Tag != "tag:acct-alice" || got.KeyID != "k1" || !got.CreatedAt.Equal(base) {
		t.Fatalf("stored Agent = %#v, want the fields it was created with", got)
	}
	if none, err := store.ListAgents(ctx, "nobody"); err != nil || len(none) != 0 {
		t.Fatalf("an unknown owner's Agents = %#v, %v, want none", none, err)
	}

	if _, found, err := store.FindAgent(ctx, "a1", "bob"); err != nil || found {
		t.Fatalf("bob found alice's Agent: %v, %v", found, err)
	}
	if got, found, err := store.FindAgent(ctx, "a1", "alice"); err != nil || !found || got.KeyID != "k1" {
		t.Fatalf("finding alice's Agent = %#v, %v, %v", got, found, err)
	}
	if deleted, err := store.DeleteAgent(ctx, "a1", "bob"); err != nil || deleted {
		t.Fatalf("bob deleted alice's Agent: %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteAgent(ctx, "a1", "alice"); err != nil || !deleted {
		t.Fatalf("alice deleting her Agent = %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteAgent(ctx, "a1", "alice"); err != nil || deleted {
		t.Fatalf("deleting an Agent twice = %v, %v, want false the second time", deleted, err)
	}
	if remaining, _ := store.ListAgents(ctx, "alice"); len(remaining) != 1 || remaining[0].ID != "a2" {
		t.Fatalf("alice's Agents after a delete = %#v", remaining)
	}
}

func testDeletingAnAccountRemovesAgents(t *testing.T, store app.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"alice", "bob"} {
		must(t, store.AddOwner(ctx, id))
	}
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "a1", OwnerID: "alice", Name: "laptop", Tag: "tag:acct-alice", KeyID: "k1", CreatedAt: base}))
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "b1", OwnerID: "bob", Name: "server", Tag: "tag:acct-bob", KeyID: "k2", CreatedAt: base}))
	must(t, store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "ha", OwnerID: "alice", ExpiresAt: base.Add(time.Hour)}))
	must(t, store.CreateEnrollmentCode(ctx, app.CreateEnrollmentCode{CodeHash: "hb", OwnerID: "bob", ExpiresAt: base.Add(time.Hour)}))

	deleted, err := store.DeleteAccount(ctx, "alice", false)
	must(t, err)
	if !deleted.Deleted {
		t.Fatalf("deleting the Account = %#v", deleted)
	}
	if agents, _ := store.ListAgents(ctx, "alice"); len(agents) != 0 {
		t.Fatalf("an Agent survived its Account: %#v", agents)
	}
	if _, consumed, _ := store.ConsumeEnrollmentCode(ctx, "ha", base); consumed {
		t.Fatal("an Enrollment Code survived its Account")
	}
	if agents, _ := store.ListAgents(ctx, "bob"); len(agents) != 1 {
		t.Fatalf("deleting alice changed bob's Agents: %#v", agents)
	}
	if owner, consumed, _ := store.ConsumeEnrollmentCode(ctx, "hb", base); !consumed || owner != "bob" {
		t.Fatal("deleting alice removed bob's Enrollment Code")
	}
}

func testListAllAgents(t *testing.T, store app.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"alice", "bob"} {
		must(t, store.AddOwner(ctx, id))
	}
	if all, err := store.ListAllAgents(ctx); err != nil || len(all) != 0 {
		t.Fatalf("Agents of an empty Store = %#v, %v", all, err)
	}
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "b1", OwnerID: "bob", Name: "server", Tag: "tag:acct-bob", KeyID: "k1", CreatedAt: base.Add(time.Minute)}))
	must(t, store.CreateAgent(ctx, app.CreateAgent{ID: "a1", OwnerID: "alice", Name: "laptop", Tag: "tag:acct-alice", KeyID: "k2", CreatedAt: base}))
	all, err := store.ListAllAgents(ctx)
	must(t, err)
	if len(all) != 2 || all[0].ID != "a1" || all[1].ID != "b1" || all[1].OwnerID != "bob" || all[0].KeyID != "k2" {
		t.Fatalf("all Agents = %#v, want a1 then b1 with their fields", all)
	}
}

func runUsage(runID, owner, agent, deployment string, started time.Time) app.RunUsageRecord {
	return app.RunUsageRecord{OwnerID: owner, AgentID: agent, RunReport: app.RunReport{
		RunID: runID, DeploymentID: deployment, Status: "ok", StartedAt: started,
		Usage:          usage.Usage{Fuel: 1234, PeakMemoryBytes: 65536, DurationMS: 7, CPUTimeMicros: 900},
		NetworkBytesIn: 11, NetworkBytesOut: 22,
	}}
}

func testRunUsage(t *testing.T, store app.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"alice", "bob"} {
		must(t, store.AddOwner(ctx, id))
	}
	first := runUsage("r1", "alice", "a1", "d1", base)
	first.Error, first.Probe, first.Status = "boom", true, "failed"
	must(t, store.RecordRunUsage(ctx, first))
	must(t, store.RecordRunUsage(ctx, runUsage("r2", "alice", "a1", "d2", base.Add(time.Second))))
	must(t, store.RecordRunUsage(ctx, runUsage("r3", "alice", "a2", "d1", base.Add(2*time.Second))))
	must(t, store.RecordRunUsage(ctx, runUsage("r4", "bob", "b1", "d9", base)))
	// A report that arrives twice, after a reconnect, is kept once.
	again := runUsage("r1", "alice", "a1", "d1", base.Add(time.Hour))
	must(t, store.RecordRunUsage(ctx, again))

	ids := func(records []app.RunUsageRecord) string {
		var out []string
		for _, record := range records {
			out = append(out, record.RunID)
		}
		return strings.Join(out, ",")
	}
	all, err := store.ListRunUsage(ctx, app.RunUsageQuery{OwnerID: "alice"})
	must(t, err)
	if ids(all) != "r3,r2,r1" {
		t.Fatalf("alice's runs = %s, want r3,r2,r1 newest first", ids(all))
	}
	got := all[2]
	if got.OwnerID != "alice" || got.AgentID != "a1" || got.DeploymentID != "d1" || got.Status != "failed" || got.Error != "boom" || !got.Probe ||
		!got.StartedAt.Equal(base) || got.Usage != first.Usage || got.NetworkBytesIn != 11 || got.NetworkBytesOut != 22 {
		t.Fatalf("stored run = %#v, want %#v", got, first)
	}
	for query, want := range map[app.RunUsageQuery]string{
		{OwnerID: "alice", AgentID: "a1"}:                     "r2,r1",
		{OwnerID: "alice", DeploymentID: "d1"}:                "r3,r1",
		{OwnerID: "alice", AgentID: "a1", DeploymentID: "d1"}: "r1",
		{OwnerID: "alice", Limit: 1}:                          "r3",
		{OwnerID: "bob", DeploymentID: "d1"}:                  "",
		{OwnerID: "bob"}:                                      "r4",
	} {
		records, err := store.ListRunUsage(ctx, query)
		must(t, err)
		if ids(records) != want {
			t.Errorf("runs for %+v = %s, want %s", query, ids(records), want)
		}
	}
}

func testRunUsageGoesWithTheAccount(t *testing.T, store app.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"alice", "bob"} {
		must(t, store.AddOwner(ctx, id))
	}
	must(t, store.RecordRunUsage(ctx, runUsage("r1", "alice", "a1", "d1", base)))
	must(t, store.RecordRunUsage(ctx, runUsage("r2", "bob", "b1", "d2", base)))
	_, err := store.DeleteAccount(ctx, "alice", false)
	must(t, err)
	if records, _ := store.ListRunUsage(ctx, app.RunUsageQuery{OwnerID: "alice"}); len(records) != 0 {
		t.Fatalf("runs survived their Account: %#v", records)
	}
	if records, _ := store.ListRunUsage(ctx, app.RunUsageQuery{OwnerID: "bob"}); len(records) != 1 {
		t.Fatalf("deleting alice changed bob's runs: %#v", records)
	}
}
