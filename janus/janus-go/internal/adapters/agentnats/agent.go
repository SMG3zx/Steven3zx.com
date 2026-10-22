package agentnats

import (
	"context"
	"encoding/json"
	"fmt"
	"janus/internal/domain/agent"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/app"
	contractfleet "janus/internal/contract/fleet"
	"janus/internal/platform/natsx"
)

// heartbeat is how often an Agent writes its state to the hub when nothing
// changed. The control plane counts an Agent offline when its state is older.
const heartbeat = 10 * time.Second

// usageRetention is how long an Agent keeps its Usage while the hub cannot read
// it.
const usageRetention = 7 * 24 * time.Hour

// Config says how an Agent serves the link. Conn is the Agent's own server; the
// hub is reached through its leaf connection.
type Config struct {
	Conn    *nats.Conn
	AgentID string
	OwnerID string
	Runtime *agent.Runtime
	// Heartbeat is how often the state is written when nothing changed. Zero
	// means 10 seconds.
	Heartbeat time.Duration
}

// Serve answers the control plane's commands for the Agent, writes its state
// and publishes its Usage, until ctx ends. It returns an error only when it
// cannot start: the Agent's USAGE stream cannot be made.
func Serve(ctx context.Context, cfg Config) error {
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = heartbeat
	}
	local, err := jetstream.New(cfg.Conn)
	if err != nil {
		return err
	}
	if _, err := local.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     contractfleet.AgentUsageStream,
		Subjects: []string{contractfleet.AgentUsageSubject(cfg.AgentID)},
		MaxAge:   usageRetention,
	}); err != nil {
		return fmt.Errorf("make the Usage stream: %w", err)
	}
	hub, err := jetstream.NewWithDomain(cfg.Conn, natsx.HubDomain)
	if err != nil {
		return err
	}
	agent := &serving{cfg: cfg, local: local, hub: hub}
	subscription, err := cfg.Conn.Subscribe(contractfleet.AgentCommandSubject(cfg.AgentID), agent.command)
	if err != nil {
		return fmt.Errorf("subscribe to commands: %w", err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { agent.writeState(ctx) })
	wg.Go(func() { agent.publishUsage(ctx) })
	<-ctx.Done()
	_ = subscription.Unsubscribe()
	wg.Wait()
	return nil
}

// serving is an Agent's end of the link while it runs.
type serving struct {
	cfg   Config
	local jetstream.JetStream
	hub   jetstream.JetStream
	// commands runs each command in its own goroutine, so a pause does not wait
	// behind a Deploy's probe.
	commands sync.WaitGroup
}

// command answers one command. A Deploy's module is read from the hub's modules
// bucket, which fleet filled before it sent the command.
func (a *serving) command(message *nats.Msg) {
	a.commands.Go(func() {
		var command contractfleet.AgentCommand
		reply := contractfleet.AgentReply{}
		if err := json.Unmarshal(message.Data, &command); err != nil {
			reply.Error = "the command is not one this Agent knows: " + err.Error()
		} else if err := a.apply(command); err != nil {
			reply.Error = err.Error()
		}
		body, err := json.Marshal(reply)
		if err == nil {
			_ = message.Respond(body)
		}
	})
}

// apply runs command on the runtime. Module bytes are read from the hub first,
// and must match the SHA-256 the command names.
func (a *serving) apply(command contractfleet.AgentCommand) error {
	ctx, cancel := context.WithTimeout(context.Background(), agent.RequestTimeout)
	defer cancel()
	if command.Deploy != nil && command.Deploy.Module == nil {
		module, err := a.fetchModule(ctx, command.Deploy.SHA256)
		if err != nil {
			return err
		}
		command.Deploy.Module = module
	}
	return a.cfg.Runtime.Apply(ctx, command)
}

// fetchModule reads the module named sha from the Owner's modules bucket on the
// hub.
func (a *serving) fetchModule(ctx context.Context, sha string) ([]byte, error) {
	store, err := a.hub.ObjectStore(ctx, contractfleet.ModulesBucket(a.cfg.OwnerID))
	if err != nil {
		return nil, fmt.Errorf("open the modules: %w", err)
	}
	module, err := store.GetBytes(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("read the module %s: %w", sha, err)
	}
	return module, nil
}

// writeState puts the runtime's state under the Agent's ID in the state bucket
// when it changes, and at least every heartbeat, until ctx ends.
func (a *serving) writeState(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Heartbeat)
	defer ticker.Stop()
	for {
		a.putState(ctx)
		select {
		case <-ctx.Done():
			return
		case <-a.cfg.Runtime.Changes():
		case <-ticker.C:
		}
	}
}

// putState writes the current state. A failure is retried at the next beat.
func (a *serving) putState(ctx context.Context) {
	body, err := json.Marshal(a.cfg.Runtime.State())
	if err != nil {
		return
	}
	bucket, err := a.hub.KeyValue(ctx, contractfleet.AgentStateBucket)
	if err != nil {
		return
	}
	_, _ = bucket.Put(ctx, a.cfg.AgentID, body)
}

// publishUsage publishes each run the runtime reports to the Agent's USAGE stream,
// keyed by its run ID, so a run published twice is stored once. A run that is
// not stored goes back to the runtime and is published again.
func (a *serving) publishUsage(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case report := <-a.cfg.Runtime.Reports():
			if err := a.publish(ctx, report); err != nil {
				a.cfg.Runtime.Requeue(report)
				select {
				case <-ctx.Done():
					return
				case <-time.After(redeliverDelay):
				}
			}
		}
	}
}

func (a *serving) publish(ctx context.Context, report app.RunReport) error {
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	publishCtx, cancel := context.WithTimeout(ctx, agent.RequestTimeout)
	defer cancel()
	_, err = a.local.Publish(publishCtx, contractfleet.AgentUsageSubject(a.cfg.AgentID), body, jetstream.WithMsgID(report.RunID))
	return err
}
