package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"janus/internal/app"
)

// testConcurrentCallers calls every Store method from many goroutines at once.
// An adapter that holds its single connection while it waits for another call
// to finish would deadlock here, so the test fails if the callers do not all
// finish in time. Each caller works on records of its own, so the outcome of
// every call is known.
func testConcurrentCallers(t *testing.T, store app.Store) {
	const callers = 24
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() { errs <- exerciseEveryMethod(store, fmt.Sprintf("u%02d", i)) })
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(60 * time.Second):
		t.Fatal("concurrent callers did not finish: the Store is deadlocked")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	// Callers that share one record are serialized, not lost: exactly one wins.
	ctx := context.Background()
	ReadySnapshot(t, store, "shared", "shared-project", "shared-snapshot", "Go")
	results := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			results <- store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: "shared-build", OwnerID: "shared", ProjectID: "shared-project", SnapshotID: "shared-snapshot", Target: "t"})
		})
	}
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		if err == nil {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d callers created the same build, want exactly 1", won)
	}
}

// exerciseEveryMethod runs one of each Store method for the Account name.
func exerciseEveryMethod(store app.Store, name string) (err error) {
	ctx := context.Background()
	step := ""
	check := func(e error) bool {
		if e != nil && err == nil {
			err = fmt.Errorf("%s: %s: %w", name, step, e)
		}
		return e == nil
	}
	do := func(label string, e error) bool { step = label; return check(e) }

	if !do("AddOwner", store.AddOwner(ctx, name)) {
		return
	}

	project, snapshot := name+"-project", name+"-snapshot"
	if !do("AddProject", store.AddProject(ctx, app.ProjectRef{ID: project, OwnerID: name})) {
		return
	}
	if !do("AddSnapshot", store.AddSnapshot(ctx, app.SnapshotRef{ID: snapshot, ProjectID: project, OwnerID: name, Language: "Go", Runtime: "go"})) {
		return
	}
	if got, found, e := store.FindSnapshot(ctx, snapshot, name); !do("FindSnapshot", e) || !found || got.Language != "Go" {
		return fmt.Errorf("%s: FindSnapshot = %+v, %v, %v", name, got, found, e)
	}
	if projects, e := store.ListOwnedProjects(ctx, name); !do("ListOwnedProjects", e) || len(projects) != 1 {
		return fmt.Errorf("%s: ListOwnedProjects = %v, %v", name, projects, e)
	}

	build := name + "-build"
	if !do("CreateWASMBuild", store.CreateWASMBuild(ctx, app.CreateWASMBuild{ID: build, OwnerID: name, ProjectID: project, SnapshotID: snapshot, Target: "wasip1-wasm"})) {
		return
	}
	if _, e := store.ListResumableBuilds(ctx); !do("ListResumableBuilds", e) {
		return
	}
	if !do("TransitionWASMBuild", store.TransitionWASMBuild(ctx, app.JobTransition{ID: build, From: "queued", To: "building", Progress: 5})) {
		return
	}
	if !do("CompleteWASMBuild", store.CompleteWASMBuild(ctx, app.CompleteWASMBuild{ID: build, ArtifactName: "a.zip", Artifact: []byte("zip"), Log: []byte("ok")})) {
		return
	}
	if active, e := store.ListActiveJobs(ctx, name); !do("ListActiveJobs", e) || len(active) != 0 {
		return fmt.Errorf("%s: ListActiveJobs = %v, %v", name, active, e)
	}
	if got, found, e := store.FindWASMBuild(ctx, build, name); !do("FindWASMBuild", e) || !found || got.Status != "complete" {
		return fmt.Errorf("%s: FindWASMBuild = %+v, %v, %v", name, got, found, e)
	}
	if artifact, e := store.FindWASMBuildArtifact(ctx, build, name); !do("FindWASMBuildArtifact", e) || !artifact.Found {
		return fmt.Errorf("%s: FindWASMBuildArtifact = %+v, %v", name, artifact, e)
	}

	if !do("RemoveSnapshot", store.RemoveSnapshot(ctx, snapshot)) {
		return
	}
	if !do("RemoveProject", store.RemoveProject(ctx, project)) {
		return
	}
	if deleted, e := store.DeleteAccount(ctx, name, false); !do("DeleteAccount", e) || !deleted.Deleted {
		return fmt.Errorf("%s: DeleteAccount = %+v, %v", name, deleted, e)
	}
	return err
}
