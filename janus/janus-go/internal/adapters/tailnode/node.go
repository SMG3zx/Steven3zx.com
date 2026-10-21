// Package tailnode puts a Janus process on the tailnet as a node of its own,
// through tsnet: no Tailscale install, no system network change. The control
// plane and every Agent each run one. A node listens and dials only on the
// tailnet, so nothing it serves is reachable from the internet.
package tailnode

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"tailscale.com/tsnet"
)

// Config says how a node joins. Dir holds its keys and state between runs.
// AuthKey is needed only for the first join; afterwards the state in Dir is
// enough. ControlURL is the Headscale server.
type Config struct {
	Dir, Hostname, ControlURL, AuthKey string
	// Logf receives tsnet's own log lines; nil discards them.
	Logf func(format string, args ...any)
}

// Node is a running tailnet node.
type Node struct{ server *tsnet.Server }

// Joined reports whether dir already holds the state of a node that joined,
// so starting it needs no key.
func Joined(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "tailscaled.state"))
	return err == nil
}

// Start brings a node up on the tailnet and returns once it is connected or
// ctx ends.
func Start(ctx context.Context, config Config) (*Node, error) {
	if err := os.MkdirAll(config.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create the tailnet state directory: %w", err)
	}
	logf := config.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	server := &tsnet.Server{Dir: config.Dir, Hostname: config.Hostname, ControlURL: config.ControlURL,
		AuthKey: config.AuthKey, Logf: logf, UserLogf: logf}
	if _, err := server.Up(ctx); err != nil {
		server.Close()
		return nil, fmt.Errorf("join the tailnet: %w", err)
	}
	return &Node{server: server}, nil
}

// Listen accepts TCP connections on port of the node's tailnet addresses only.
func (n *Node) Listen(port int) (net.Listener, error) {
	return n.server.Listen("tcp", ":"+strconv.Itoa(port))
}

// Dial opens a TCP connection to address, "host:port", over the tailnet.
func (n *Node) Dial(ctx context.Context, address string) (net.Conn, error) {
	return n.server.Dial(ctx, "tcp", address)
}

// HTTPClient returns a client whose requests go over the tailnet.
func (n *Node) HTTPClient() *http.Client { return n.server.HTTPClient() }

// RequireTag returns a check that accepts a caller only when the tailnet says
// the node at remoteAddr carries tag.
func (n *Node) RequireTag(tag string) func(ctx context.Context, remoteAddr string) error {
	return func(ctx context.Context, remoteAddr string) error {
		local, err := n.server.LocalClient()
		if err != nil {
			return err
		}
		who, err := local.WhoIs(ctx, remoteAddr)
		if err != nil {
			return fmt.Errorf("identify the caller: %w", err)
		}
		if who.Node == nil || !slices.Contains(who.Node.Tags, tag) {
			return errors.New("the caller does not carry " + tag)
		}
		return nil
	}
}

// Close takes the node off the tailnet. Its state stays in Dir.
func (n *Node) Close() error { return n.server.Close() }
