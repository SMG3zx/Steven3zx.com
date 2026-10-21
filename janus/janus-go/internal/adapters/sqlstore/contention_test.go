//go:build contention

package sqlstore

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"janus/internal/adapters/storetest"
	"janus/internal/app"
)

// TestContention measures how long sign-in lookups and project pages wait while
// a large Snapshot import and a build's file reads share the single connection.
// It is the measurement ENG-105 asks for, kept so it can be repeated:
//
//	go test -tags contention -run TestContention -v ./internal/adapters/sqlstore
func TestContention(t *testing.T) {
	store := openTest(t, filepath.Join(t.TempDir(), "contention.db"))
	ctx := context.Background()
	must(t, store.AddOwner(ctx, "alice"))
	_, err := store.CreateProject(ctx, app.CreateProject{ID: "p", OwnerID: "alice", Name: "p"})
	must(t, err)
	commit := strings.Repeat("a", 40)

	// A finished Snapshot of 2,000 files of 16 KiB, which the build reads.
	content := make([]byte, 16<<10)
	seed := func(job, snapshot string, files int, finish bool) {
		must(t, store.CreateImportJob(ctx, app.CreateImportJob{ID: job, OwnerID: "alice", ProjectID: "p", Repository: "alice/r", Branch: "main", Commit: commit}))
		storetest.AdvanceImport(t, store, job, "queued", "cloning", "scanning", "storing")
		must(t, store.BeginSnapshot(ctx, app.BeginSnapshot{ID: snapshot, JobID: job, OwnerID: "alice", ProjectID: "p", Repository: "alice/r", Branch: "main", Commit: commit, MetadataJSON: "{}", Language: "Go"}))
		for start := 0; start < files; start += 100 {
			batch := make([]app.SnapshotFile, 0, 100)
			for i := start; i < min(start+100, files); i++ {
				batch = append(batch, app.SnapshotFile{Path: fmt.Sprintf("%s/file-%05d.bin", snapshot, i), Mode: "100644", Hash: fmt.Sprint(i), Content: content})
			}
			must(t, store.AddSnapshotFiles(ctx, snapshot, batch))
		}
		if finish {
			must(t, store.CompleteSnapshot(ctx, app.CompleteSnapshot{SnapshotID: snapshot, JobID: job, FileCount: files, TotalBytes: int64(files * len(content))}))
		}
	}
	seed("job-1", "readable", 2000, true)

	measure := func(label string, n int) (p50, p95, worst time.Duration) {
		var signIn, page []time.Duration
		for range n {
			start := time.Now()
			// Signing in adds the Owner here; the credential lookup itself is in
			// the identity service's own database since ENG-117.
			if err := store.AddOwner(ctx, "alice"); err != nil {
				t.Fatalf("sign-in: %v", err)
			}
			signIn = append(signIn, time.Since(start))
			start = time.Now()
			if _, found, err := store.GetProject(ctx, "p", "alice"); err != nil || !found {
				t.Fatalf("project page: %v", err)
			}
			if _, err := store.ListSnapshots(ctx, "alice", "p"); err != nil {
				t.Fatal(err)
			}
			page = append(page, time.Since(start))
		}
		report := func(name string, d []time.Duration) {
			slices.Sort(d)
			t.Logf("%-28s %-9s p50 %-10v p95 %-10v max %v", label, name, d[len(d)/2], d[len(d)*95/100], d[len(d)-1])
		}
		report("sign-in lookup", signIn)
		report("project page", page)
		slices.Sort(signIn)
		return signIn[len(signIn)/2], signIn[len(signIn)*95/100], signIn[len(signIn)-1]
	}

	measure("idle", 300)

	// Load: an import storing 3,000 more files in batches of 100 (48 MiB), and
	// a build reading the first Snapshot's files in pages of 200, over and over.
	var load sync.WaitGroup
	stop := make(chan struct{})
	load.Go(func() {
		seed("job-2", "importing", 3000, false)
		close(stop)
	})
	load.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			for offset := 0; offset < 2000; offset += 200 {
				if _, err := store.ListProjectFiles(ctx, app.ListProjectFiles{OwnerID: "alice", ProjectID: "p", SnapshotID: "readable", Offset: offset, Limit: 200}); err != nil {
					t.Error(err)
					return
				}
			}
		}
	})
	time.Sleep(50 * time.Millisecond)
	_, _, worst := measure("while importing and building", 300)
	load.Wait()
	t.Logf("worst sign-in lookup under load: %v", worst)
}
