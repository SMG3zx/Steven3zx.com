package main

import (
	"syscall"
	"unsafe"
)

// Go's time.Now on Windows only advances with the system timer tick, about
// half a millisecond, which is useless for microsecond latencies and for
// spin(25us). The performance counter is a real high-resolution clock.
var (
	qpcAddr uintptr
	qpcFreq int64
)

func init() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	freq := k32.NewProc("QueryPerformanceFrequency")
	counter := k32.NewProc("QueryPerformanceCounter")
	if err := freq.Find(); err != nil {
		panic(err)
	}
	if err := counter.Find(); err != nil {
		panic(err)
	}
	qpcAddr = counter.Addr()
	syscall.SyscallN(freq.Addr(), uintptr(unsafe.Pointer(&qpcFreq)))
}

// nanos returns a monotonic high-resolution timestamp in nanoseconds.
func nanos() int64 {
	var c int64
	syscall.SyscallN(qpcAddr, uintptr(unsafe.Pointer(&c)))
	return (c/qpcFreq)*1e9 + (c%qpcFreq)*1e9/qpcFreq
}
