package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

// What the tuning pass picked for each family. Edit after running -tune.
var (
	bestMutexShards = 1024
	bestChanShards  = 256
	// Janus today: one actor, default mailbox, default dispatcher budget, Ask.
	janusActor = ActorParams{Shards: 1, Ask: true, Mailbox: "default", Throughput: 32}
	// The best single actor and the best sharded actors the tuning pass found.
	bestSingleActor  = ActorParams{Shards: 1, Mailbox: "segmented", Throughput: 256}
	bestShardedActor = ActorParams{Shards: 12, Mailbox: "default", Throughput: 2048}
)

func withAsk(p ActorParams) ActorParams { p.Ask = true; return p }

func variants() []Variant {
	return []Variant{
		{"mutex/coarse", "mutex", newCoarse},
		{"mutex/rwmutex", "mutex", newRW},
		{fmt.Sprintf("mutex/sharded-%d", bestMutexShards), "mutex", newShardedRW(bestMutexShards)},
		{"atomic/slots-cas", "atomic", newSlotsCAS},
		{"atomic/syncmap-cas", "atomic", newSyncMapCAS},
		{"atomic/slots-fetch-add (counter only)", "atomic", newSlotsFetchAdd},
		{"chan/single-owner", "channel", newChanStore(1)},
		{fmt.Sprintf("chan/sharded-%d", bestChanShards), "channel", newChanStore(bestChanShards)},
		{"actor/janus-today", "actor", newActorStore(janusActor)},
		{"actor/single-tuned", "actor", newActorStore(bestSingleActor)},
		{fmt.Sprintf("actor/sharded-%d-ask", bestShardedActor.Shards), "actor", newActorStore(withAsk(bestShardedActor))},
		{fmt.Sprintf("actor/sharded-%d-tuned", bestShardedActor.Shards), "actor", newActorStore(bestShardedActor)},
	}
}

func main() {
	var (
		dur       = flag.Duration("dur", 1500*time.Millisecond, "measured time per run")
		warm      = flag.Duration("warm", 300*time.Millisecond, "warm-up per run")
		reps      = flag.Int("reps", 3, "runs per cell; the median is kept")
		only      = flag.String("only", "", "comma-separated workload names")
		match     = flag.String("variants", "", "substring filter on variant names")
		tune      = flag.Bool("tune", false, "run the tuning sweep instead of the matrix")
		out       = flag.String("out", "results", "directory for results.md and results.csv")
		gcPercent = flag.Int("gc", -1, "GC percent, -1 leaves the default")
	)
	flag.Parse()
	if *gcPercent >= 0 {
		debug.SetGCPercent(*gcPercent)
	}
	defer stopSystems()

	if *tune {
		runTune(*dur, *warm, *reps)
		return
	}

	selected := map[string]bool{}
	for _, n := range strings.Split(*only, ",") {
		if n != "" {
			selected[n] = true
		}
	}
	var results []Result
	for _, w := range workloads {
		if len(selected) > 0 && !selected[w.Name] {
			continue
		}
		for _, workers := range w.Workers {
			for _, v := range variants() {
				if *match != "" && !strings.Contains(v.Name, *match) {
					continue
				}
				r := run(v, w, workers, *reps, *warm, *dur)
				fmt.Fprintf(os.Stderr, "%-12s w=%-3d %-26s %10.0f ops/s  p50 %-9v p99 %-9v p99.9 %-9v spread %3.0f%%\n",
					w.Name, workers, v.Name, r.OpsPerSec, r.P50, r.P99, r.P999, r.Spread*100)
				results = append(results, r)
			}
		}
	}
	if err := writeReport(*out, results, *dur, *reps); err != nil {
		fmt.Fprintln(os.Stderr, "write report:", err)
		os.Exit(1)
	}
}

