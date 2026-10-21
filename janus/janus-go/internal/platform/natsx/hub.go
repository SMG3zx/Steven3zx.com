// Package natsx is how a janus-go process reaches NATS: it embeds a JetStream
// server or connects to one, and carries the trace context of every message in
// its headers. See section 4 of docs/architecture.md.
package natsx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// HubDomain is the JetStream domain of the control plane's server. An Agent's
// leaf server uses its own domain, so the two never share streams by accident.
const HubDomain = "hub"

// readyTimeout bounds how long an embedded server may take to accept
// connections.
const readyTimeout = 10 * time.Second

// Config says which NATS server a process uses.
type Config struct {
	// URL is an external server to connect to. When empty, Start embeds one.
	URL string
	// Address is the host:port the embedded server listens on. Port 0 picks a
	// free port. Empty means 127.0.0.1 on a free port.
	Address string
	// StoreDir is where the embedded server keeps JetStream data. Required when
	// URL is empty.
	StoreDir string
	// Name names the connection in the server's monitoring.
	Name string
}

// Hub is a process's connection to NATS and, when embedded, the server itself.
type Hub struct {
	conn   *nats.Conn
	server *server.Server
	closed chan struct{}
}

// Start connects to cfg.URL, or embeds a JetStream server with domain HubDomain
// and connects to that. A failure leaves nothing running.
func Start(ctx context.Context, cfg Config) (*Hub, error) {
	h := &Hub{closed: make(chan struct{})}
	url := cfg.URL
	if url == "" {
		srv, err := embed(cfg)
		if err != nil {
			return nil, err
		}
		h.server = srv
		url = srv.ClientURL()
	}
	options := []nats.Option{
		nats.Name(cfg.Name),
		nats.ClosedHandler(func(*nats.Conn) { close(h.closed) }),
		nats.MaxReconnects(-1),
	}
	if deadline, ok := ctx.Deadline(); ok {
		options = append(options, nats.Timeout(time.Until(deadline)))
	}
	conn, err := nats.Connect(url, options...)
	if err != nil {
		h.stopServer()
		return nil, fmt.Errorf("connect to NATS at %s: %w", url, err)
	}
	h.conn = conn
	return h, nil
}

func embed(cfg Config) (*server.Server, error) {
	if cfg.StoreDir == "" {
		return nil, errors.New("an embedded NATS server needs a JetStream store directory")
	}
	if err := os.MkdirAll(cfg.StoreDir, 0700); err != nil {
		return nil, fmt.Errorf("create NATS store directory: %w", err)
	}
	host, port, err := listenAddress(cfg.Address)
	if err != nil {
		return nil, err
	}
	srv, err := server.NewServer(&server.Options{
		ServerName:      "janus-hub",
		Host:            host,
		Port:            port,
		JetStream:       true,
		JetStreamDomain: HubDomain,
		StoreDir:        cfg.StoreDir,
		NoSigs:          true,
		NoLog:           true,
	})
	if err != nil {
		return nil, fmt.Errorf("configure embedded NATS server: %w", err)
	}
	srv.Start()
	if !srv.ReadyForConnections(readyTimeout) {
		srv.Shutdown()
		srv.WaitForShutdown()
		return nil, fmt.Errorf("embedded NATS server did not accept connections within %s", readyTimeout)
	}
	return srv, nil
}

func listenAddress(address string) (string, int, error) {
	if address == "" {
		return "127.0.0.1", server.RANDOM_PORT, nil
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("NATS listen address %q: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("NATS listen address %q: bad port", address)
	}
	if port == 0 {
		port = server.RANDOM_PORT
	}
	return host, port, nil
}

// Conn is the process's connection. Services share it.
func (h *Hub) Conn() *nats.Conn { return h.conn }

// JetStream returns the JetStream API over the process's connection.
func (h *Hub) JetStream() (jetstream.JetStream, error) { return jetstream.New(h.conn) }

// URL is the server the process is connected to, without credentials.
func (h *Hub) URL() string { return h.conn.ConnectedUrlRedacted() }

// Embedded reports whether the server runs inside this process.
func (h *Hub) Embedded() bool { return h.server != nil }

// Close drains the connection, so messages being handled finish, then stops
// the embedded server. It returns early with ctx's error if ctx ends first;
// the server is stopped either way.
func (h *Hub) Close(ctx context.Context) error {
	defer h.stopServer()
	if err := h.conn.Drain(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		h.conn.Close()
		return fmt.Errorf("drain NATS connection: %w", err)
	}
	select {
	case <-h.closed:
		return nil
	case <-ctx.Done():
		h.conn.Close()
		return ctx.Err()
	}
}

func (h *Hub) stopServer() {
	if h.server == nil {
		return
	}
	h.server.Shutdown()
	h.server.WaitForShutdown()
}
