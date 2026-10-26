package images

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanListsFilesAndIgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "kernels", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "rootfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"kernels/vmlinux-b", "kernels/vmlinux-a", "rootfs/ubuntu.ext4"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	c, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Kernels) != 2 || c.Kernels[0].Name != "vmlinux-a" || c.Kernels[1].Name != "vmlinux-b" {
		t.Errorf("kernels = %+v, want sorted vmlinux-a, vmlinux-b", c.Kernels)
	}
	if len(c.RootFS) != 1 || c.RootFS[0].Size != 4 {
		t.Errorf("rootfs = %+v", c.RootFS)
	}
	if len(c.BootArgs) != len(BootArgPresets) {
		t.Errorf("boot args = %v", c.BootArgs)
	}
}

func TestScanMissingDirectoriesIsEmpty(t *testing.T) {
	c, err := Scan(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.Kernels) != 0 || len(c.RootFS) != 0 {
		t.Errorf("catalog = %+v, want empty", c)
	}
}
