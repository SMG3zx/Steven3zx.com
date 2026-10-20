//go:build !linux && !windows

package wasmtime

import "time"

// threadCPUTime reports that thread CPU time is not read on this platform, so
// runs here report no CPU time. Production nodes run Linux.
func threadCPUTime() (d time.Duration, ok bool) { return 0, false }

// threadClock reports no readable clock on this platform.
func threadClock() (read func() (time.Duration, bool), release func()) {
	return func() (time.Duration, bool) { return 0, false }, func() {}
}
