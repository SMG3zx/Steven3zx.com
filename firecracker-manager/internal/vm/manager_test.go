package vm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// testImages returns paths to a kernel and rootfs file that pass validation.
func testImages(t *testing.T) (kernel, rootfs string) {
	t.Helper()
	dir := t.TempDir()
	kernel = filepath.Join(dir, "vmlinux")
	rootfs = filepath.Join(dir, "rootfs.ext4")
	for _, p := range []string{kernel, rootfs} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return kernel, rootfs
}

func newTestManager(t *testing.T, dataDir, fcBin string) *Manager {
	t.Helper()
	m, err := NewManager(Options{DataDir: dataDir, FirecrackerBin: fcBin})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func validSpec(t *testing.T, name string) Spec {
	t.Helper()
	kernel, rootfs := testImages(t)
	return Spec{Name: name, KernelPath: kernel, RootfsPath: rootfs, VCPUs: 2, MemoryMiB: 256}
}

func TestCreatePersistsAndReloads(t *testing.T) {
	dataDir := t.TempDir()
	m := newTestManager(t, dataDir, "firecracker")

	created, err := m.Create(validSpec(t, "web-1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.State != StateStopped {
		t.Errorf("state = %s, want stopped", created.State)
	}
	if created.Spec.BootArgs != DefaultBootArgs {
		t.Errorf("boot args = %q, want default", created.Spec.BootArgs)
	}

	reloaded := newTestManager(t, dataDir, "firecracker")
	got, err := reloaded.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.Spec.Name != "web-1" {
		t.Errorf("name after reload = %q", got.Spec.Name)
	}
}

func TestCreateRejectsInvalidSpecs(t *testing.T) {
	m := newTestManager(t, t.TempDir(), "firecracker")
	good := validSpec(t, "ok")

	cases := map[string]func(*Spec){
		"bad name":        func(s *Spec) { s.Name = "Has Spaces" },
		"leading hyphen":  func(s *Spec) { s.Name = "-x" },
		"relative kernel": func(s *Spec) { s.KernelPath = "vmlinux" },
		"missing rootfs":  func(s *Spec) { s.RootfsPath = "/does/not/exist" },
		"zero vcpus":      func(s *Spec) { s.VCPUs = 0 },
		"too many vcpus":  func(s *Spec) { s.VCPUs = 64 },
		"tiny memory":     func(s *Spec) { s.MemoryMiB = 16 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec := good
			mutate(&spec)
			_, err := m.Create(spec)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCreateRejectsDuplicateName(t *testing.T) {
	m := newTestManager(t, t.TempDir(), "firecracker")
	if _, err := m.Create(validSpec(t, "dup")); err != nil {
		t.Fatal(err)
	}
	_, err := m.Create(validSpec(t, "dup"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestStartFailureRecordsErrorAndAllowsDelete(t *testing.T) {
	m := newTestManager(t, t.TempDir(), filepath.Join(t.TempDir(), "no-such-firecracker"))
	created, err := m.Create(validSpec(t, "broken"))
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Start(created.ID); err == nil {
		t.Fatal("Start with missing binary succeeded")
	}
	got, _ := m.Get(created.ID)
	if got.State != StateError || got.LastError == "" {
		t.Fatalf("state = %s, lastError = %q; want error with message", got.State, got.LastError)
	}

	if err := m.Delete(created.ID); err != nil {
		t.Fatalf("Delete after failed start: %v", err)
	}
	if _, err := m.Get(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

func TestOperationsOnUnknownVM(t *testing.T) {
	m := newTestManager(t, t.TempDir(), "firecracker")
	if err := m.Start("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Start: err = %v, want ErrNotFound", err)
	}
	if err := m.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: err = %v, want ErrNotFound", err)
	}
}

func TestStopRequiresRunningVM(t *testing.T) {
	m := newTestManager(t, t.TempDir(), "firecracker")
	created, err := m.Create(validSpec(t, "idle"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(created.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("Stop on stopped VM: err = %v, want ErrConflict", err)
	}
	if err := m.Pause(created.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("Pause on stopped VM: err = %v, want ErrConflict", err)
	}
}
