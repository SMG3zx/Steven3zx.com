// Package agentlinktest holds an in-process app.AgentLink and the contract
// suite every app.AgentLink adapter, real or test, must pass.
package agentlinktest

import (
	"context"
	"sync"

	"janus/internal/app"
)

// Loopback is an app.AgentLink to Agents in the same process: each address is
// an *app.AgentRuntime registered with Serve. It carries commands and events
// the way a network link does, including connections that drop when an Agent
// goes away and come back when it returns. A connection to an Agent that is
// served is up as soon as Connect or Serve returns, and Settle waits for its
// events, so tests need not poll. It is safe for concurrent use.
type Loopback struct {
	mu     sync.Mutex
	agents map[string]*app.AgentRuntime // by address
	conns  map[string]*loopConn         // by Agent ID
	wg     sync.WaitGroup
}

type loopConn struct {
	address string
	events  func(app.AgentEvent)
	stop    chan struct{}
	// moved wakes the connection when its Agent changes; settle asks it to
	// deliver what is waiting and answer.
	moved  chan struct{}
	settle chan chan struct{}

	mu       sync.Mutex
	attached *app.AgentRuntime
}

var _ app.AgentLink = (*Loopback)(nil)

// NewLoopback returns a Loopback with no Agents.
func NewLoopback() *Loopback {
	return &Loopback{agents: map[string]*app.AgentRuntime{}, conns: map[string]*loopConn{}}
}

// Serve makes runtime the Agent at address, replacing any Agent there.
func (l *Loopback) Serve(address string, runtime *app.AgentRuntime) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.agents[address] = runtime
	l.moveLocked(address, runtime)
}

// Unserve takes the Agent at address away, as a crash or a network failure
// would; connections to it drop.
func (l *Loopback) Unserve(address string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.agents, address)
	l.moveLocked(address, nil)
}

// moveLocked attaches every connection to address to runtime. The caller holds mu.
func (l *Loopback) moveLocked(address string, runtime *app.AgentRuntime) {
	for _, conn := range l.conns {
		if conn.address == address {
			conn.attach(runtime)
			select {
			case conn.moved <- struct{}{}:
			default:
			}
		}
	}
}

func (l *Loopback) Connect(agentID, address string, events func(app.AgentEvent)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if old, ok := l.conns[agentID]; ok {
		if old.address == address {
			return
		}
		close(old.stop)
	}
	conn := &loopConn{address: address, events: events, stop: make(chan struct{}),
		moved: make(chan struct{}, 1), settle: make(chan chan struct{}), attached: l.agents[address]}
	l.conns[agentID] = conn
	l.wg.Add(1)
	go l.run(conn)
}

func (l *Loopback) Disconnect(agentID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if conn, ok := l.conns[agentID]; ok {
		conn.attach(nil)
		close(conn.stop)
		delete(l.conns, agentID)
	}
}

func (l *Loopback) Close() error {
	l.mu.Lock()
	for id, conn := range l.conns {
		conn.attach(nil)
		close(conn.stop)
		delete(l.conns, id)
	}
	l.mu.Unlock()
	l.wg.Wait()
	return nil
}

// Settle waits until the connection to agentID has delivered every event its
// Agent has produced so far. It returns at once for an Agent with no
// connection or with no Agent served at its address.
func (l *Loopback) Settle(agentID string) {
	l.mu.Lock()
	conn := l.conns[agentID]
	l.mu.Unlock()
	if conn == nil {
		return
	}
	done := make(chan struct{})
	select {
	case conn.settle <- done:
		<-done
	case <-conn.stop:
	}
}

// run forwards the events of whichever Agent conn is attached to, State first
// on every attachment, until conn is stopped.
func (l *Loopback) run(conn *loopConn) {
	defer l.wg.Done()
	for {
		runtime := conn.current()
		if runtime == nil {
			select {
			case <-conn.stop:
				return
			case <-conn.moved:
				continue
			case done := <-conn.settle:
				close(done)
				continue
			}
		}
		state := runtime.State()
		conn.events(app.AgentEvent{State: &state})
	forward:
		for {
			select {
			case <-conn.stop:
				return
			case <-conn.moved:
				break forward
			case report := <-runtime.Reports():
				conn.events(app.AgentEvent{Usage: &report})
			case <-runtime.Changes():
				state := runtime.State()
				conn.events(app.AgentEvent{State: &state})
			case done := <-conn.settle:
				drain(conn, runtime)
				close(done)
			}
		}
	}
}

// drain delivers what runtime has waiting without blocking.
func drain(conn *loopConn, runtime *app.AgentRuntime) {
	for {
		select {
		case report := <-runtime.Reports():
			conn.events(app.AgentEvent{Usage: &report})
		case <-runtime.Changes():
			state := runtime.State()
			conn.events(app.AgentEvent{State: &state})
		default:
			return
		}
	}
}

func (c *loopConn) attach(runtime *app.AgentRuntime) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attached = runtime
}

func (c *loopConn) current() *app.AgentRuntime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attached
}

func (l *Loopback) Send(ctx context.Context, agentID string, command app.AgentCommand) error {
	l.mu.Lock()
	conn := l.conns[agentID]
	l.mu.Unlock()
	if conn == nil {
		return app.ErrAgentUnreachable
	}
	runtime := conn.current()
	if runtime == nil {
		return app.ErrAgentUnreachable
	}
	if err := runtime.Apply(ctx, command); err != nil {
		return &app.AgentError{Message: err.Error()}
	}
	return nil
}
