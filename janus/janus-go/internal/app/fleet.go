package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"janus/internal/domain/artifact"
)

// ErrAgentNotFound is returned for an Agent the Owner does not have.
var ErrAgentNotFound = errors.New("Agent not found")

// recordTimeout bounds the Store write of one reported run.
const recordTimeout = 10 * time.Second

// AgentStatusReport is the last state an Agent reported over its connection.
// Connected is false when the control plane has no state from it yet.
type AgentStatusReport struct {
	Connected bool
	AgentState
}

// Fleet keeps the control plane connected to every enrolled Agent whose node
// joined the tailnet, sends Agents their commands, and records the Usage they
// report. Every Owner-facing call is scoped to the Owner's own Agents. Usage is
// recorded under the Owner of the Agent that reported it, whatever the report
// says.
type Fleet struct {
	store   Store
	builds  BuildArtifacts
	tailnet Tailnet
	link    AgentLink
	audit   Audit

	mu        sync.Mutex
	addresses map[string]string            // connected Agent ID to address
	states    map[string]AgentStatusReport // by Agent ID
}

// NewFleet returns a Fleet that finds Agents in store and their nodes on
// tailnet, and reaches them through link.
func NewFleet(store Store, tailnet Tailnet, link AgentLink, audit Audit) *Fleet {
	return &Fleet{store: store, tailnet: tailnet, link: link, audit: audit,
		addresses: map[string]string{}, states: map[string]AgentStatusReport{}}
}

// WithBuilds makes the Fleet read the Artifacts it deploys from builds. It
// returns f, so it chains onto NewFleet.
func (f *Fleet) WithBuilds(builds BuildArtifacts) *Fleet {
	f.builds = builds
	return f
}

// Reconcile connects to every Agent whose node has joined the tailnet, at its
// node's IPv4 address, and disconnects from Agents that were removed. An Agent
// whose node is offline is still connected to, so the link reaches it as soon
// as it is back.
func (f *Fleet) Reconcile(ctx context.Context) error {
	agents, err := f.store.ListAllAgents(ctx)
	if err != nil {
		return fmt.Errorf("list Agents: %w", err)
	}
	nodes, err := f.tailnet.Nodes(ctx)
	if err != nil {
		return fmt.Errorf("list tailnet nodes: %w", err)
	}
	byKey := make(map[string]TailnetNode, len(nodes))
	for _, node := range nodes {
		if node.KeyID != "" {
			byKey[node.KeyID] = node
		}
	}
	want := map[string]bool{}
	for _, agent := range agents {
		node, joined := byKey[agent.KeyID]
		ip := ipv4(node.Addresses)
		if !joined || ip == "" {
			continue
		}
		want[agent.ID] = true
		address := net.JoinHostPort(ip, strconv.Itoa(AgentConnectPort))
		f.mu.Lock()
		current := f.addresses[agent.ID]
		f.addresses[agent.ID] = address
		f.mu.Unlock()
		if current != address {
			f.link.Connect(agent.ID, address, f.receiver(agent))
		}
	}
	f.mu.Lock()
	var gone []string
	for id := range f.addresses {
		if !want[id] {
			gone = append(gone, id)
			delete(f.addresses, id)
			delete(f.states, id)
		}
	}
	f.mu.Unlock()
	for _, id := range gone {
		f.link.Disconnect(id)
	}
	return nil
}

// Run reconciles at once and then every interval until ctx ends. A failed
// round is recorded and retried at the next one.
func (f *Fleet) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := f.Reconcile(ctx); err != nil && ctx.Err() == nil {
			f.audit.Record(ctx, AuditEvent{Level: "warn", Name: "agent.reconcile.failed", Fields: map[string]string{"error": err.Error()}})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// receiver handles what agent reports over its connection.
func (f *Fleet) receiver(agent AgentRecord) func(AgentEvent) {
	return func(event AgentEvent) {
		switch {
		case event.State != nil:
			f.mu.Lock()
			if _, connected := f.addresses[agent.ID]; connected {
				f.states[agent.ID] = AgentStatusReport{Connected: true, AgentState: *event.State}
			}
			f.mu.Unlock()
		case event.Usage != nil:
			ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
			defer cancel()
			record := RunUsageRecord{RunReport: *event.Usage, OwnerID: agent.OwnerID, AgentID: agent.ID}
			if err := f.store.RecordRunUsage(ctx, record); err != nil {
				f.audit.Record(ctx, AuditEvent{Level: "error", Name: "agent.usage.dropped",
					Fields: map[string]string{"user_id": agent.OwnerID, "agent_id": agent.ID, "error": err.Error()}})
			}
		}
	}
}

// Status returns the last state the Owner's Agent reported, for each of the
// Owner's Agents by ID.
func (f *Fleet) Status(ctx context.Context, ownerID string) (map[string]AgentStatusReport, error) {
	agents, err := f.store.ListAgents(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]AgentStatusReport, len(agents))
	for _, agent := range agents {
		out[agent.ID] = f.states[agent.ID]
	}
	return out, nil
}

