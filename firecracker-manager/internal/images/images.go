// Package images lists the kernel images and root filesystems available on the
// host, so the UI can offer them as suggestions.
package images

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Image is one file found in the images directory.
type Image struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Catalog is what the UI offers as dropdown suggestions. The UI still accepts
// any path or boot-args string the user types.
type Catalog struct {
	// Dir is the images directory that was scanned.
	Dir      string   `json:"dir"`
	Kernels  []Image  `json:"kernels"`
	RootFS   []Image  `json:"rootfs"`
	BootArgs []string `json:"bootArgs"`
}

// BootArgPresets are common kernel command lines for Firecracker guests.
var BootArgPresets = []string{
	"console=ttyS0 reboot=k panic=1 pci=off",
	"console=ttyS0 reboot=k panic=1 pci=off init=/init",
	"console=ttyS0 reboot=k panic=1 pci=off quiet",
}

// Scan reads dir/kernels and dir/rootfs. Each regular file (symlinks followed)
// is one image. Missing subdirectories are not an error: they just yield no entries.
func Scan(dir string) (Catalog, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Catalog{}, err
	}
	kernels, err := listFiles(filepath.Join(abs, "kernels"))
	if err != nil {
		return Catalog{}, err
	}
	rootfs, err := listFiles(filepath.Join(abs, "rootfs"))
	if err != nil {
		return Catalog{}, err
	}
	return Catalog{
		Dir:      abs,
		Kernels:  kernels,
		RootFS:   rootfs,
		BootArgs: append([]string(nil), BootArgPresets...),
	}, nil
}

func listFiles(dir string) ([]Image, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Image{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Image{}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, Image{Name: e.Name(), Path: path, Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
