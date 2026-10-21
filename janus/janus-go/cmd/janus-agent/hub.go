package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"janus/internal/adapters/tailnode"
	contractfleet "janus/internal/contract/fleet"
	"janus/internal/platform/leafbridge"
)

// hubNodeName is the hostname of the control plane's tailnet node.
const hubNodeName = "janus-control"

// hubWait bounds how long an Agent waits for the control plane's node to show on
// the tailnet when it starts.
const hubWait = 2 * time.Minute

// agentHub is the Agent's own NATS server, joined to the hub as a leaf, and the
// connection its own code uses to reach the server.
type agentHub struct {
	bridge *leafbridge.Bridge
	server *server.Server
	conn   *nats.Conn
}

// joinHub starts the Agent's NATS server as a leaf of the hub. The leaf reaches
// the hub through a bridge over the Agent's tailnet node, and the server trusts
// the accounts the enrollment gave it, so the leaf is the Agent's own account
// and not the global one. It returns once the server is up; the leaf joins the
// hub on its own when the hub is reachable.
func joinHub(ctx context.Context, state string, s settings, node *tailnode.Node) (*agentHub, error) {
	if s.Trust == nil {
		return nil, errors.New("this Agent has no NATS trust; enroll it again with a control plane that issues credentials")
	}
	hubIP, err := waitForHub(ctx, node)
	if err != nil {
		return nil, err
	}
	hubAddress := net.JoinHostPort(hubIP, strconv.Itoa(contractfleet.AgentLeafPort))
	bridge, leafAddress, err := leafbridge.DialHub(func() (net.Conn, error) {
		dialCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		return node.Dial(dialCtx, hubAddress)
	})
	if err != nil {
		return nil, fmt.Errorf("listen for the leaf: %w", err)
	}
	srv, err := startServer(state, s, leafAddress)
	if err != nil {
		bridge.Close()
		return nil, err
	}
	// The Agent's own clients reach the server in process, so the Agent listens
	// on no host port. Their inboxes sit under the Agent's ID, which is all the
	// leaf may receive.
	conn, err := nats.Connect(nats.DefaultURL, nats.InProcessServer(srv), nats.UserCredentials(credsPath(state, localCreds)),
		nats.Name("janus-agent"), nats.CustomInboxPrefix("_INBOX."+s.AgentID))
	if err != nil {
		srv.Shutdown()
		bridge.Close()
		return nil, fmt.Errorf("connect to the Agent's server: %w", err)
	}
	return &agentHub{bridge: bridge, server: srv, conn: conn}, nil
}

// Close takes the Agent's connection, server and bridge down.
func (h *agentHub) Close() {
	h.conn.Close()
	h.server.Shutdown()
	h.server.WaitForShutdown()
	h.bridge.Close()
}

// waitForHub returns the IPv4 address of the control plane's node, waiting for
// it to show on the tailnet for up to hubWait.
func waitForHub(ctx context.Context, node *tailnode.Node) (string, error) {
	deadline := time.Now().Add(hubWait)
	for {
		address, err := node.PeerAddress(ctx, hubNodeName)
		if err == nil {
			return address, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return "", fmt.Errorf("the control plane is not on the tailnet: %w", err)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// startServer runs the Agent's server: it trusts the operator and the accounts of
// the enrollment, and its leaf connects to leafAddress with the leaf credentials.
func startServer(state string, s settings, leafAddress string) (*server.Server, error) {
	operator, err := jwt.DecodeOperatorClaims(s.Trust.OperatorJWT)
	if err != nil {
		return nil, fmt.Errorf("read the operator: %w", err)
	}
	resolver := &server.MemAccResolver{}
	for pub, token := range map[string]string{s.Trust.AgentsAccount: s.Trust.AgentsJWT, s.Trust.SystemAccount: s.Trust.SystemJWT} {
		if err := resolver.Store(pub, token); err != nil {
			return nil, fmt.Errorf("store account %s: %w", pub, err)
		}
	}
	leafURL, err := url.Parse("nats-leaf://" + leafAddress)
	if err != nil {
		return nil, err
	}
	storeDir := filepath.Join(state, "nats")
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		return nil, err
	}
	srv, err := server.NewServer(&server.Options{
		ServerName:       "janus-agent-" + s.AgentID,
		DontListen:       true,
		JetStream:        true,
		JetStreamDomain:  contractfleet.AgentDomain(s.AgentID),
		StoreDir:         storeDir,
		TrustedOperators: []*jwt.OperatorClaims{operator},
		AccountResolver:  resolver,
		SystemAccount:    s.Trust.SystemAccount,
		NoSigs:           true,
		NoLog:            true,
		LeafNode: server.LeafNodeOpts{Remotes: []*server.RemoteLeafOpts{{
			URLs:         []*url.URL{leafURL},
			Credentials:  credsPath(state, leafCreds),
			LocalAccount: s.Trust.AgentsAccount,
		}}},
	})
	if err != nil {
		return nil, fmt.Errorf("configure the Agent's server: %w", err)
	}
	srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		srv.Shutdown()
		return nil, errors.New("the Agent's server did not start")
	}
	return srv, nil
}
