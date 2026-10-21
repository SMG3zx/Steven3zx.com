package storetest

import (
	"context"
	"fmt"
	"strings"
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
	must(t, store.AddOwner(ctx, "shared"))
	results := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			_, err := store.CreateProject(ctx, app.CreateProject{ID: "shared-project", OwnerID: "shared", Name: "one"})
			results <- err
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
		t.Fatalf("%d callers created the same project, want exactly 1", won)
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
	if _, e := store.CreateProject(ctx, app.CreateProject{ID: project, OwnerID: name, Name: "p"}); !do("CreateProject", e) {
		return
	}
	if _, e := store.UpdateProject(ctx, app.UpdateProject{ID: project, OwnerID: name, Name: "renamed"}); !do("UpdateProject", e) {
		return
	}
	commit := strings.Repeat("a", 40)
	if !do("CreateImportJob", store.CreateImportJob(ctx, app.CreateImportJob{ID: name + "-job", OwnerID: name, ProjectID: project, Repository: name + "/r", Branch: "main", Commit: commit})) {
		return
	}
	if !do("TransitionImportJob", store.TransitionImportJob(ctx, app.JobTransition{ID: name + "-job", From: "queued", To: "cloning", Progress: 10})) {
		return
	}
	if !do("BeginSnapshot", store.BeginSnapshot(ctx, app.BeginSnapshot{ID: snapshot, JobID: name + "-job", OwnerID: name, ProjectID: project, Repository: name + "/r", Branch: "main", Commit: commit, MetadataJSON: "{}", Language: "Go", Manifest: "[]"})) {
		return
	}
	if staging, e := store.FindStagingSnapshot(ctx, name+"-job"); !do("FindStagingSnapshot", e) || !staging.Found {
		return fmt.Errorf("%s: FindStagingSnapshot did not find the staging snapshot (%v)", name, e)
	}
	if !do("AddSnapshotFiles", store.AddSnapshotFiles(ctx, snapshot, []app.SnapshotFile{{Path: "main.go", Mode: "100644", Hash: "h", Content: []byte("package main")}})) {
		return
	}
	if !do("TransitionImportJob", store.TransitionImportJob(ctx, app.JobTransition{ID: name + "-job", From: "cloning", To: "storing", Progress: 70})) {
		return
	}
	if _, e := store.ListResumableImports(ctx); !do("ListResumableImports", e) {
		return
	}
	if active, e := store.ListActiveJobs(ctx, name); !do("ListActiveJobs", e) || len(active) != 1 {
		return fmt.Errorf("%s: ListActiveJobs = %v, %v", name, active, e)
	}
	if !do("CompleteSnapshot", store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: snapshot, JobID: name + "-job", FileCount: 1, TotalBytes: 12})) {
		return
	}
	if job, found, e := store.FindImportJob(ctx, name+"-job", name); !do("FindImportJob", e) || !found || job.Status != "complete" {
		return fmt.Errorf("%s: FindImportJob = %+v, %v, %v", name, job, found, e)
	}
	if got, found, e := store.GetProject(ctx, project, name); !do("GetProject", e) || !found || got.SnapshotID != snapshot {
		return fmt.Errorf("%s: GetProject = %+v, %v, %v", name, got, found, e)
	}
	if projects, e := store.ListProjects(ctx, name); !do("ListProjects", e) || len(projects) != 1 {
		return fmt.Errorf("%s: ListProjects = %v, %v", name, projects, e)
	}
	if snapshots, e := store.ListSnapshots(ctx, name, project); !do("ListSnapshots", e) || len(snapshots) != 1 {
		return fmt.Errorf("%s: ListSnapshots = %v, %v", name, snapshots, e)
	}
	if activated, e := store.ActivateSnapshot(ctx, name, project, snapshot); !do("ActivateSnapshot", e) || !activated {
		return fmt.Errorf("%s: ActivateSnapshot = %v, %v", name, activated, e)
	}
	if !do("SetProjectEntryPoint", store.SetProjectEntryPoint(ctx, app.SetProjectEntryPoint{OwnerID: name, ProjectID: project, SnapshotID: snapshot, EntryPoint: "main.go"})) {
		return
	}
	if files, e := store.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: name, ProjectID: project, SnapshotID: snapshot, Limit: 10}); !do("ListProjectFiles", e) || len(files) != 1 {
		return fmt.Errorf("%s: ListProjectFiles = %v, %v", name, files, e)
	}

	// A second import that is abandoned: its staging snapshot is discarded and deleted.
	if !do("CreateImportJob", store.CreateImportJob(ctx, app.CreateImportJob{ID: name + "-job2", OwnerID: name, ProjectID: project, Repository: name + "/r", Branch: "main", Commit: commit})) {
		return
	}
	if !do("BeginSnapshot", store.BeginSnapshot(ctx, app.BeginSnapshot{ID: name + "-staging", JobID: name + "-job2", OwnerID: name, ProjectID: project, Repository: name + "/r", Branch: "main", Commit: commit, MetadataJSON: "{}"})) {
		return
	}
	if !do("DeleteStagingSnapshot", store.DeleteStagingSnapshot(ctx, name+"-staging")) {
		return
	}
	if !do("DiscardJobStaging", store.DiscardJobStaging(ctx, name+"-job2")) {
		return
	}
	if !do("TransitionImportJob", store.TransitionImportJob(ctx, app.JobTransition{ID: name + "-job2", From: "queued", To: "failed", Progress: 100, Error: "abandoned"})) {
		return
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
	if got, found, e := store.FindWASMBuild(ctx, build, name); !do("FindWASMBuild", e) || !found || got.Status != "complete" {
		return fmt.Errorf("%s: FindWASMBuild = %+v, %v, %v", name, got, found, e)
	}
	if artifact, e := store.FindWASMBuildArtifact(ctx, build, name); !do("FindWASMBuildArtifact", e) || !artifact.Found {
		return fmt.Errorf("%s: FindWASMBuildArtifact = %+v, %v", name, artifact, e)
	}

	if deleted, e := store.DeleteProject(ctx, project, name); !do("DeleteProject", e) || !deleted {
		return fmt.Errorf("%s: DeleteProject = %v, %v", name, deleted, e)
	}
	if deleted, e := store.DeleteAccount(ctx, name, false); !do("DeleteAccount", e) || !deleted.Deleted {
		return fmt.Errorf("%s: DeleteAccount = %+v, %v", name, deleted, e)
	}
	return err
}
