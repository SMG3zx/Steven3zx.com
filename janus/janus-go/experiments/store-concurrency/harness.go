package main

import (
	"math/bits"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Workload is one scenario. Every variant runs exactly the same one.
type Workload struct {
	Name    string
	Desc    string
	Records int
	ReadPct int           // percent of foreground ops that are Get; the rest are Update
	HotPct  int           // percent of ops aimed at key 0
	Work    time.Duration // spent inside the owner on each Get and Update
	Scanner bool          // add one background worker that scans in a loop
	Workers []int         // foreground goroutine counts to try
}

var workloads = []Workload{
	{Name: "read95", Desc: "95% Get, 5% Update, 10k keys, uniform", Records: 10_000, ReadPct: 95, Workers: []int{1, 8, 64}},
	{Name: "mixed50", Desc: "50% Get, 50% Update, 10k keys, uniform", Records: 10_000, ReadPct: 50, Workers: []int{1, 8, 64}},
	{Name: "write100", Desc: "100% Update, 10k keys, uniform", Records: 10_000, ReadPct: 0, Workers: []int{1, 8, 64}},
	{Name: "hotkey", Desc: "50/50, 80% of ops on one key", Records: 10_000, ReadPct: 50, HotPct: 80, Workers: []int{1, 8, 64}},
	{Name: "scan-blocks", Desc: "95% Get, 5% Update on 100k keys, plus one goroutine scanning all 100k in a loop; latency is the foreground ops'", Records: 100_000, ReadPct: 95, Scanner: true, Workers: []int{8, 64}},
	{Name: "slow-io-25us", Desc: "50/50, 10k keys, 25us of work inside the owner per op (a store bound by one database connection)", Records: 10_000, ReadPct: 50, Work: 25 * time.Microsecond, Workers: []int{1, 8, 64}},
}

// hist is a log-linear latency histogram: four buckets per power of two.
type hist struct{ b [64 * 4]uint32 }

func bucket(ns int64) int {
	if ns < 4 {
		return int(ns)
	}
	l := bits.Len64(uint64(ns))
	return l*4 + int((uint64(ns)>>(l-3))&3) - 4
}

// bucketUpper returns the upper bound, in ns, of bucket i.
func bucketUpper(i int) int64 {
	if i < 4 {
		return int64(i) + 1
	}
	l := (i + 4) / 4
	sub := int64((i + 4) % 4)
	return (int64(4) + sub + 1) << (l - 3)
}

func (h *hist) add(d time.Duration) {
	i := bucket(int64(d))
	if i >= len(h.b) {
		i = len(h.b) - 1
	}
	h.b[i]++
}

func (h *hist) merge(o *hist) {
	for i := range h.b {
		h.b[i] += o.b[i]
	}
}

func (h *hist) total() uint64 {
	var n uint64
	for _, c := range h.b {
		n += uint64(c)
	}
	return n
}

func (h *hist) percentile(p float64) time.Duration {
	total := h.total()
	if total == 0 {
		return 0
	}
	target := uint64(float64(total) * p)
	var cum uint64
	for i, c := range h.b {
		cum += uint64(c)
		if cum > target {
			return time.Duration(bucketUpper(i))
		}
	}
	return 0
}

// Result is one measured run.
type Result struct {
	Variant   string
	Workload  string
	Workers   int
	Ops       uint64
	OpsPerSec float64
	P50       time.Duration
	P99       time.Duration
	P999      time.Duration
	Allocs    float64 // heap allocations per op
	Scans     float64 // background scans per second
	Spread    float64 // (max-min)/median ops/s over the repetitions
}

type xorshift uint64

func (x *xorshift) next() uint64 {
	v := uint64(*x)
	v ^= v << 13
	v ^= v >> 7
	v ^= v << 17
	*x = xorshift(v)
	return v
}

const (
	phaseWarm = iota
	phaseMeasure
	phaseStop
)

func runOnce(v Variant, w Workload, workers int, warm, dur time.Duration) Result {
	s := v.New(Config{Records: w.Records, Work: w.Work})
	defer s.Close()
	runtime.GC()

	var phase atomic.Int32
	hists := make([]hist, workers)
	ops := make([]uint64, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rng := xorshift(0x9E3779B97F4A7C15 ^ uint64(i+1)*0xBF58476D1CE4E5B9)
			h := &hists[i]
			for {
				p := phase.Load()
				if p == phaseStop {
					return
				}
				id := rng.next() % uint64(w.Records)
				if w.HotPct > 0 && int(rng.next()%100) < w.HotPct {
					id = 0
				}
				read := int(rng.next()%100) < w.ReadPct
				// Time one op in eight: the clock call costs about as much as a
				// fast op, and the same sampling applies to every variant.
				sample := p == phaseMeasure && ops[i]&7 == 0
				var t0 int64
				if sample {
					t0 = nanos()
				}
				if read {
					s.Get(id)
				} else {
					s.Update(id, bump)
				}
				if sample {
					h.add(time.Duration(nanos() - t0))
				}
				if p == phaseMeasure {
					ops[i]++
				}
			}
		}(i)
	}
	var scans atomic.Uint64
	if w.Scanner {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for owner := uint32(0); phase.Load() != phaseStop; owner++ {
				s.Scan(owner % 64)
				if phase.Load() == phaseMeasure {
					scans.Add(1)
				}
			}
		}()
	}

	time.Sleep(warm)
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	start := time.Now()
	phase.Store(phaseMeasure)
	time.Sleep(dur)
	phase.Store(phaseStop)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&m1)
	wg.Wait()

	var total hist
	var n uint64
	for i := range hists {
		total.merge(&hists[i])
		n += ops[i]
	}
	r := Result{Variant: v.Name, Workload: w.Name, Workers: workers, Ops: n,
		OpsPerSec: float64(n) / elapsed.Seconds(),
		P50:       total.percentile(0.50), P99: total.percentile(0.99), P999: total.percentile(0.999),
		Scans: float64(scans.Load()) / elapsed.Seconds()}
	if n > 0 {
		r.Allocs = float64(m1.Mallocs-m0.Mallocs) / float64(n)
	}
	return r
}

// run repeats a cell and keeps the median run by throughput.
func run(v Variant, w Workload, workers, reps int, warm, dur time.Duration) Result {
	results := make([]Result, reps)
	for i := range results {
		results[i] = runOnce(v, w, workers, warm, dur)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].OpsPerSec < results[j].OpsPerSec })
	med := results[len(results)/2]
	if med.OpsPerSec > 0 {
		med.Spread = (results[len(results)-1].OpsPerSec - results[0].OpsPerSec) / med.OpsPerSec
	}
	return med
}
