package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Sentinel errors, mapped to HTTP status codes by the API layer.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid")
)

// DefaultBootArgs is the kernel command line used when a Spec leaves BootArgs empty.
const DefaultBootArgs = "console=ttyS0 reboot=k panic=1 pci=off"

const (
	maxVCPUs     = 32
	minMemoryMiB = 128
	maxMemoryMiB = 32768
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Spec is the user-supplied definition of a microVM.
type Spec struct {
	Name       string `json:"name"`
	KernelPath string `json:"kernelPath"`
	RootfsPath string `json:"rootfsPath"`
	VCPUs      int    `json:"vcpus"`
	MemoryMiB  int    `json:"memoryMiB"`
	BootArgs   string `json:"bootArgs"`
}

func (s *Spec) applyDefaults() {
	if s.BootArgs == "" {
		s.BootArgs = DefaultBootArgs
	}
}

func (s Spec) validate() error {
	if !namePattern.MatchString(s.Name) {
		return invalidf("name must be 1-63 lowercase letters, digits or hyphens, not starting with a hyphen")
	}
	if err := checkFile(s.KernelPath, "kernelPath"); err != nil {
		return err
	}
	if err := checkFile(s.RootfsPath, "rootfsPath"); err != nil {
		return err
	}
	if s.VCPUs < 1 || s.VCPUs > maxVCPUs {
		return invalidf("vcpus must be between 1 and %d", maxVCPUs)
	}
	if s.MemoryMiB < minMemoryMiB || s.MemoryMiB > maxMemoryMiB {
		return invalidf("memoryMiB must be between %d and %d", minMemoryMiB, maxMemoryMiB)
	}
	return nil
}

func checkFile(path, field string) error {
	if path == "" {
		return invalidf("%s is required", field)
	}
	if !filepath.IsAbs(path) {
		return invalidf("%s must be an absolute path", field)
	}
	info, err := os.Stat(path)
	if err != nil {
		return invalidf("%s: %v", field, err)
	}
	if !info.Mode().IsRegular() {
		return invalidf("%s must be a regular file", field)
	}
	return nil
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