// runTune sweeps each family's knobs on two foreground-heavy workloads at 64
// workers, so the head-to-head compares every family at its best.
func runTune(dur, warm time.Duration, reps int) {
	cells := []Workload{workloads[0], workloads[1]}
	type cand struct {
		name string
		v    Variant
	}
	var cands []cand
	for _, n := range []int{8, 16, 64, 256, 1024} {
		cands = append(cands, cand{fmt.Sprintf("mutex sharded-%d", n), Variant{Name: "m", New: newShardedRW(n)}})
		cands = append(cands, cand{fmt.Sprintf("chan sharded-%d", n), Variant{Name: "c", New: newChanStore(n)}})
	}
	for _, shards := range []int{1, 12, 64, 256} {
		for _, mb := range []string{"default", "segmented"} {
			for _, budget := range []int{32, 256, 2048} {
				p := ActorParams{Shards: shards, Mailbox: mb, Throughput: budget}
				cands = append(cands, cand{fmt.Sprintf("actor tell shards=%d mailbox=%s budget=%d", shards, mb, budget), Variant{Name: "a", New: newActorStore(p)}})
			}
		}
	}
	type row struct {
		name   string
		scores []float64
	}
	var rows []row
	for _, c := range cands {
		r := row{name: c.name}
		for _, w := range cells {
			res := run(c.v, w, 64, reps, warm, dur)
			r.scores = append(r.scores, res.OpsPerSec)
		}
		fmt.Fprintf(os.Stderr, "%-58s read95 %10.0f   mixed50 %10.0f\n", r.name, r.scores[0], r.scores[1])
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].scores[0]+rows[i].scores[1] > rows[j].scores[0]+rows[j].scores[1]
	})
	fmt.Println("Tuning sweep, 64 workers, ranked by read95+mixed50 ops/s:")
	for _, r := range rows {
		fmt.Printf("%-58s read95 %10.0f   mixed50 %10.0f\n", r.name, r.scores[0], r.scores[1])
	}
}

func writeReport(dir string, results []Result, dur time.Duration, reps int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var csv strings.Builder
	csv.WriteString("workload,workers,variant,ops_per_sec,p50_ns,p99_ns,p999_ns,allocs_per_op,scans_per_sec,spread\n")
	for _, r := range results {
		fmt.Fprintf(&csv, "%s,%d,%s,%.0f,%d,%d,%d,%.2f,%.1f,%.3f\n", r.Workload, r.Workers, r.Variant, r.OpsPerSec, r.P50, r.P99, r.P999, r.Allocs, r.Scans, r.Spread)
	}
	if err := os.WriteFile(filepath.Join(dir, "results.csv"), []byte(csv.String()), 0o644); err != nil {
		return err
	}

	var md strings.Builder
	fmt.Fprintf(&md, "# Raw results\n\nGo %s, %s/%s, GOMAXPROCS=%d, %d reps of %v per cell, median by throughput. Spread is (max-min)/median ops/s across the reps.\n\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), reps, dur)
	for _, w := range workloads {
		var any bool
		for _, r := range results {
			if r.Workload == w.Name {
				any = true
			}
		}
		if !any {
			continue
		}
		fmt.Fprintf(&md, "## %s\n\n%s\n\n", w.Name, w.Desc)
		for _, workers := range w.Workers {
			fmt.Fprintf(&md, "### %d workers\n\n| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |\n|---|---:|---:|---:|---:|---:|---:|\n", workers)
			for _, r := range results {
				if r.Workload != w.Name || r.Workers != workers {
					continue
				}
				fmt.Fprintf(&md, "| %s | %s | %v | %v | %v | %.1f | %.0f%% |\n", r.Variant, human(r.OpsPerSec), r.P50, r.P99, r.P999, r.Allocs, r.Spread*100)
			}
			if w.Scanner {
				md.WriteString("\nBackground scans per second: ")
				for _, r := range results {
					if r.Workload == w.Name && r.Workers == workers {
						fmt.Fprintf(&md, "%s %.0f; ", r.Variant, r.Scans)
					}
				}
				md.WriteString("\n")
			}
			md.WriteString("\n")
		}
	}
	return os.WriteFile(filepath.Join(dir, "results.md"), []byte(md.String()), 0o644)
}

func human(v float64) string {
	switch {
	case v >= 1e6:
		return fmt.Sprintf("%.2fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.1fk", v/1e3)
	}
	return fmt.Sprintf("%.0f", v)
}
