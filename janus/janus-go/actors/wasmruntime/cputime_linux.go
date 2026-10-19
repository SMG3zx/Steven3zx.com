//go:build linux

package wasmruntime

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
