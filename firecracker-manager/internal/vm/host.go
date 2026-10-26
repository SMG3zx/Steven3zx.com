package vm

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// HostInfo describes whether this machine can run Firecracker.
type HostInfo struct {
	KVM            bool   `json:"kvm"`
	KVMError       string `json:"kvmError,omitempty"`
	FirecrackerBin string `json:"firecrackerBin"`
	Firecracker    string `json:"firecracker,omitempty"`
	FirecrackerErr string `json:"firecrackerError,omitempty"`
}

// Host checks KVM access and the Firecracker binary at fcBin.
func Host(ctx context.Context, fcBin string) HostInfo {
	info := HostInfo{FirecrackerBin: fcBin}

	if f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err != nil {
		info.KVMError = err.Error()
	} else {
		info.KVM = true
		_ = f.Close()
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, fcBin, "--version").Output()
	if err != nil {
		info.FirecrackerErr = err.Error()
		return info
	}
	firstLine, _, _ := strings.Cut(string(out), "\n")
	info.Firecracker = strings.TrimSpace(firstLine)
	return info
}
