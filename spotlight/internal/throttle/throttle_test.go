package throttle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// newTest returns a governor whose sensors are all "unavailable" until a test
// sets the ones it cares about, with fast polling.
func newTest(lim Limits) *Governor {
	g := New(lim, slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.poll = 2 * time.Millisecond
	g.cpuWindow = 0
	g.gpuTTL = 0
	off := errors.New("sensor off")
	g.cpuTimes = func() (uint64, uint64, error) { return 0, 0, off }
	g.memPercent = func() (float64, error) { return 0, off }
	g.gpuQuery = func(context.Context) (gpuReading, error) { return gpuReading{}, off }
	return g
}

func TestPaceKeepsBusyShareAtLimit(t *testing.T) {
	g := newTest(Limits{GPU: 80})
	start := time.Now()
	if err := g.Pace(context.Background(), 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// 100ms busy at an 80% share needs 25ms idle.
	if got := time.Since(start); got < 20*time.Millisecond || got > 150*time.Millisecond {
		t.Fatalf("slept %v, want about 25ms", got)
	}
}

func TestPaceOffWhenDisabledOrFull(t *testing.T) {
	for _, gpu := range []float64{0, 100} {
		g := newTest(Limits{GPU: gpu})
		start := time.Now()
		if err := g.Pace(context.Background(), time.Second); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) > 20*time.Millisecond {
			t.Fatalf("limit %v should not pace", gpu)
		}
	}
}

func TestNilGovernorIsNoop(t *testing.T) {
	var g *Governor
	if err := g.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := g.Pace(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	g.Probe(context.Background())
}

func TestWaitBlocksWhileMemoryOverLimit(t *testing.T) {
	g := newTest(Limits{Mem: 80})
	calls := 0
	g.memPercent = func() (float64, error) {
		calls++
		if calls <= 3 {
			return 91, nil
		}
		return 40, nil
	}
	if err := g.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("memory sampled %d times, want 4", calls)
	}
}

func TestWaitStopsWhenContextEnds(t *testing.T) {
	g := newTest(Limits{Mem: 80})
	g.memPercent = func() (float64, error) { return 95, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := g.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
}

func TestWaitGPUUtilizationAndVRAM(t *testing.T) {
	g := newTest(Limits{GPU: 80})
	readings := []gpuReading{{util: 95}, {util: 30, vram: 90}, {util: 30, vram: 40}}
	i := 0
	g.gpuQuery = func(context.Context) (gpuReading, error) {
		r := readings[min(i, len(readings)-1)]
		i++
		return r, nil
	}
	if err := g.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if i != 3 {
		t.Fatalf("gpu sampled %d times, want 3 (busy, vram full, clear)", i)
	}
}

func TestMissingSensorsDoNotBlock(t *testing.T) {
	g := newTest(Limits{CPU: 80, Mem: 80, GPU: 80}) // every sensor errors
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := g.Wait(ctx); err != nil {
		t.Fatalf("unavailable sensors must not block: %v", err)
	}
}

func TestDisabledLimitsNeverSample(t *testing.T) {
	g := newTest(Limits{})
	g.memPercent = func() (float64, error) { t.Fatal("memory sampled while disabled"); return 0, nil }
	g.gpuQuery = func(context.Context) (gpuReading, error) {
		t.Fatal("gpu sampled while disabled")
		return gpuReading{}, nil
	}
	if err := g.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCPUPercentFromCounters(t *testing.T) {
	g := newTest(Limits{CPU: 80})
	var idle, total uint64
	g.cpuTimes = func() (uint64, uint64, error) {
		idle += 100 // 10% idle per step, so 90% busy
		total += 1000
		return idle, total, nil
	}
	if _, ok := g.sampleCPU(); ok {
		t.Fatal("first call only records a baseline")
	}
	v, ok := g.sampleCPU()
	if !ok || v < 89.9 || v > 90.1 {
		t.Fatalf("got %v, %v; want 90, true", v, ok)
	}
	if reason := g.check(context.Background()); reason == "" {
		t.Fatal("90% cpu should exceed an 80% limit")
	}
}

func TestParseSMI(t *testing.T) {
	r, err := parseSMI("37, 1024, 12288\n5, 6144, 8192\n")
	if err != nil {
		t.Fatal(err)
	}
	if r.util != 37 || r.vram != 75 {
		t.Fatalf("got %+v, want util 37 and vram 75 (busiest of each)", r)
	}
	if _, err := parseSMI("[N/A], [N/A], [N/A]"); err == nil {
		t.Fatal("expected an error for unparseable output")
	}
}
