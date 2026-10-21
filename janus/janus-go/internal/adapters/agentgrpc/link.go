package agentgrpc

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"janus/internal/adapters/agentgrpc/agentpb"
	"janus/internal/app"
)

// Link is the control plane's end: an app.AgentLink that holds one gRPC stream
// to each connected Agent and reopens it when it drops.
//
// The connection is not TLS: on the tailnet it already runs inside WireGuard,
// between nodes that Headscale authenticated.
type Link struct {
	dial     func(ctx context.Context, address string) (net.Conn, error)
	maxDelay time.Duration

	mu    sync.Mutex
	conns map[string]*linkConn
	wg    sync.WaitGroup
}

var _ app.AgentLink = (*Link)(nil)

// Option changes a Link.
type Option func(*Link)

// WithMaxRetryDelay caps the wait between attempts to reach an Agent, 5 s by
// default.
func WithMaxRetryDelay(d time.Duration) Option { return func(l *Link) { l.maxDelay = d } }

// NewLink returns a Link that opens connections with dial, such as a tailnet
// node's Dial.
func NewLink(dial func(ctx context.Context, address string) (net.Conn, error), options ...Option) *Link {
	l := &Link{dial: dial, maxDelay: 5 * time.Second, conns: map[string]*linkConn{}}
	for _, option := range options {
		option(l)
	}
	return l
}

type linkConn struct {
	address string
	events  func(app.AgentEvent)
	cancel  context.CancelFunc
	client  *grpc.ClientConn

	mu      sync.Mutex
	stream  agentpb.Agent_ConnectClient
	pending map[string]chan *agentpb.Reply
	sendMu  sync.Mutex
}

func (l *Link) Connect(agentID, address string, events func(app.AgentEvent)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if old, ok := l.conns[agentID]; ok {
		if old.address == address {
			return
		}
		old.cancel()
		delete(l.conns, agentID)
	}
	client, err := grpc.NewClient("passthrough:///"+address,
		grpc.WithContextDialer(l.dial),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// ENG-78: ping every 10 s with a 5 s timeout, so a dead Agent is noticed
		// in about 15 s.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 10 * time.Second, Timeout: 5 * time.Second, PermitWithoutStream: true}),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.Config{BaseDelay: 200 * time.Millisecond, Multiplier: 1.6, Jitter: 0.2, MaxDelay: l.maxDelay},
			MinConnectTimeout: 5 * time.Second,
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessageBytes), grpc.MaxCallSendMsgSize(maxMessageBytes)),
	)
	if err != nil {
		// NewClient fails only for a malformed target; an address that never
		// answers is retried below like any other.
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	conn := &linkConn{address: address, events: events, cancel: cancel, client: client, pending: map[string]chan *agentpb.Reply{}}
	l.conns[agentID] = conn
	l.wg.Go(func() { l.run(ctx, conn) })
}

func (l *Link) Disconnect(agentID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if conn, ok := l.conns[agentID]; ok {
		conn.cancel()
		delete(l.conns, agentID)
	}
}

func (l *Link) Close() error {
	l.mu.Lock()
	for id, conn := range l.conns {
		conn.cancel()
		delete(l.conns, id)
	}
	l.mu.Unlock()
	l.wg.Wait()
	return nil
}

// run opens the stream, reads it until it breaks, and opens it again, until
// ctx ends.
func (l *Link) run(ctx context.Context, conn *linkConn) {
	defer conn.client.Close()
	agent := agentpb.NewAgentClient(conn.client)
	delay := 200 * time.Millisecond
	for ctx.Err() == nil {
		stream, err := agent.Connect(ctx, grpc.WaitForReady(true))
		if err == nil {
			delay = 200 * time.Millisecond
			conn.read(stream)
		}
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
		delay = min(delay*2, l.maxDelay)
	}
}

// read delivers the stream's events and replies until it breaks. The stream
// counts as up for Send only once its first message, the Agent's State, has
// arrived, so a caller that the Agent refuses never sees it as up.
func (c *linkConn) read(stream agentpb.Agent_ConnectClient) {
	defer c.drop()
	for first := true; ; first = false {
		event, err := stream.Recv()
		if err != nil {
			return
		}
		if first {
			c.mu.Lock()
			c.stream = stream
			c.mu.Unlock()
		}
		switch kind := event.Kind.(type) {
		case *agentpb.Event_Reply:
			c.mu.Lock()
			waiter := c.pending[kind.Reply.Id]
			delete(c.pending, kind.Reply.Id)
			c.mu.Unlock()
			if waiter != nil {
				waiter <- kind.Reply
			}
		case *agentpb.Event_State:
			state := stateFromProto(kind.State)
			c.events(app.AgentEvent{State: &state})
		case *agentpb.Event_Usage:
			report := reportFromProto(kind.Usage)
			c.events(app.AgentEvent{Usage: &report})
		}
	}
}

// drop forgets the broken stream and fails every command waiting on it.
func (c *linkConn) drop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stream = nil
	for id, waiter := range c.pending {
		close(waiter)
		delete(c.pending, id)
	}
}

func (l *Link) Send(ctx context.Context, agentID string, command app.AgentCommand) error {
	l.mu.Lock()
	conn := l.conns[agentID]
	l.mu.Unlock()
	if conn == nil {
		return app.ErrAgentUnreachable
	}
	id := uuid.NewString()
	waiter := make(chan *agentpb.Reply, 1)
	conn.mu.Lock()
	stream := conn.stream
	if stream != nil {
		conn.pending[id] = waiter
	}
	conn.mu.Unlock()
	if stream == nil {
		return app.ErrAgentUnreachable
	}
	conn.sendMu.Lock()
	err := stream.Send(commandToProto(id, command))
	conn.sendMu.Unlock()
	if err != nil {
		conn.forget(id)
		return errors.Join(app.ErrAgentUnreachable, err)
	}
	select {
	case reply, ok := <-waiter:
		switch {
		case !ok:
			return app.ErrAgentUnreachable
		case reply.Error != "":
			return &app.AgentError{Message: reply.Error}
		}
		return nil
	case <-ctx.Done():
		conn.forget(id)
		return ctx.Err()
	}
}

func (c *linkConn) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
}
