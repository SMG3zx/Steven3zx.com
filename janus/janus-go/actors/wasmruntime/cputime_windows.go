//go:build windows

package wasmruntime

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getThreadTimes = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetThreadTimes")

// threadCPUTime returns the CPU time (kernel plus user) the calling OS thread has
// used so far. Windows keeps it in 100 ns units but advances it by scheduler
// tick, about 15.6 ms, so readings are only accurate for runs well past that.
// ok is false when the clock cannot be read.
func threadCPUTime() (d time.Duration, ok bool) {
	var created, exited, kernel, user windows.Filetime
	r, _, _ := getThreadTimes.Call(
		uintptr(windows.CurrentThread()),
		uintptr(unsafe.Pointer(&created)),
		uintptr(unsafe.Pointer(&exited)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if r == 0 {
		return 0, false
	}
	units := filetimeUnits(kernel) + filetimeUnits(user)
	return time.Duration(units) * 100, true
}

func filetimeUnits(f windows.Filetime) int64 {
	return int64(f.HighDateTime)<<32 | int64(f.LowDateTime)
}
