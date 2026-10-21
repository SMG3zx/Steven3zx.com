// Package agentlinktest holds an in-process app.AgentLink and the contract
// suite every app.AgentLink adapter, real or test, must pass.
package agentlinktest

import (
	"context"
	"errors"
	"sync"

	"janus/internal/app"
)

// Loopback is an app.AgentLink to Agents in the same process: each address is
// an *app.AgentRuntime registered with Serve. It carries commands and events
// the way a network link does, including connections that drop when an Agent
// goes away and come back when it returns. It is safe for concurrent use.
type Loopback struct {
	mu     sync.Mutex
	agents map[string]*app.AgentRuntime // by address
	moved  chan struct{}                // closed and replaced when agents changes
	conns  map[string]*loopConn         // by Agent ID
	wg     sync.WaitGroup
}

type loopConn struct {
	address string
	events  func(app.AgentEvent)
	stop    chan struct{}

	mu       sync.Mutex
	attached *app.AgentRuntime
}

var _ app.AgentLink = (*Loopback)(nil)

// NewLoopback returns a Loopback with no Agents.
func NewLoopback() *Loopback {
	return &Loopback{agents: map[string]*app.AgentRuntime{}, moved: make(chan struct{}), conns: map[string]*loopConn{}}
}

// Serve makes runtime the Agent at address, replacing any Agent there.
func (l *Loopback) Serve(address string, runtime *app.AgentRuntime) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.agents[address] = runtime
	l.announce()
}

// Unserve takes the Agent at address away, as a crash or a network failure
// would; connections to it drop.
func (l *Loopback) Unserve(address string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.agents, address)
	l.announce()
}

// announce wakes every connection to look at the Agents again. The caller holds mu.
func (l *Loopback) announce() {
	close(l.moved)
	l.moved = make(chan struct{})
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
	conn := &loopConn{address: address, events: events, stop: make(chan struct{})}
	l.conns[agentID] = conn
	l.wg.Add(1)
	go l.run(conn)
}

func (l *Loopback) Disconnect(agentID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if conn, ok := l.conns[agentID]; ok {
		close(conn.stop)
		delete(l.conns, agentID)
	}
}

func (l *Loopback) Close() error {
	l.mu.Lock()
	for id, conn := range l.conns {
		close(conn.stop)
		delete(l.conns, id)
	}
	l.mu.Unlock()
	l.wg.Wait()
	return nil
}

// run keeps conn attached to whichever Agent is at its address and forwards
// that Agent's events, until conn is stopped.
func (l *Loopback) run(conn *loopConn) {
	defer l.wg.Done()
	for {
		l.mu.Lock()
		runtime, moved := l.agents[conn.address], l.moved
		l.mu.Unlock()
		conn.attach(runtime)
		if runtime == nil {
			select {
			case <-conn.stop:
				return
			case <-moved:
				continue
			}
		}
		state := runtime.State()
		conn.events(app.AgentEvent{State: &state})
	forward:
		for {
			select {
			case <-conn.stop:
				conn.attach(nil)
				return
			case <-moved:
				break forward
			case report := <-runtime.Reports():
				conn.events(app.AgentEvent{Usage: &report})
			case <-runtime.Changes():
				state := runtime.State()
				conn.events(app.AgentEvent{State: &state})
			}
		}
	}
}

func (c *loopConn) attach(runtime *app.AgentRuntime) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attached = runtime
}

func (l *Loopback) Send(ctx context.Context, agentID string, command app.AgentCommand) error {
	l.mu.Lock()
	conn := l.conns[agentID]
	l.mu.Unlock()
	if conn == nil {
		return app.ErrAgentUnreachable
	}
	conn.mu.Lock()
	runtime := conn.attached
	conn.mu.Unlock()
	if runtime == nil {
		return app.ErrAgentUnreachable
	}
	var err error
	switch {
	case command.Deploy != nil:
		err = runtime.Deploy(ctx, *command.Deploy)
	case command.Stop != "":
		err = runtime.Stop(ctx, command.Stop)
	case command.Pause:
		err = runtime.Pause(ctx)
	case command.Resume:
		err = runtime.Resume(ctx)
	default:
		err = errors.New("the command has no action")
	}
	if err != nil {
		return &app.AgentError{Message: err.Error()}
	}
	return nil
}
