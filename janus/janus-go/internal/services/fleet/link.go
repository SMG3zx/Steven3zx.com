package fleet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	contractfleet "janus/internal/contract/fleet"
)

// recordTimeout bounds the database write of one reported run.
const recordTimeout = 10 * time.Second

// agentStaleAfter is how old an Agent's last state may be before the Agent counts
// as offline. An Agent writes its state every heartbeat, well inside this.
const agentStaleAfter = 30 * time.Second

// agentView is the last state an Agent wrote, and when the hub recorded it.
type agentView struct {
	state contractfleet.AgentState
	at    time.Time
}

// startReconcile starts the loop that keeps the Agent link to every enrolled
// Agent. Stop ends it.
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

// reconcile links every enrolled Agent, and drops the links of Agents that were
// removed. An Agent that is offline is still linked, so its events arrive as
// soon as it is back.
func (s *Service) reconcile(ctx context.Context) error {
	agents, err := s.loadAgents(ctx, "")
	if err != nil {
		return err
	}
	want := make(map[string]bool, len(agents))
	var connect []contractfleet.AgentRecord
	s.mu.Lock()
	for _, agent := range agents {
		want[agent.ID] = true
		if !s.linked[agent.ID] {
			s.linked[agent.ID] = true
			connect = append(connect, agent)
		}
	}
	var gone []string
	for id := range s.linked {
		if !want[id] {
			gone = append(gone, id)
			delete(s.linked, id)
			delete(s.states, id)
		}
	}
	s.mu.Unlock()
	for _, agent := range connect {
		s.cfg.Link.Connect(agent.ID, s.receiver(agent))
	}
	for _, id := range gone {
		s.cfg.Link.Disconnect(id)
	}
	return nil
}

// receiver handles what agent reports. Usage is recorded under the Owner of the
// Agent that reported it, whatever the report says. An event that cannot be
// kept is returned as an error, so the link delivers it again.
func (s *Service) receiver(agent contractfleet.AgentRecord) func(contractfleet.AgentEvent) error {
	return func(event contractfleet.AgentEvent) error {
		switch {
		case event.State != nil:
			s.mu.Lock()
			if s.linked[agent.ID] {
				s.states[agent.ID] = agentView{state: *event.State, at: event.At}
			}
			s.mu.Unlock()
		case event.Usage != nil:
			ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
			defer cancel()
			record := contractfleet.RunUsageRecord{RunReport: *event.Usage, OwnerID: agent.OwnerID, AgentID: agent.ID}
			if err := s.insertRunUsage(ctx, record); err != nil {
				s.audit(ctx, "error", "agent.usage.failed", map[string]string{"user_id": agent.OwnerID, "agent_id": agent.ID, "error": err.Error()})
				return err
			}
		}
		return nil
	}
}

// reportLocked is what the control plane knows of the Agent with id. It is
// connected when the Agent is linked and wrote its state within agentStaleAfter.
// The caller holds mu.
func (s *Service) reportLocked(id string) contractfleet.AgentStatusReport {
	view, ok := s.states[id]
	if !ok || !s.linked[id] {
		return contractfleet.AgentStatusReport{}
	}
	connected := time.Since(view.at) < agentStaleAfter
	return contractfleet.AgentStatusReport{Connected: connected, AgentState: view.state}
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
		reports[agent.ID] = s.reportLocked(agent.ID)
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
	report := s.reportLocked(agentID)
	s.mu.Unlock()
	switch {
	case report.Paused:
		return invalid("the Agent is paused, so it cannot take a Deploy")
	case !report.Connected:
		return invalid("the Agent is offline, so it cannot take a Deploy")
	}
	return nil
}

// stageModule puts the module of a Deployment in the Owner's modules bucket,
// under its SHA-256, where the Agent reads it, and takes the module out of the
// command. A WASM module is larger than one NATS message.
func (s *Service) stageModule(ctx context.Context, owner string, hosted *contractfleet.HostedDeployment) error {
	store, err := s.modulesStore(ctx, owner)
	if err != nil {
		return fmt.Errorf("open the modules of the Owner: %w", err)
	}
	if _, err := store.PutBytes(ctx, hosted.SHA256, hosted.Module); err != nil {
		return fmt.Errorf("stage the module: %w", err)
	}
	hosted.Module = nil
	return nil
}

// modulesStore returns the Owner's modules bucket, making it on first use.
func (s *Service) modulesStore(ctx context.Context, owner string) (jetstream.ObjectStore, error) {
	name := contractfleet.ModulesBucket(owner)
	store, err := s.agentsJS.ObjectStore(ctx, name)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		store, err = s.agentsJS.CreateObjectStore(ctx, jetstream.ObjectStoreConfig{Bucket: name})
	}
	return store, err
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
