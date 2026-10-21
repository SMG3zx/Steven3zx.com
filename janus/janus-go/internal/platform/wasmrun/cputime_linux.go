//go:build linux

package wasmrun

import (
	"time"

	"golang.org/x/sys/unix"
)

// threadCPUTime returns the CPU time the calling OS thread has used so far, in
// nanoseconds. ok is false when the clock cannot be read.
func threadCPUTime() (d time.Duration, ok bool) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &ts); err != nil {
		return 0, false
	}
	return time.Duration(ts.Nano()), true
}

// threadClock returns a reader for the calling thread's CPU time that any
// goroutine may call, so a sampler can watch a run from another thread. The
// kernel names a thread's CPU clock from its id: the complement of the id shifted
// left three bits, with the per-thread (4) and scheduler (2) flags in the low bits.
func threadClock() (read func() (time.Duration, bool), release func()) {
	clock := int32(^uint32(unix.Gettid())<<3 | 6)
	read = func() (time.Duration, bool) {
		var ts unix.Timespec
		if err := unix.ClockGettime(clock, &ts); err != nil {
			return 0, false
		}
		return time.Duration(ts.Nano()), true
	}
	return read, func() {}
}
