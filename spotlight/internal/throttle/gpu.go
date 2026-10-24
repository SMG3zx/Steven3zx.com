package throttle

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// queryNvidiaSMI reads GPU and video-memory load from nvidia-smi, which ships
// with the NVIDIA driver on Windows and Linux. With several GPUs it reports the
// busiest value of each.
func queryNvidiaSMI(ctx context.Context) (gpuReading, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, smiPath(),
		"--query-gpu=utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits")
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return gpuReading{}, err
	}
	return parseSMI(string(out))
}

// parseSMI parses lines like "37, 1024, 12288" (util %, MiB used, MiB total).
func parseSMI(out string) (gpuReading, error) {
	var worst gpuReading
	found := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, ",")
		if len(f) != 3 {
			continue
		}
		util, e1 := strconv.ParseFloat(strings.TrimSpace(f[0]), 64)
		used, e2 := strconv.ParseFloat(strings.TrimSpace(f[1]), 64)
		total, e3 := strconv.ParseFloat(strings.TrimSpace(f[2]), 64)
		if e1 != nil || e2 != nil || e3 != nil || total <= 0 {
			continue
		}
		worst.util = max(worst.util, util)
		worst.vram = max(worst.vram, 100*used/total)
		found = true
	}
	if !found {
		return gpuReading{}, errors.New("unexpected nvidia-smi output")
	}
	return worst, nil
}

// smiPath prefers the driver's install locations on Windows over a PATH
// lookup, so a stray nvidia-smi.exe earlier in PATH is never run.
func smiPath() string {
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		for _, p := range []string{
			filepath.Join(root, "System32", "nvidia-smi.exe"),
			`C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe`,
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return "nvidia-smi"
}
