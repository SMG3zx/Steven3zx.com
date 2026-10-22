//go:build windows

package throttle

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

type fileTime struct{ low, high uint32 }

func (f fileTime) u64() uint64 { return uint64(f.high)<<32 | uint64(f.low) }

// cpuTimes returns cumulative idle and total CPU time across all cores.
// GetSystemTimes' kernel time already includes idle time.
func cpuTimes() (idle, total uint64, err error) {
	var i, k, u fileTime
	r, _, e := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&i)),
		uintptr(unsafe.Pointer(&k)),
		uintptr(unsafe.Pointer(&u)))
	if r == 0 {
		return 0, 0, e
	}
	return i.u64(), k.u64() + u.u64(), nil
}

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX structure (64 bytes).
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// memPercent returns the percentage of physical memory in use.
func memPercent() (float64, error) {
	var m memoryStatusEx
	m.length = uint32(unsafe.Sizeof(m))
	r, _, e := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, e
	}
	return float64(m.memoryLoad), nil
}