// Pause tells the Owner's Agent to stop serving until Resume.
func (f *Fleet) Pause(ctx context.Context, ownerID, agentID string) error {
	return f.command(ctx, ownerID, agentID, AgentCommand{Pause: true}, "agent.paused")
}

// Resume tells the Owner's Agent to serve again.
func (f *Fleet) Resume(ctx context.Context, ownerID, agentID string) error {
	return f.command(ctx, ownerID, agentID, AgentCommand{Resume: true}, "agent.resumed")
}

// Ready returns nil when the Owner's Agent is connected and not paused, so a
// Deploy can reach it. Otherwise it returns an error that says why not.
func (f *Fleet) Ready(ctx context.Context, ownerID, agentID string) error {
	states, err := f.Status(ctx, ownerID)
	if err != nil {
		return err
	}
	state, ok := states[agentID]
	switch {
	case !ok:
		return ErrAgentNotFound
	case state.Paused:
		return errors.New("the Agent is paused, so it cannot take a Deploy")
	case !state.Connected:
		return errors.New("the Agent is offline, so it cannot take a Deploy")
	}
	return nil
}

var _ AgentDeployer = (*Fleet)(nil)

// Stop tells the Owner's Agent to stop serving a Deployment.
func (f *Fleet) Stop(ctx context.Context, ownerID, agentID, deploymentID string) error {
	return f.command(ctx, ownerID, agentID, AgentCommand{Stop: deploymentID}, "agent.deployment.stopped")
}

// Deploy hands the Owner's Agent the module of a completed WASI build as the
// Deployment deploymentID, with the limits of the build's Template. It returns
// nil once the Agent reports the Deployment healthy, an *AgentError with the
// reason when it failed there, and an error matching ErrAgentUnreachable when
// the Agent cannot be reached.
func (f *Fleet) Deploy(ctx context.Context, ownerID, agentID, deploymentID, buildID string) error {
	build, stored, err := runnableBuild(ctx, f.store, f.builds, ownerID, buildID)
	if err != nil {
		return err
	}
	module, _, err := artifact.Module(stored.Data, stored.Name)
	if err != nil {
		return fmt.Errorf("open the Artifact: %w", err)
	}
	deployment := HostedDeployment{ID: deploymentID, SHA256: hashOf(module), Module: module,
		Limits: templateLimits(ctx, f.store, ownerID, build)}
	return f.command(ctx, ownerID, agentID, AgentCommand{Deploy: &deployment}, "agent.deployment.started")
}

// Usage returns the Owner's recorded runs that match query, newest first. A
// query for an Agent the Owner does not have fails with ErrAgentNotFound.
func (f *Fleet) Usage(ctx context.Context, query RunUsageQuery) ([]RunUsageRecord, error) {
	if query.AgentID != "" {
		if _, found, err := f.store.FindAgent(ctx, query.AgentID, query.OwnerID); err != nil {
			return nil, err
		} else if !found {
			return nil, ErrAgentNotFound
		}
	}
	return f.store.ListRunUsage(ctx, query)
}

// command sends command to the Owner's Agent and records the Audit Event name
// when the Agent accepts it.
func (f *Fleet) command(ctx context.Context, ownerID, agentID string, command AgentCommand, name string) error {
	if _, found, err := f.store.FindAgent(ctx, agentID, ownerID); err != nil {
		return err
	} else if !found {
		return ErrAgentNotFound
	}
	if err := f.link.Send(ctx, agentID, command); err != nil {
		return err
	}
	fields := map[string]string{"user_id": ownerID, "agent_id": agentID}
	switch {
	case command.Deploy != nil:
		fields["deployment_id"] = command.Deploy.ID
	case command.Stop != "":
		fields["deployment_id"] = command.Stop
	}
	f.audit.Record(ctx, AuditEvent{Level: "info", Name: name, Fields: fields})
	return nil
}

// ipv4 returns the first IPv4 address of addresses, or "".
func ipv4(addresses []string) string {
	for _, address := range addresses {
		if ip := net.ParseIP(address); ip != nil && ip.To4() != nil {
			return address
		}
	}
	return ""
}
