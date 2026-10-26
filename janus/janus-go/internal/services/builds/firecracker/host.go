package firecracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Ready checks that this host can boot the microVMs of the builds: KVM is
// usable by this process, the Firecracker binary and the guest kernel exist,
// e2fsprogs is installed for the drives, and each Language's root filesystem is
// built. Every problem is reported, not just the first. Nothing is built here;
// the root filesystems come from build/firecracker/build-rootfs.sh.
func Ready(ctx context.Context, host Host, rootfs Rootfs) error {
	var errs []error
	if kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err != nil {
		errs = append(errs, fmt.Errorf("open /dev/kvm: %w", err))
	} else {
		kvm.Close()
	}
	if output, err := exec.CommandContext(ctx, host.Binary, "--version").CombinedOutput(); err != nil {
		errs = append(errs, fmt.Errorf("run Firecracker %q: %w: %s", host.Binary, err, strings.TrimSpace(string(output))))
	}
	if err := isFile(host.Kernel); err != nil {
		errs = append(errs, fmt.Errorf("guest kernel: %w", err))
	}
	for _, tool := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			errs = append(errs, fmt.Errorf("%s is required to build the microVM drives: %w", tool, err))
		}
	}
	if err := os.MkdirAll(host.WorkDir, 0o700); err != nil {
		errs = append(errs, fmt.Errorf("create the microVM work directory: %w", err))
	}
	for _, image := range []struct{ name, path string }{
		{"Rust", rootfs.Rust}, {"Go", rootfs.Go}, {"JavaScript/TypeScript", rootfs.JavaScript},
	} {
		if err := isFile(image.path); err != nil {
			errs = append(errs, fmt.Errorf("%s WASM root filesystem: %w", image.name, err))
		}
	}
	return errors.Join(errs...)
}

func isFile(path string) error {
	if path == "" {
		return errors.New("path is not configured")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}
