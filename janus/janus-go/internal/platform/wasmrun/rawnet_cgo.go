//go:build cgo

package wasmrun

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	wasmtime "github.com/bytecodealliance/wasmtime-go/v49"
)

// The host functions of janus_net_v1. They return a value >= 0 on success and one
// of these negative codes on failure, so a guest can tell a refusal from a fault.
const (
	rawOK          = 0
	rawBadArgs     = -1
	rawNotAllowed  = -2
	rawUnavailable = -3
	rawTooMany     = -4
	rawFailed      = -5
	rawBadHandle   = -6
	rawInterrupted = -7

	maxRawHandles = 4
	maxRawFrame   = 64 << 10
	maxIfaceName  = 64
	// recvSlice bounds how long one call blocks in the kernel, so the run's deadline
	// and cancellation are seen even by a guest that waits for a frame that never comes.
	recvSlice = 50 * time.Millisecond
)

// rawNetHost serves the raw-net capability to one run.
type rawNetHost struct {
	allowed map[string]bool
	open    PacketOpener
	audit   AuditFunc
	stop    <-chan struct{}

	// interrupted is set when a wait was cut short by the run ending.
	interrupted atomic.Bool

	mu      sync.Mutex
	next    int32
	sources map[int32]*capture
}

type capture struct {
	iface         string
	source        PacketSource
	frames, bytes int
}

// registerRawNet defines the janus_net_v1 functions on the linker.
func registerRawNet(linker *wasmtime.Linker, interfaces map[string]bool, open PacketOpener, audit AuditFunc, stop <-chan struct{}) (*rawNetHost, error) {
	if open == nil {
		open = openPacketSource
	}
	host := &rawNetHost{allowed: interfaces, open: open, audit: audit, stop: stop, next: 1, sources: make(map[int32]*capture)}
	if err := linker.FuncWrap(rawNetModule, "raw_open", host.rawOpen); err != nil {
		return nil, err
	}
	if err := linker.FuncWrap(rawNetModule, "raw_recv", host.rawRecv); err != nil {
		return nil, err
	}
	if err := linker.FuncWrap(rawNetModule, "raw_close", host.rawClose); err != nil {
		return nil, err
	}
	return host, nil
}

func (h *rawNetHost) emit(event string, attrs map[string]string) {
	if h.audit != nil {
		h.audit(event, attrs)
	}
}

// guestBytes returns a copy of n bytes of the guest's memory at ptr. The pointer and
// length come from the guest, so both are treated as unsigned and checked against the
// memory without overflow. The copy matters: memory the runtime hands out is only
// valid until the guest grows it.
func guestBytes(caller *wasmtime.Caller, ptr, n int32) ([]byte, bool) {
	memory := guestMemory(caller)
	if memory == nil {
		return nil, false
	}
	data := memory.UnsafeData(caller)
	start, length := uint64(uint32(ptr)), uint64(uint32(n))
	if start+length > uint64(len(data)) {
		return nil, false
	}
	return append([]byte(nil), data[start:start+length]...), true
}

// putGuestBytes writes b into the guest's memory at ptr, reporting whether it fit.
func putGuestBytes(caller *wasmtime.Caller, ptr int32, b []byte) bool {
	memory := guestMemory(caller)
	if memory == nil {
		return false
	}
	data := memory.UnsafeData(caller)
	start := uint64(uint32(ptr))
	if start+uint64(len(b)) > uint64(len(data)) {
		return false
	}
	copy(data[start:], b)
	return true
}

func guestMemory(caller *wasmtime.Caller) *wasmtime.Memory {
	export := caller.GetExport("memory")
	if export == nil {
		return nil
	}
	return export.Memory()
}

// rawOpen is raw_open(name_ptr, name_len) -> handle or a negative code.
func (h *rawNetHost) rawOpen(caller *wasmtime.Caller, ptr, n int32) int32 {
	if n <= 0 || n > maxIfaceName {
		return rawBadArgs
	}
	raw, ok := guestBytes(caller, ptr, n)
	if !ok {
		return rawBadArgs
	}
	iface := string(raw)
	if !h.allowed[iface] {
		h.emit("raw_net.open", map[string]string{"interface": iface, "result": "denied"})
		return rawNotAllowed
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sources) >= maxRawHandles {
		return rawTooMany
	}
	source, err := h.open(iface)
	if err != nil {
		h.emit("raw_net.open", map[string]string{"interface": iface, "result": "error", "error": err.Error()})
		return rawUnavailable
	}
	handle := h.next
	h.next++
	h.sources[handle] = &capture{iface: iface, source: source}
	h.emit("raw_net.open", map[string]string{"interface": iface, "result": "ok"})
	return handle
}

// rawRecv is raw_recv(handle, buf_ptr, buf_len, timeout_ms) -> frame length, 0 when the
// wait ended with no frame, or a negative code. A frame longer than buf_len is cut.
func (h *rawNetHost) rawRecv(caller *wasmtime.Caller, handle, ptr, n, timeoutMS int32) int32 {
	if n <= 0 || timeoutMS < 0 {
		return rawBadArgs
	}
	if n > maxRawFrame {
		n = maxRawFrame
	}
	h.mu.Lock()
	c, ok := h.sources[handle]
	h.mu.Unlock()
	if !ok {
		return rawBadHandle
	}
	// Check the destination before waiting, so a bad pointer fails at once.
	if _, ok := guestBytes(caller, ptr, 0); !ok {
		return rawBadArgs
	}
	buf := make([]byte, n)
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		select {
		case <-h.stop:
			h.interrupted.Store(true)
			return rawInterrupted
		default:
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return 0
		}
		if wait > recvSlice {
			wait = recvSlice
		}
		got, err := c.source.Recv(buf, wait)
		if err != nil {
			return rawFailed
		}
		if got > 0 {
			if !putGuestBytes(caller, ptr, buf[:got]) {
				return rawBadArgs
			}
			c.frames++
			c.bytes += got
			return int32(got)
		}
	}
}

// rawClose is raw_close(handle).
func (h *rawNetHost) rawClose(_ *wasmtime.Caller, handle int32) int32 {
	h.mu.Lock()
	c, ok := h.sources[handle]
	delete(h.sources, handle)
	h.mu.Unlock()
	if !ok {
		return rawBadHandle
	}
	h.finish(c)
	return rawOK
}

func (h *rawNetHost) finish(c *capture) {
	_ = c.source.Close()
	h.emit("raw_net.close", map[string]string{"interface": c.iface, "frames": strconv.Itoa(c.frames), "bytes": strconv.Itoa(c.bytes)})
}

// Close ends every capture the module left open.
func (h *rawNetHost) Close() {
	h.mu.Lock()
	open := h.sources
	h.sources = make(map[int32]*capture)
	h.mu.Unlock()
	for _, c := range open {
		h.finish(c)
	}
}
