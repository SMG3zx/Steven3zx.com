package firecracker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain turns the test binary into a stand-in for the firecracker executable
// when FAKE_FIRECRACKER is set. "hang" never returns, so a job can be timed out.
// "guest" plays the guest: it writes FAKE_GUEST_STATUS, FAKE_GUEST_STDOUT and
// FAKE_GUEST_STDERR to the output drive named in its configuration, as janus-init
// would, and exits as a rebooted VM does.
func TestMain(m *testing.M) {
	switch os.Getenv("FAKE_FIRECRACKER") {
	case "hang":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "guest":
		os.Exit(fakeGuest(os.Args[len(os.Args)-1]))
	}
	os.Exit(m.Run())
}

func fakeGuest(configPath string) int {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return 1
	}
	var cfg struct {
		Drives []struct {
			DriveID    string `json:"drive_id"`
			PathOnHost string `json:"path_on_host"`
		} `json:"drives"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return 1
	}
	var output string
	for _, drive := range cfg.Drives {
		if drive.DriveID == "output" {
			output = drive.PathOnHost
		}
	}
	dir, err := os.MkdirTemp("", "fake-guest-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir)
	files := map[string]string{
		"status": os.Getenv("FAKE_GUEST_STATUS"),
		"stdout": os.Getenv("FAKE_GUEST_STDOUT"),
		"stderr": os.Getenv("FAKE_GUEST_STDERR"),
	}
	for name, content := range files {
		local := filepath.Join(dir, name)
		if os.WriteFile(local, []byte(content), 0o600) != nil {
			return 1
		}
		if exec.Command("debugfs", "-w", "-R", "write "+local+" "+name, output).Run() != nil {
			return 1
		}
	}
	return 0
}

// requireE2fsprogs skips a test that builds real drives on a host without them.
func requireE2fsprogs(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

// fakeHost returns a Host whose firecracker is this test binary, in the mode set
// by the caller's environment.
func fakeHost(t *testing.T) Host {
	t.Helper()
	requireE2fsprogs(t)
	return Host{Binary: os.Args[0], Kernel: "/unused/vmlinux", WorkDir: t.TempDir()}
}

func workspaceWithGoMod(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func TestExecuteReturnsTheCommandsOutputAndLog(t *testing.T) {
	host := fakeHost(t)
	t.Setenv("FAKE_FIRECRACKER", "guest")
	t.Setenv("FAKE_GUEST_STATUS", "0\n")
	t.Setenv("FAKE_GUEST_STDOUT", "tar bytes")
	t.Setenv("FAKE_GUEST_STDERR", "build log")

	got := host.Execute(context.Background(), Run{Rootfs: "/images/go.ext4", Workspace: workspaceWithGoMod(t), Command: "true", MaxLog: 64 << 10, MaxOutput: 1 << 20})
	if got.Err != nil || string(got.Stdout) != "tar bytes" || string(got.Stderr) != "build log" {
		t.Fatalf("Execute = %q %q %v", got.Stdout, got.Stderr, got.Err)
	}
}

func TestExecuteReportsANonZeroStatusWithItsOutput(t *testing.T) {
	host := fakeHost(t)
	t.Setenv("FAKE_FIRECRACKER", "guest")
	t.Setenv("FAKE_GUEST_STATUS", "3\n")
	t.Setenv("FAKE_GUEST_STDOUT", "partial")
	t.Setenv("FAKE_GUEST_STDERR", "failed")

	got := host.Execute(context.Background(), Run{Rootfs: "/images/go.ext4", Workspace: workspaceWithGoMod(t), Command: "exit 3", MaxLog: 64 << 10, MaxOutput: 1 << 20})
	if got.Err == nil || !strings.Contains(got.Err.Error(), "status 3") {
		t.Fatalf("Execute error = %v, want the status", got.Err)
	}
	if string(got.Stderr) != "failed" {
		t.Fatalf("Stderr = %q, want the failed command's log", got.Stderr)
	}
}

func TestExecuteRefusesOutputOverTheLimit(t *testing.T) {
	host := fakeHost(t)
	t.Setenv("FAKE_FIRECRACKER", "guest")
	t.Setenv("FAKE_GUEST_STATUS", "0\n")
	t.Setenv("FAKE_GUEST_STDOUT", strings.Repeat("x", 100))
	t.Setenv("FAKE_GUEST_STDERR", "")

	got := host.Execute(context.Background(), Run{Rootfs: "/images/go.ext4", Workspace: workspaceWithGoMod(t), Command: "true", MaxLog: 64 << 10, MaxOutput: 10})
	if got.Err == nil || !strings.Contains(got.Err.Error(), "exceeds") {
		t.Fatalf("Execute error = %v, want the output limit", got.Err)
	}
	if len(got.Stdout) != 10 {
		t.Fatalf("kept %d bytes of output, want the 10 the limit allows", len(got.Stdout))
	}
}

func TestExecuteRefusesUntrustedRootfs(t *testing.T) {
	requireE2fsprogs(t)
	for _, rootfs := range []string{"", "builder:local", "relative/rootfs.ext4"} {
		got := Host{}.Execute(context.Background(), Run{Rootfs: rootfs, Workspace: t.TempDir(), Command: "true"})
		if got.Err == nil {
			t.Errorf("accepted rootfs %q", rootfs)
		}
	}
}

func TestExecuteKillsTheVMWhenTheJobEnds(t *testing.T) {
	host := fakeHost(t)
	t.Setenv("FAKE_FIRECRACKER", "hang")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := host.Execute(ctx, Run{Rootfs: "/images/go.ext4", Workspace: workspaceWithGoMod(t), Command: "sleep 600", MaxLog: 64 << 10, MaxOutput: 1 << 20})
	if got.Err == nil {
		t.Fatal("a hung VM returned no error")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("a hung VM held the job for %v", elapsed)
	}
}

func TestVMConfigBootsTheJobsDrives(t *testing.T) {
	d := drives{rootfs: "/images/go.ext4", source: "/job/src.ext4", job: "/job/job.ext4", output: "/job/out.ext4"}
	var cfg struct {
		BootSource struct {
			KernelImagePath string `json:"kernel_image_path"`
			BootArgs        string `json:"boot_args"`
		} `json:"boot-source"`
		Drives []struct {
			DriveID      string `json:"drive_id"`
			PathOnHost   string `json:"path_on_host"`
			IsRootDevice bool   `json:"is_root_device"`
			IsReadOnly   bool   `json:"is_read_only"`
		} `json:"drives"`
	}
	if err := json.Unmarshal(d.config("/images/vmlinux"), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.BootSource.KernelImagePath != "/images/vmlinux" || !strings.Contains(cfg.BootSource.BootArgs, "init=/sbin/janus-init") || !strings.Contains(cfg.BootSource.BootArgs, "reboot=k") {
		t.Fatalf("boot source = %+v", cfg.BootSource)
	}
	if len(cfg.Drives) != 4 {
		t.Fatalf("drives = %d, want rootfs, source, command and output", len(cfg.Drives))
	}
	root := cfg.Drives[0]
	if root.PathOnHost != "/images/go.ext4" || !root.IsRootDevice || !root.IsReadOnly {
		t.Fatalf("rootfs drive = %+v, want the shared image read-only", root)
	}
	if output := cfg.Drives[3]; output.PathOnHost != "/job/out.ext4" || output.IsReadOnly {
		t.Fatalf("output drive = %+v, want it writable", output)
	}
}

func TestSourceDriveHoldsTheWorkspace(t *testing.T) {
	requireE2fsprogs(t)
	workspace := workspaceWithGoMod(t)
	dir := t.TempDir()
	d, err := prepareDrives(context.Background(), dir, Run{Rootfs: "/images/go.ext4", Workspace: workspace, Command: "true"}, 64<<10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copied-go.mod")
	if err := extract(context.Background(), d.source, "go.mod", dst); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "module example.com/app\n" {
		t.Fatalf("go.mod on the source drive = %q, %v", data, err)
	}
}
