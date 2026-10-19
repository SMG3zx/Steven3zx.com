//go:build !linux && !windows

package wasmruntime

import "time"

// threadCPUTime reports that thread CPU time is not read on this platform, so
// runs here report no CPU time. Production nodes run Linux.
func threadCPUTime() (d time.Duration, ok bool) { return 0, false }
