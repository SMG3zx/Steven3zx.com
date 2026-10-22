// Package linktest holds an in-process Agent link and the contract
// suite every Agent link, real or test, must pass.
package linktest

import (
	"context"
	"janus/internal/domain/agent"
	"sync"
	"time"

	domainfleet "janus/internal/domain/fleet"
)

// Loopback is an Agent link to Agents in the same process: each address is
// an *agent.Runtime registered with Serve. It carries commands and events
// the way a network link does, including connections that drop when an Agent
// goes away and come back when it returns. A connection to an Agent that is
// served is up as soon as Connect or Serve returns, and Settle waits for its
// events, so tests need not poll. It is safe for concurrent use.
type Loopback struct {
	// ModuleSource reads the module of a Deploy whose module the service did not
	// send inline, as an Agent reads it from the hub. Nil means such a Deploy
	// carries no module.
	ModuleSource func(ctx context.Context, agentID, sha string) ([]byte, error)

	mu     sync.Mutex
	agents map[string]*agent.Runtime // by Agent ID
	conns  map[string]*loopConn      // by Agent ID
	wg     sync.WaitGroup
}

type loopConn struct {
	agentID string
	events  func(domainfleet.AgentEvent) error
	stop    chan struct{}
	// moved wakes the connection when its Agent changes; settle asks it to
	// deliver what is waiting and answer.
	moved  chan struct{}
	settle chan chan struct{}

	mu       sync.Mutex
	attached *agent.Runtime
}

// NewLoopback returns a Loopback with no Agents.
func NewLoopback() *Loopback {
	return &Loopback{agents: map[string]*agent.Runtime{}, conns: map[string]*loopConn{}}
}

// Serve makes runtime the Agent with agentID, replacing any Agent with that ID.
func (l *Loopback) Serve(agentID string, runtime *agent.Runtime) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.agents[agentID] = runtime
	l.moveLocked(agentID, runtime)
}

// Unserve takes the Agent with agentID away, as a crash or a network failure
// would; connections to it drop.
func (l *Loopback) Unserve(agentID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.agents, agentID)
	l.moveLocked(agentID, nil)
}

// moveLocked attaches every connection to the Agent with agentID to runtime. The
// caller holds mu.
func (l *Loopback) moveLocked(agentID string, runtime *agent.Runtime) {
	for _, conn := range l.conns {
		if conn.agentID == agentID {
			conn.attach(runtime)
			select {
			case conn.moved <- struct{}{}:
			default:
			}
		}
	}
}

func (l *Loopback) Connect(agentID string, events func(domainfleet.AgentEvent) error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.conns[agentID]; ok {
		return
	}
	conn := &loopConn{agentID: agentID, events: events, stop: make(chan struct{}),
		moved: make(chan struct{}, 1), settle: make(chan chan struct{}), attached: l.agents[agentID]}
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
		_ = conn.events(domainfleet.AgentEvent{State: &state, At: time.Now()})
	forward:
		for {
			select {
			case <-conn.stop:
				return
			case <-conn.moved:
				break forward
			case report := <-runtime.Reports():
				_ = conn.events(domainfleet.AgentEvent{Usage: &report})
			case <-runtime.Changes():
				state := runtime.State()
				_ = conn.events(domainfleet.AgentEvent{State: &state, At: time.Now()})
			case done := <-conn.settle:
				drain(conn, runtime)
				close(done)
			}
		}
	}
}

// drain delivers what runtime has waiting without blocking.
func drain(conn *loopConn, runtime *agent.Runtime) {
	for {
		select {
		case report := <-runtime.Reports():
			_ = conn.events(domainfleet.AgentEvent{Usage: &report})
		case <-runtime.Changes():
			state := runtime.State()
			_ = conn.events(domainfleet.AgentEvent{State: &state, At: time.Now()})
		default:
			return
		}
	}
}

func (c *loopConn) attach(runtime *agent.Runtime) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attached = runtime
}

func (c *loopConn) current() *agent.Runtime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attached
}

func (l *Loopback) Send(ctx context.Context, agentID string, command domainfleet.AgentCommand) error {
	l.mu.Lock()
	conn := l.conns[agentID]
	l.mu.Unlock()
	if conn == nil {
		return domainfleet.ErrAgentUnreachable
	}
	runtime := conn.current()
	if runtime == nil {
		return domainfleet.ErrAgentUnreachable
	}
	if command.Deploy != nil && command.Deploy.Module == nil && l.ModuleSource != nil {
		module, err := l.ModuleSource(ctx, agentID, command.Deploy.SHA256)
		if err != nil {
			return &domainfleet.AgentError{Message: err.Error()}
		}
		deploy := *command.Deploy
		deploy.Module = module
		command.Deploy = &deploy
	}
	if err := runtime.Apply(ctx, command); err != nil {
		return &domainfleet.AgentError{Message: err.Error()}
	}
	return nil
}
