// Package leafbridge carries an Agent's leaf connection to the hub over the
// tailnet. The hub's NATS server and the Agent's each speak plain TCP to a
// loopback address, and two bridges carry the bytes between them: the hub's
// listens on its tailnet node and copies each connection to the hub's leaf port,
// and the Agent's listens on loopback and copies each connection over its own
// tailnet node to the hub. See docs/architecture.md section 6.
package leafbridge

import (
	"io"
	"net"
	"sync"
)

// ServeHub copies every connection accepted on ln to the hub's leaf port at
// leafAddress, the host:port the embedded server takes leaf connections on.
func ServeHub(ln net.Listener, leafAddress string) *Bridge {
	return NewBridge(ln, func() (net.Conn, error) {
		return net.Dial("tcp", leafAddress)
	})
}

// DialHub listens on a loopback port and copies each connection to the hub
// through dial, which reaches the hub over the tailnet. It returns the bridge and
// the loopback address the Agent's leaf remote dials.
func DialHub(dial func() (net.Conn, error)) (*Bridge, string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	return NewBridge(ln, dial), ln.Addr().String(), nil
}

// Bridge accepts connections on a listener and copies each one to a fresh
// connection from dial. SetUp(false) makes the link go down: new connections
// are refused at once and the ones in flight are cut, which is how the test
// cuts an Agent off from the hub.
type Bridge struct {
	ln   net.Listener
	dial func() (net.Conn, error)

	mu    sync.Mutex
	up    bool
	conns map[net.Conn]struct{}
	wg    sync.WaitGroup
}

// NewBridge starts copying connections from ln to dial. The link starts up.
func NewBridge(ln net.Listener, dial func() (net.Conn, error)) *Bridge {
	b := &Bridge{ln: ln, dial: dial, up: true, conns: map[net.Conn]struct{}{}}
	b.wg.Add(1)
	go b.accept()
	return b
}

// SetUp brings the link up or down.
func (b *Bridge) SetUp(up bool) {
	b.mu.Lock()
	b.up = up
	if !up {
		for c := range b.conns {
			c.Close()
		}
	}
	b.mu.Unlock()
}

// Close stops accepting and cuts everything in flight.
func (b *Bridge) Close() {
	b.ln.Close()
	b.SetUp(false)
	b.wg.Wait()
}

func (b *Bridge) accept() {
	defer b.wg.Done()
	for {
		c, err := b.ln.Accept()
		if err != nil {
			return
		}
		b.mu.Lock()
		if !b.up {
			b.mu.Unlock()
			c.Close()
			continue
		}
		b.conns[c] = struct{}{}
		b.mu.Unlock()
		go b.pipe(c)
	}
}

func (b *Bridge) pipe(in net.Conn) {
	out, err := b.dial()
	if err != nil {
		b.forget(in)
		in.Close()
		return
	}
	b.mu.Lock()
	if !b.up {
		b.mu.Unlock()
		in.Close()
		out.Close()
		b.forget(in)
		return
	}
	b.conns[out] = struct{}{}
	b.mu.Unlock()

	done := make(chan struct{}, 2)
	go func() { io.Copy(out, in); done <- struct{}{} }()
	go func() { io.Copy(in, out); done <- struct{}{} }()
	<-done
	in.Close()
	out.Close()
	<-done
	b.forget(in)
	b.forget(out)
}

func (b *Bridge) forget(c net.Conn) {
	b.mu.Lock()
	delete(b.conns, c)
	b.mu.Unlock()
}
