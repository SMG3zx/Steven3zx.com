package wasmrun

import (
	"errors"
	"fmt"
	"time"
)

// A capability is something a module may only do when the host grants it: the
// host registers the functions for a granted capability on the linker and nothing
// else, so a module that imports an ungranted one never starts.
//
// Each capability lives in an import module whose name carries a version. Renaming
// one breaks every module built against it.
const (
	// CapabilityRawNet is capture of raw frames from named network interfaces.
	CapabilityRawNet = "raw-net"

	rawNetModule = "janus_net_v1"
)

// capabilityModules maps the import modules the host provides to the capability
// that grants them.
var capabilityModules = map[string]string{rawNetModule: CapabilityRawNet}

// Grant allows one capability to a run. For CapabilityRawNet, Interfaces names the
// interfaces the module may capture from; an empty list allows none.
type Grant struct {
	Capability string
	Interfaces []string
}

// AuditFunc receives what a capability did, such as a raw capture being opened or
// refused. It runs on the module's goroutine, so it must not block.
type AuditFunc func(event string, attrs map[string]string)

// ErrCapabilityNotGranted is what the error of a run matches when its module imports
// a capability that was not granted.
var ErrCapabilityNotGranted = errors.New("the module needs a capability that was not granted")

// CapabilityError reports which capability a module needs. It matches
// ErrCapabilityNotGranted.
type CapabilityError struct {
	Capability string
	Module     string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("the module imports %s and needs the %s capability, which was not granted", e.Module, e.Capability)
}

func (e *CapabilityError) Is(target error) bool { return target == ErrCapabilityNotGranted }

// ErrRawNetUnavailable is what opening a packet source returns where this host
// cannot capture raw frames.
var ErrRawNetUnavailable = errors.New("raw packet capture is not available on this host")

// PacketSource is an open capture on one interface. It is the seam between the
// host functions and the operating system: a privileged helper that hands over
// an already-open socket can provide it without the host functions changing.
type PacketSource interface {
	// Recv waits up to timeout for one frame and copies it into buf. It returns 0, nil
	// when the wait ends with no frame.
	Recv(buf []byte, timeout time.Duration) (int, error)
	Close() error
}

// PacketOpener opens a capture on the named interface.
type PacketOpener func(iface string) (PacketSource, error)

// granted reports whether grants include the capability, and the interfaces they allow.
func granted(grants []Grant, capability string) (interfaces map[string]bool, ok bool) {
	interfaces = make(map[string]bool)
	for _, grant := range grants {
		if grant.Capability != capability {
			continue
		}
		ok = true
		for _, name := range grant.Interfaces {
			interfaces[name] = true
		}
	}
	return interfaces, ok
}
