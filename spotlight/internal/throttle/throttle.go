// Package throttle keeps background indexing from saturating the machine.
//
// It does two things:
//
//   - Wait pauses work while system-wide CPU, memory or GPU load is above a
//     limit, so indexing yields to whatever else you are doing.
//   - Pace spaces out GPU work so that this process alone keeps the GPU busy no
//     more than the GPU limit's share of the time (80% limit means roughly one
//     idle moment for every four busy ones).
//
// Limits are percentages. Zero disables a limit. A sensor that can't be read
// (for example nvidia-smi on a machine without an NVIDIA GPU) is skipped with a
// one-time warning rather than blocking indexing forever.
package throttle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

const (
	defaultPoll = time.Second
	cpuWindow   = 250 * time.Millisecond // shortest span CPU load is averaged over
	gpuCacheTTL = time.Second            // nvidia-smi is a process spawn; don't call it per batch
	logEvery    = 30 * time.Second
)

// Limits are percentages from 0 to 100. Zero disables that limit.
type Limits struct {
	CPU, Mem, GPU float64
}

type gpuReading struct {
	util float64 // percent of time a kernel was running
	vram float64 // percent of video memory in use
}

type Governor struct {
	lim  Limits
	log  *slog.Logger
	poll time.Duration

	// Sensors and timings; tests replace these.
	cpuTimes   func() (idle, total uint64, err error)
	memPercent func() (float64, error)
	gpuQuery   func(ctx context.Context) (gpuReading, error)
	cpuWindow  time.Duration
	gpuTTL     time.Duration

	mu                  sync.Mutex
	prevIdle, prevTotal uint64
	cpuAt               time.Time
	cpuLast             float64
	cpuValid            bool
	gpuAt               time.Time
	gpuLast             gpuReading
	gpuValid            bool
	gpuGone             bool
	warned              map[string]bool
}

func New(lim Limits, log *slog.Logger) *Governor {
	return &Governor{
		lim:        lim,
		log:        log,
		poll:       defaultPoll,
		cpuTimes:   cpuTimes,
		memPercent: memPercent,
		gpuQuery:   queryNvidiaSMI,
		cpuWindow:  cpuWindow,
		gpuTTL:     gpuCacheTTL,
		warned:     make(map[string]bool),
	}
}

// Probe logs the active limits and reports which sensors work, so a limit that
// can't be enforced is visible at startup instead of silently doing nothing.
func (g *Governor) Probe(ctx context.Context) {
	if g == nil {
		return
	}
	g.log.Info("resource limits (0 = off)", "cpu", g.lim.CPU, "memory", g.lim.Mem, "gpu", g.lim.GPU)
	if g.lim.CPU > 0 {
		if _, _, err := g.cpuTimes(); err != nil {
			g.log.Warn("cpu limit not enforced: cannot read CPU load", "err", err)
		}
	}
	if g.lim.Mem > 0 {
		if _, err := g.memPercent(); err != nil {
			g.log.Warn("memory limit not enforced: cannot read memory load", "err", err)
		}
	}
	if g.lim.GPU > 0 {
		if r, err := g.gpuQuery(ctx); err != nil {
			g.log.Warn("cannot read GPU load (nvidia-smi); GPU limit is enforced by pacing only and will not yield to other GPU use", "err", err)
		} else {
			g.log.Info("gpu sensor ok", "util", r.util, "vram", r.vram)
		}
	}
}

// Wait blocks until CPU, memory and GPU load are all at or below their limits,
// or ctx ends. It returns immediately when nothing is over a limit.
func (g *Governor) Wait(ctx context.Context) error {
	if g == nil {
		return nil
	}
	var lastLog time.Time
	for {
		reason := g.check(ctx)
		if reason == "" {
			return nil
		}
		if time.Since(lastLog) >= logEvery {
			g.log.Info("indexing paused: system busy", "reason", reason)
			lastLog = time.Now()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(g.poll):
		}
	}
}

// Pace sleeps after a stretch of GPU work that kept the GPU busy for busy, so
// the busy share of time stays at or under the GPU limit.
func (g *Governor) Pace(ctx context.Context, busy time.Duration) error {
	if g == nil || g.lim.GPU <= 0 || g.lim.GPU >= 100 || busy <= 0 {
		return nil
	}
	share := g.lim.GPU / 100
	idle := time.Duration(float64(busy) * (1/share - 1))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(idle):
		return nil
	}
}

// check returns a description of the first limit that is exceeded, or "".
func (g *Governor) check(ctx context.Context) string {
	if g.lim.CPU > 0 {
		if v, ok := g.sampleCPU(); ok && v > g.lim.CPU {
			return fmt.Sprintf("cpu %.0f%% > %.0f%%", v, g.lim.CPU)
		}
	}
	if g.lim.Mem > 0 {
		v, err := g.memPercent()
		if err != nil {
			g.warnOnce("memory", err)
		} else if v > g.lim.Mem {
			return fmt.Sprintf("memory %.0f%% > %.0f%%", v, g.lim.Mem)
		}
	}
	if g.lim.GPU > 0 {
		if r, ok := g.sampleGPU(ctx); ok {
			if r.util > g.lim.GPU {
				return fmt.Sprintf("gpu %.0f%% > %.0f%%", r.util, g.lim.GPU)
			}
			if r.vram > g.lim.GPU {
				return fmt.Sprintf("gpu memory %.0f%% > %.0f%%", r.vram, g.lim.GPU)
			}
		}
	}
	return ""
}

// sampleCPU returns system CPU load averaged since the previous sample. The
// first call only records a baseline and reports no reading.
func (g *Governor) sampleCPU() (float64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	if g.prevTotal != 0 && now.Sub(g.cpuAt) < g.cpuWindow {
		return g.cpuLast, g.cpuValid
	}
	idle, total, err := g.cpuTimes()
	if err != nil {
		g.warnLocked("cpu", err)
		return 0, false
	}
	if g.prevTotal == 0 {
		g.prevIdle, g.prevTotal, g.cpuAt = idle, total, now
		return 0, false
	}
	dTotal, dIdle := total-g.prevTotal, idle-g.prevIdle
	g.prevIdle, g.prevTotal, g.cpuAt = idle, total, now
	if dTotal == 0 {
		return g.cpuLast, g.cpuValid
	}
	g.cpuLast = clamp(100 * (1 - float64(dIdle)/float64(dTotal)))
	g.cpuValid = true
	return g.cpuLast, true
}

func (g *Governor) sampleGPU(ctx context.Context) (gpuReading, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.gpuGone {
		return gpuReading{}, false
	}
	if g.gpuValid && time.Since(g.gpuAt) < g.gpuTTL {
		return g.gpuLast, true
	}
	r, err := g.gpuQuery(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return gpuReading{}, false
		}
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			g.gpuGone = true // no NVIDIA tooling; stop trying
		}
		g.warnLocked("gpu", err)
		return gpuReading{}, false
	}
	g.gpuLast, g.gpuAt, g.gpuValid = r, time.Now(), true
	return r, true
}

func (g *Governor) warnOnce(sensor string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.warnLocked(sensor, err)
}

func (g *Governor) warnLocked(sensor string, err error) {
	if g.warned[sensor] {
		return
	}
	g.warned[sensor] = true
	g.log.Warn("resource sensor unavailable; that limit is not enforced", "sensor", sensor, "err", err)
}

func clamp(v float64) float64 {
	return min(max(v, 0), 100)
}
