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
	// Callers that record the same Build's state are serialized, not lost: each
	// applies it, and the Build is kept once.
	ctx := context.Background()
	ReadySnapshot(t, store, "shared", "shared-project", "shared-snapshot", "Go")
	results := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			results <- store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: "shared-build", OwnerID: "shared", ProjectID: "shared-project", SnapshotID: "shared-snapshot", Target: "t", Status: "queued"})
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent ApplyWASMBuild: %v", err)
		}
	}
	if _, found, err := store.FindWASMBuild(ctx, "shared-build", "shared"); err != nil || !found {
		t.Fatalf("shared build = found %v, %v", found, err)
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
	if !do("ApplyWASMBuild", store.ApplyWASMBuild(ctx, app.WASMBuildChange{ID: build, OwnerID: name, ProjectID: project, SnapshotID: snapshot, Target: "wasip1-wasm", Status: "complete"})) {
		return
	}
	if !do("ApplyCheck", store.ApplyCheck(ctx, app.CheckChange{BuildID: build, OwnerID: name, Status: app.CheckPassed, Log: "ok"})) {
		return
	}
	if got, found, e := store.FindWASMBuild(ctx, build, name); !do("FindWASMBuild", e) || !found || got.Status != "complete" {
		return fmt.Errorf("%s: FindWASMBuild = %+v, %v, %v", name, got, found, e)
	}
	if check, found, e := store.FindCheck(ctx, build, name); !do("FindCheck", e) || !found || check.Status != app.CheckPassed {
		return fmt.Errorf("%s: FindCheck = %+v, %v, %v", name, check, found, e)
	}

	if !do("RemoveSnapshot", store.RemoveSnapshot(ctx, snapshot)) {
		return
	}
	if !do("RemoveProject", store.RemoveProject(ctx, project)) {
		return
	}
	if deleted, e := store.DeleteAccount(ctx, name); !do("DeleteAccount", e) || !deleted.Deleted {
		return fmt.Errorf("%s: DeleteAccount = %+v, %v", name, deleted, e)
	}
	return err
}
