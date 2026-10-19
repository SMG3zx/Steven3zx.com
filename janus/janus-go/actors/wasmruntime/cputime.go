package wasmruntime

import (
	"runtime"
	"sync"
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
	return measureCallSampled(0, nil, call)
}

// measureCallSampled is measureCall that also reports progress. While call runs,
// onSample is invoked every interval with the CPU time the call has used so far,
// so a long-running instance can be billed as it goes instead of only at exit.
// onSample runs on its own goroutine, never on the call's thread, and has
// returned before measureCallSampled does. A zero interval or nil onSample
// disables sampling. Nothing is reported when the clock cannot be read.
func measureCallSampled(interval time.Duration, onSample func(cpu time.Duration), call func() error) (wall, cpu time.Duration, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	read, release := threadClock()
	defer release()
	before, beforeOK := read()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	if interval > 0 && onSample != nil && beforeOK {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					if now, ok := read(); ok && now >= before {
						onSample(now - before)
					}
				}
			}
		}()
	}

	began := time.Now()
	err = call()
	wall = time.Since(began)
	close(stop)
	wg.Wait()
	if after, afterOK := read(); beforeOK && afterOK && after >= before {
		cpu = after - before
	}
	return wall, cpu, err
}
