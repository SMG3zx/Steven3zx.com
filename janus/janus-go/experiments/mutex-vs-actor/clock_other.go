//go:build !windows

package main

import "time"

var clockStart = time.Now()

// nanos returns a monotonic timestamp in nanoseconds.
func nanos() int64 { return int64(time.Since(clockStart)) }
