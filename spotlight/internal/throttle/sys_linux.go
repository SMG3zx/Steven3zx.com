//go:build linux

package throttle

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// cpuTimes returns cumulative idle and total CPU time from /proc/stat.
func cpuTimes() (idle, total uint64, err error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	line, _, _ := strings.Cut(string(b), "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, errors.New("unexpected /proc/stat format")
	}
	var vals []uint64
	for _, s := range f[1:] {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		vals = append(vals, v)
	}
	// user nice system idle iowait irq softirq steal; guest time is already
	// counted inside user and nice.
	for _, v := range vals[:min(len(vals), 8)] {
		total += v
	}
	idle = vals[3] + vals[4]
	return idle, total, nil
}

// memPercent returns the percentage of memory that is not available.
func memPercent() (float64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	var total, avail float64
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch strings.TrimSuffix(f[0], ":") {
		case "MemTotal":
			total, _ = strconv.ParseFloat(f[1], 64)
		case "MemAvailable":
			avail, _ = strconv.ParseFloat(f[1], 64)
		}
	}
	if total <= 0 || avail <= 0 {
		return 0, errors.New("MemTotal/MemAvailable not found in /proc/meminfo")
	}
	return clamp(100 * (1 - avail/total)), nil
}
