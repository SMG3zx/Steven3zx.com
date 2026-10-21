package fleet

import (
	"context"
	"errors"
	"net"
	"strconv"
	"time"

	contractfleet "janus/internal/contract/fleet"
	"janus/internal/services/fleet/headscale"
)

// recordTimeout bounds the database write of one reported run.
const recordTimeout = 10 * time.Second

// startReconcile starts the loop that keeps the Agent link connected to every
// enrolled Agent. Stop ends it.
func (s *Service) startReconcile() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stopReconcile = cancel
	s.reconcileOut = make(chan struct{})
	go func() {
		defer close(s.reconcileOut)
		s.reconcileLoop(ctx)
	}()
}

// reconcileLoop reconciles at once and then every interval until ctx ends. A
// failed round is audited and retried at the next one.
func (s *Service) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.ReconcileInterval)
	defer ticker.Stop()
	for {
		if err := s.reconcile(ctx); err != nil && ctx.Err() == nil {
			s.audit(ctx, "warn", "agent.reconcile.failed", map[string]string{"error": err.Error()})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// reconcile connects to every Agent whose node has joined the tailnet, at its
// node's IPv4 address, and disconnects from Agents that were removed. An Agent
// whose node is offline is still connected to, so the link reaches it as soon as
// it is back.
func (s *Service) reconcile(ctx context.Context) error {
	agents, err := s.loadAgents(ctx, "")
	if err != nil {
		return err
	}
	nodes, err := s.cfg.Tailnet.Nodes(ctx)
	if err != nil {
		return err
	}
	byKey := make(map[string]headscale.Node, len(nodes))
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
		s.mu.Lock()
		current := s.addresses[agent.ID]
		s.addresses[agent.ID] = address
		s.mu.Unlock()
		if current != address {
			s.cfg.Link.Connect(agent.ID, address, s.receiver(agent))
		}
	}
	s.mu.Lock()
	var gone []string
	for id := range s.addresses {
		if !want[id] {
			gone = append(gone, id)
			delete(s.addresses, id)
			delete(s.states, id)
		}
	}
	s.mu.Unlock()
	for _, id := range gone {
		s.cfg.Link.Disconnect(id)
	}
	return nil
}

// receiver handles what agent reports over its connection. Usage is recorded
// under the Owner of the Agent that reported it, whatever the report says.
func (s *Service) receiver(agent contractfleet.AgentRecord) func(contractfleet.AgentEvent) {
	return func(event contractfleet.AgentEvent) {
		switch {
		case event.State != nil:
			s.mu.Lock()
			if _, connected := s.addresses[agent.ID]; connected {
				s.states[agent.ID] = contractfleet.AgentStatusReport{Connected: true, AgentState: *event.State}
			}
			s.mu.Unlock()
		case event.Usage != nil:
			ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
			defer cancel()
			record := contractfleet.RunUsageRecord{RunReport: *event.Usage, OwnerID: agent.OwnerID, AgentID: agent.ID}
			if err := s.insertRunUsage(ctx, record); err != nil {
				s.audit(ctx, "error", "agent.usage.dropped", map[string]string{"user_id": agent.OwnerID, "agent_id": agent.ID, "error": err.Error()})
			}
		}
	}
}

// agentStatus returns the last state each of the Owner's Agents reported, by ID.
func (s *Service) agentStatus(ctx context.Context, request contractfleet.Owner) (contractfleet.AgentStatuses, error) {
	if err := requireOwner(request.OwnerID); err != nil {
		return contractfleet.AgentStatuses{}, err
	}
	agents, err := s.loadAgents(ctx, request.OwnerID)
	if err != nil {
		return contractfleet.AgentStatuses{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reports := make(map[string]contractfleet.AgentStatusReport, len(agents))
	for _, agent := range agents {
		reports[agent.ID] = s.states[agent.ID]
	}
	return contractfleet.AgentStatuses{Reports: reports}, nil
}

// pauseAgent tells the Owner's Agent to stop serving until it is resumed.
func (s *Service) pauseAgent(ctx context.Context, request contractfleet.AgentKey) (contractfleet.Empty, error) {
	return s.command(ctx, request, contractfleet.AgentCommand{Pause: true}, "agent.paused")
}

// resumeAgent tells the Owner's Agent to serve again.
func (s *Service) resumeAgent(ctx context.Context, request contractfleet.AgentKey) (contractfleet.Empty, error) {
	return s.command(ctx, request, contractfleet.AgentCommand{Resume: true}, "agent.resumed")
}

// command sends command to the Owner's Agent and records the Audit Event name
// when the Agent accepts it.
func (s *Service) command(ctx context.Context, key contractfleet.AgentKey, command contractfleet.AgentCommand, name string) (contractfleet.Empty, error) {
	if err := requireOwner(key.OwnerID); err != nil {
		return contractfleet.Empty{}, err
	}
	if err := s.sendToAgent(ctx, key.OwnerID, key.AgentID, command); err != nil {
		return contractfleet.Empty{}, err
	}
	fields := map[string]string{"user_id": key.OwnerID, "agent_id": key.AgentID}
	switch {
	case command.Deploy != nil:
		fields["deployment_id"] = command.Deploy.ID
	case command.Stop != "":
		fields["deployment_id"] = command.Stop
	}
	s.audit(ctx, "info", name, fields)
	return contractfleet.Empty{}, nil
}

// sendToAgent checks that the Owner has the Agent, then delivers command to it.
// An Agent that cannot be reached is a 503; one that refuses is a 400 with the
// Agent's reason.
func (s *Service) sendToAgent(ctx context.Context, owner, agentID string, command contractfleet.AgentCommand) error {
	if _, found, err := s.loadAgent(ctx, agentID, owner); err != nil {
		return err
	} else if !found {
		return errAgentNotFound
	}
	err := s.cfg.Link.Send(ctx, agentID, command)
	var refused *contractfleet.AgentError
	switch {
	case errors.Is(err, contractfleet.ErrAgentUnreachable):
		return contractUnavailable("the Agent cannot be reached")
	case errors.As(err, &refused):
		return invalid("%s", refused.Message)
	}
	return err
}

// ready returns nil when the Owner's Agent is connected and not paused, so a
// Deployment can reach it. Otherwise it returns an error that says why not.
func (s *Service) ready(ctx context.Context, owner, agentID string) error {
	if _, found, err := s.loadAgent(ctx, agentID, owner); err != nil {
		return err
	} else if !found {
		return errAgentNotFound
	}
	s.mu.Lock()
	state := s.states[agentID]
	connected := false
	if _, ok := s.addresses[agentID]; ok {
		connected = state.Connected
	}
	s.mu.Unlock()
	switch {
	case state.Paused:
		return invalid("the Agent is paused, so it cannot take a Deploy")
	case !connected:
		return invalid("the Agent is offline, so it cannot take a Deploy")
	}
	return nil
}

// listUsage returns the Owner's recorded runs that match the query, newest first.
// A query for an Agent the Owner does not have is not found.
func (s *Service) listUsage(ctx context.Context, query contractfleet.RunUsageQuery) (contractfleet.RunUsage, error) {
	if err := requireOwner(query.OwnerID); err != nil {
		return contractfleet.RunUsage{}, err
	}
	if query.AgentID != "" {
		if _, found, err := s.loadAgent(ctx, query.AgentID, query.OwnerID); err != nil {
			return contractfleet.RunUsage{}, err
		} else if !found {
			return contractfleet.RunUsage{}, errAgentNotFound
		}
	}
	runs, err := s.loadRunUsage(ctx, query)
	return contractfleet.RunUsage{Runs: runs}, err
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
