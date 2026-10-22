//go:build linux

package throttle

import "testing"

func TestLinuxSensorsReadRealValues(t *testing.T) {
	idle, total, err := cpuTimes()
	if err != nil {
		t.Skipf("no /proc/stat: %v", err)
	}
	if total == 0 || idle > total {
		t.Fatalf("implausible cpu counters: idle=%d total=%d", idle, total)
	}
	m, err := memPercent()
	if err != nil {
		t.Skipf("no /proc/meminfo: %v", err)
	}
	if m <= 0 || m > 100 {
		t.Fatalf("memory percent %v out of range", m)
	}
}
