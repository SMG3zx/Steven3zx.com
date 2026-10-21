//go:build contention

package projects

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	contractprojects "janus/internal/contract/projects"
)

// TestContention measures how long project pages wait while a large Snapshot
// import and a build's file reads share projects.db's single connection. It is
// the measurement ENG-105 asked of janus.db, which now holds neither:
//
//	go test -tags contention -run TestContention -v ./internal/services/projects
func TestContention(t *testing.T) {
	h := start(t)
	ctx := context.Background()
	h.project("alice", "p")
	commit := strings.Repeat("a", 40)
	content := make([]byte, 16<<10)
	seed := func(snapshot string, files int, finish bool) {
		state := "staging"
		if finish {
			state = "ready"
		}
		if _, err := h.svc.db.Exec(`INSERT INTO project_snapshots(id,project_id,owner_id,repository_id,repository,branch,commit_sha,language,state,is_active) VALUES(?,'p','alice',1,'alice/r','main',?,'Go',?,1)`, snapshot, commit, state); err != nil {
			t.Fatal(err)
		}
		run := &importRun{job: contractprojects.ImportJob{ID: "none"}, status: "storing"}
		for start := 0; start < files; start += 100 {
			batch := make([]file, 0, 100)
			for i := start; i < min(start+100, files); i++ {
				batch = append(batch, file{Path: fmt.Sprintf("%s/file-%05d.bin", snapshot, i), Mode: "100644", Hash: fmt.Sprint(i), Content: content})
			}
			if err := h.svc.storeFiles(ctx, run, snapshot, batch); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed("readable", 2000, true)

	measure := func(label string, n int) time.Duration {
		page := make([]time.Duration, 0, n)
		for range n {
			start := time.Now()
			if _, err := h.svc.getProject(ctx, contractprojects.ProjectKey{OwnerID: "alice", ProjectID: "p"}); err != nil {
				t.Fatal(err)
			}
			if _, err := h.svc.listSnapshots(ctx, contractprojects.ProjectKey{OwnerID: "alice", ProjectID: "p"}); err != nil {
				t.Fatal(err)
			}
			page = append(page, time.Since(start))
		}
		slices.Sort(page)
		t.Logf("%-28s project page p50 %-10v p95 %-10v max %v", label, page[len(page)/2], page[len(page)*95/100], page[len(page)-1])
		return page[len(page)-1]
	}
	measure("idle", 300)

	// Load: an import storing 3,000 more files in batches of 100 (48 MiB), and a
	// build reading the first Snapshot's files in pages, over and over.
	var load sync.WaitGroup
	stop := make(chan struct{})
	load.Go(func() {
		seed("importing", 3000, false)
		close(stop)
	})
	load.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			page := contractprojects.SnapshotFiles{OwnerID: "alice", ProjectID: "p", SnapshotID: "readable"}
			for {
				files, err := h.svc.snapshotFiles(ctx, page)
				if err != nil {
					t.Error(err)
					return
				}
				if !files.More {
					break
				}
				page.Offset += len(files.Files)
			}
		}
	})
	worst := measure("while importing and building", 300)
	load.Wait()
	t.Logf("worst project page under load: %v", worst)
}
