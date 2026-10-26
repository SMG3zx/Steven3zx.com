//go:build linux

package wasmrun

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"
)

// openPacketSource opens an AF_PACKET socket on iface for receiving. It needs
// CAP_NET_RAW, so an agent either has that capability or gets the socket from a
// privileged helper through a PacketOpener of its own. Nothing here can send: the
// host exposes no function that writes to the socket.
func openPacketSource(iface string) (PacketSource, error) {
	found, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, fmt.Errorf("find interface %q: %w", iface, err)
	}
	protocol := htons(syscall.ETH_P_ALL)
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, int(protocol))
	if err != nil {
		return nil, fmt.Errorf("open a packet socket (needs CAP_NET_RAW): %w", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: protocol, Ifindex: found.Index}); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("bind to %q: %w", iface, err)
	}
	return &packetSocket{fd: fd}, nil
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

type packetSocket struct{ fd int }

func (p *packetSocket) Recv(buf []byte, timeout time.Duration) (int, error) {
	tv := syscall.NsecToTimeval(timeout.Nanoseconds())
	if err := syscall.SetsockoptTimeval(p.fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		return 0, err
	}
	n, _, err := syscall.Recvfrom(p.fd, buf, 0)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EINTR):
		return 0, nil
	default:
		return 0, err
	}
}

func (p *packetSocket) Close() error { return syscall.Close(p.fd) }
