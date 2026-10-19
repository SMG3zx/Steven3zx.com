package wasmruntime

import (
	"runtime"
	"time"
)

// measureCall runs call on the current OS thread and returns how long it took on
// the host clock (wall) and how much CPU time that thread spent in it (cpu).
//
// CPU time is active time only: a thread that sleeps or waits on I/O adds to
// wall but not to cpu. That is the quantity the CPU layer bills. cpu is zero
// when the platform cannot read thread CPU time (see threadCPUTime).
//
// The goroutine is pinned to its thread for the call, because the thread clock
// belongs to the thread and Go may otherwise move the goroutine to another one.
// Wasmtime runs the module on the calling thread, so the module's CPU time lands
// on the pinned thread.
func measureCall(call func() error) (wall, cpu time.Duration, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	before, beforeOK := threadCPUTime()
	began := time.Now()
	err = call()
	wall = time.Since(began)
	if after, afterOK := threadCPUTime(); beforeOK && afterOK && after >= before {
		cpu = after - before
	}
	return wall, cpu, err
}
