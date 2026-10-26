package firecracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadyNamesEachMissingPiece(t *testing.T) {
	requireE2fsprogs(t)
	if f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err != nil {
		t.Skip("/dev/kvm is not usable here")
	} else {
		f.Close()
	}
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	if err := os.WriteFile(kernel, []byte("kernel"), 0o600); err != nil {
		t.Fatal(err)
	}
	rust := filepath.Join(dir, "rust.ext4")
	if err := os.WriteFile(rust, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary, lookErr := exec.LookPath("firecracker")
	if lookErr != nil {
		t.Skip("firecracker is not installed")
	}
	host := Host{Binary: binary, Kernel: kernel, WorkDir: filepath.Join(dir, "jobs")}
	err := Ready(context.Background(), host, Rootfs{Rust: rust, Go: filepath.Join(dir, "go.ext4"), JavaScript: ""})
	if err == nil {
		t.Fatal("Ready accepted missing root filesystems")
	}
	for _, want := range []string{"Go WASM root filesystem", "JavaScript/TypeScript WASM root filesystem"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Ready error %q does not name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "Rust WASM") {
		t.Errorf("Ready error %q names the Rust image, which exists", err)
	}
}
