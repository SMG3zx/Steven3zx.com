//go:build !linux && !windows

package throttle

import "errors"

var errUnsupported = errors.New("CPU and memory sensors are not implemented on this OS")

func cpuTimes() (idle, total uint64, err error) { return 0, 0, errUnsupported }

func memPercent() (float64, error) { return 0, errUnsupported }
