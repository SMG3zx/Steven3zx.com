package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// A job's disks are ext4 images made with e2fsprogs. Building them needs no
// root, and the guest mounts each one as a block device. The guest sees them as
// /dev/vda (the shared root filesystem, read-only), /dev/vdb (the source,
// read-only), /dev/vdc (the command) and /dev/vdd (the output, read-write).

const (
	mib = 1 << 20
	// driveSlack is spare room in each image for ext4's own metadata and for the
	// build's files, which are copied into /workspace in the guest.
	driveSlack = 64 * mib
	// commandDriveMiB holds the command script and nothing else.
	commandDriveMiB = 8
	// outputSlackMiB is spare room in the output image beyond the log and the
	// output stream.
	outputSlackMiB = 16
)

// drives are the disk images of one job.
type drives struct {
	rootfs, source, job, output string
}

// prepareDrives writes the disk images of msg into dir.
func prepareDrives(ctx context.Context, dir string, msg Run, maxLog, maxOutput int) (drives, error) {
	d := drives{
		rootfs: msg.Rootfs,
		source: filepath.Join(dir, "src.ext4"),
		job:    filepath.Join(dir, "job.ext4"),
		output: filepath.Join(dir, "out.ext4"),
	}
	workspaceBytes, err := treeBytes(msg.Workspace)
	if err != nil {
		return drives{}, fmt.Errorf("measure the microVM workspace: %w", err)
	}
	if err := makeDrive(ctx, d.source, msg.Workspace, roundMiB(workspaceBytes+driveSlack)); err != nil {
		return drives{}, fmt.Errorf("build the microVM source drive: %w", err)
	}

	commandDir := filepath.Join(dir, "command")
	if err := os.Mkdir(commandDir, 0o700); err != nil {
		return drives{}, fmt.Errorf("create the microVM command directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(commandDir, "command.sh"), []byte(msg.Command+"\n"), 0o600); err != nil {
		return drives{}, fmt.Errorf("write the microVM command: %w", err)
	}
	if err := makeDrive(ctx, d.job, commandDir, commandDriveMiB); err != nil {
		return drives{}, fmt.Errorf("build the microVM command drive: %w", err)
	}

	outputMiB := int64((maxLog+maxOutput)/mib) + outputSlackMiB
	if err := makeEmptyDrive(ctx, d.output, outputMiB); err != nil {
		return drives{}, fmt.Errorf("build the microVM output drive: %w", err)
	}
	return d, nil
}

// config returns the Firecracker configuration file that boots kernel with
// these drives. The guest runs janus-init as PID 1 and reboots when it is done,
// which with reboot=k makes Firecracker exit.
func (d drives) config(kernel string) []byte {
	type drive struct {
		DriveID      string `json:"drive_id"`
		PathOnHost   string `json:"path_on_host"`
		IsRootDevice bool   `json:"is_root_device"`
		IsReadOnly   bool   `json:"is_read_only"`
	}
	cfg := struct {
		BootSource struct {
			KernelImagePath string `json:"kernel_image_path"`
			BootArgs        string `json:"boot_args"`
		} `json:"boot-source"`
		Drives        []drive `json:"drives"`
		MachineConfig struct {
			VCPUCount  int  `json:"vcpu_count"`
			MemSizeMiB int  `json:"mem_size_mib"`
			SMT        bool `json:"smt"`
		} `json:"machine-config"`
	}{}
	cfg.BootSource.KernelImagePath = kernel
	cfg.BootSource.BootArgs = "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda ro init=/sbin/janus-init"
	cfg.Drives = []drive{
		{DriveID: "rootfs", PathOnHost: d.rootfs, IsRootDevice: true, IsReadOnly: true},
		{DriveID: "source", PathOnHost: d.source, IsReadOnly: true},
		{DriveID: "command", PathOnHost: d.job, IsReadOnly: true},
		{DriveID: "output", PathOnHost: d.output},
	}
	cfg.MachineConfig.VCPUCount = 2
	cfg.MachineConfig.MemSizeMiB = 2048
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return data
}

// makeDrive writes an ext4 image of sizeMiB holding the contents of dir.
func makeDrive(ctx context.Context, image, dir string, sizeMiB int64) error {
	return runE2fs(ctx, "mkfs.ext4", "-q", "-F", "-d", dir, image, sizeArg(sizeMiB))
}

// makeEmptyDrive writes an empty ext4 image of sizeMiB.
func makeEmptyDrive(ctx context.Context, image string, sizeMiB int64) error {
	return runE2fs(ctx, "mkfs.ext4", "-q", "-F", image, sizeArg(sizeMiB))
}

// extract copies the file name from the root of the image to dst.
func extract(ctx context.Context, image, name, dst string) error {
	if err := runE2fs(ctx, "debugfs", "-R", "dump /"+name+" "+dst, image); err != nil {
		return fmt.Errorf("read %s from the microVM: %w", name, err)
	}
	if _, err := os.Stat(dst); err != nil {
		return fmt.Errorf("the microVM wrote no %s", name)
	}
	return nil
}

func runE2fs(ctx context.Context, tool string, args ...string) error {
	output, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", tool, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func sizeArg(sizeMiB int64) string { return strconv.FormatInt(sizeMiB, 10) + "M" }

// treeBytes estimates the ext4 space the regular files under root need: each
// file rounded up to a 4 KiB block, plus one block for its directory entry.
func treeBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() && !entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		blocks := (info.Size() + 4095) / 4096
		total += (blocks + 1) * 4096
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// roundMiB returns bytes in whole MiB, rounded up.
func roundMiB(bytes int64) int64 { return (bytes + mib - 1) / mib }
