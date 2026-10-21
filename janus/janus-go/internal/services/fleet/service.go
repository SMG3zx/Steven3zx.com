// Package fleet is the fleet service. It owns fleet.db: enrollment codes, the
// Janus Agents enrolled on the tailnet, their Deployments, and the Usage they
// report. It keeps the control plane connected to every enrolled Agent whose
// node joined the tailnet, sends Agents their commands, and reads Artifacts from
// the artifacts object store to deploy them. A Deployment's Project and Build are
// plain IDs; the projects and builds services are asked about them. Its contract
// is internal/contract/fleet.
package fleet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/contract"
	contractaudit "janus/internal/contract/audit"
	contractfleet "janus/internal/contract/fleet"
	"janus/internal/platform/natsauth"
	"janus/internal/platform/natsx"
	"janus/internal/platform/tursodb"
	"janus/internal/services/fleet/headscale"
)

// operationTimeout bounds the database, tailnet and builds work of one request.
const operationTimeout = 30 * time.Second

// defaultReconcileInterval is how often the Fleet looks for Agents to connect to.
const defaultReconcileInterval = 15 * time.Second

// AgentConnectPort is the port on an Agent's tailnet node that carries its
// connection to the control plane.
const AgentConnectPort = 7100

// AgentLink carries the control plane's commands to Agents over the tailnet and
// their events back. It holds one connection per Agent.
//
// Connect keeps a connection open to the Agent at address, the IP address of its
// tailnet node, until Disconnect, reconnecting whenever it drops. Connecting an
// Agent that is already connected to the same address does nothing; a new address
// replaces the old connection. Events from the Agent go to events one at a time,
// in the order the Agent sent them, on a goroutine the link owns. An Agent sends
// its AgentState first on every new connection.
//
// Send delivers one command and waits for the Agent's answer. It fails with an
// error matching contractfleet.ErrAgentUnreachable when there is no connection or
// the connection drops before the answer, and with a *contractfleet.AgentError
// when the Agent refused the command. Close disconnects every Agent and waits for
// the link's goroutines.
type AgentLink interface {
	Connect(agentID, address string, events func(contractfleet.AgentEvent))
	Disconnect(agentID string)
	Send(ctx context.Context, agentID string, command contractfleet.AgentCommand) error
	Close() error
}

// Config configures the service.
type Config struct {
	// DatabasePath is fleet.db.
	DatabasePath string
	// Tailnet is the control server the Agents enroll with.
	Tailnet headscale.Tailnet
	// ControlURL is the control server's URL, which each Agent is told to join.
	ControlURL string
	// Link carries commands to the Agents and their events back.
	Link AgentLink
	// Agents is the AGENTS account the hub signs Agents' leaf credentials with.
	// Nil means enrollment issues no leaf credentials.
	Agents *natsauth.Account
	// OnRevoke is called after a leaf credential is revoked in Agents, so the
	// hub can drop the connections it revoked. Nil means nothing is called.
	OnRevoke func(*natsauth.Account) error
	// ReconcileInterval is how often the service connects to Agents. Zero means 15
	// seconds.
	ReconcileInterval time.Duration
	// Now reports the time. Zero means time.Now; tests set it.
	Now func() time.Time
}

// Service answers the fleet subjects from fleet.db and runs Deployments.
type Service struct {
	cfg       Config
	db        *sql.DB
	conn      *nats.Conn
	js        jetstream.JetStream
	service   micro.Service
	artifacts jetstream.ObjectStore
	events    jetstream.ConsumeContext

	// mu guards the connections the reconcile loop keeps, and the last state
	// each Agent reported.
	mu        sync.Mutex
	addresses map[string]string
	states    map[string]contractfleet.AgentStatusReport

	// closed is set when Stop starts; Deployments are not queued after it.
	closed        bool
	running       sync.WaitGroup
	stopCtx       context.Context
	stopRuns      context.CancelFunc
	stopReconcile context.CancelFunc
	reconcileOut  chan struct{}
}

// New returns a stopped service.
func New(cfg Config) *Service {
	if cfg.ReconcileInterval <= 0 {
		cfg.ReconcileInterval = defaultReconcileInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{cfg: cfg, addresses: map[string]string{}, states: map[string]contractfleet.AgentStatusReport{}}
}

// Start opens fleet.db, declares the artifacts store it reads, answers its
// subjects, consumes the projects events that stop and remove Deployments, and
// starts connecting to Agents. A failure leaves nothing running.
func (s *Service) Start(ctx context.Context, conn *nats.Conn) (err error) {
	if s.cfg.Tailnet == nil || s.cfg.Link == nil {
		return errors.New("the fleet service needs a tailnet and an Agent link")
	}
	s.conn = conn
	s.stopCtx, s.stopRuns = context.WithCancel(context.Background())
	if s.db, err = tursodb.Open(ctx, s.cfg.DatabasePath, migrations); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = s.Stop(context.Background())
		}
	}()
	if s.js, err = jetstream.New(conn); err != nil {
		return fmt.Errorf("open JetStream: %w", err)
	}
	if _, err = natsx.DeclareEvents(ctx, s.js); err != nil {
		return err
	}
	if s.artifacts, err = natsx.DeclareObjectStore(ctx, s.js, natsx.ArtifactsBucket); err != nil {
		return err
	}
	if s.service, err = micro.AddService(conn, micro.Config{Name: contractfleet.ServiceName, Version: "1.0.0", Description: "Janus Agents, their Deployments and Usage"}); err != nil {
		return fmt.Errorf("register the fleet service: %w", err)
	}
	if err = s.addEndpoints(); err != nil {
		return err
	}
	if err = s.startProjectEvents(ctx); err != nil {
		return err
	}
	s.startReconcile()
	return nil
}

func (s *Service) addEndpoints() error {
	return errors.Join(
		natsx.AddEndpoint(s.service, "issue-code", contractfleet.SubjectIssueCode, timed(s.issueCode)),
		natsx.AddEndpoint(s.service, "enroll", contractfleet.SubjectEnroll, timed(s.enroll)),
		natsx.AddEndpoint(s.service, "list-agents", contractfleet.SubjectListAgents, timed(s.listAgents)),
		natsx.AddEndpoint(s.service, "remove-agent", contractfleet.SubjectRemoveAgent, timed(s.removeAgent)),
		natsx.AddEndpoint(s.service, "revoke-agents", contractfleet.SubjectRevokeAgents, timed(s.revokeAgents)),
		natsx.AddEndpoint(s.service, "agent-status", contractfleet.SubjectAgentStatus, timed(s.agentStatus)),
		natsx.AddEndpoint(s.service, "pause-agent", contractfleet.SubjectPauseAgent, timed(s.pauseAgent)),
		natsx.AddEndpoint(s.service, "resume-agent", contractfleet.SubjectResumeAgent, timed(s.resumeAgent)),
		natsx.AddEndpoint(s.service, "list-usage", contractfleet.SubjectListUsage, timed(s.listUsage)),
		natsx.AddEndpoint(s.service, "create-deployment", contractfleet.SubjectCreateDeployment, timed(s.createDeployment)),
		natsx.AddEndpoint(s.service, "rollback-deployment", contractfleet.SubjectRollbackDeployment, timed(s.rollbackDeployment)),
		natsx.AddEndpoint(s.service, "stop-deployment", contractfleet.SubjectStopDeployment, timed(s.stopDeployment)),
		natsx.AddEndpoint(s.service, "find-deployment", contractfleet.SubjectFindDeployment, timed(s.findDeployment)),
		natsx.AddEndpoint(s.service, "teardown-deployments", contractfleet.SubjectTeardownDeployments, timed(s.teardownDeployments)),
		natsx.AddEndpoint(s.service, "delete-owner", contractfleet.SubjectDeleteOwner, timed(s.deleteOwner)),
	)
}

// timed bounds a handler by operationTimeout.
func timed[Req, Rep any](handle func(context.Context, Req) (Rep, error)) func(context.Context, Req) (Rep, error) {
	return func(ctx context.Context, request Req) (Rep, error) {
		ctx, cancel := context.WithTimeout(ctx, operationTimeout)
		defer cancel()
		return handle(ctx, request)
	}
}

// Stop stops answering, stops the reconcile loop and the events, waits for the
// running Deployments, and closes
// fleet.db. The Agent link itself belongs to the caller.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	var errs []error
	if s.service != nil {
		errs = append(errs, s.service.Stop())
	}
	if s.events != nil {
		s.events.Drain()
	}
	if s.stopReconcile != nil {
		s.stopReconcile()
		select {
		case <-s.reconcileOut:
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("wait for the reconcile loop: %w", ctx.Err()))
		}
	}
	if s.stopRuns != nil {
		s.stopRuns()
	}
	waited := make(chan struct{})
	go func() { s.running.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("wait for running Deployments: %w", ctx.Err()))
	}
	if s.db != nil {
		errs = append(errs, s.db.Close())
	}
	return errors.Join(errs...)
}

// audit publishes an Audit Event for the audit service. A failure is not the
// caller's: the event is dropped.
func (s *Service) audit(ctx context.Context, level, name string, fields map[string]string) {
	data, err := json.Marshal(contractaudit.Event{Level: level, Name: name, Fields: fields})
	if err != nil {
		return
	}
	msg := &nats.Msg{Subject: contractaudit.Subject(contractfleet.ServiceName), Data: data}
	natsx.Inject(ctx, msg)
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, _ = s.js.PublishMsg(publishCtx, msg)
}

func invalid(format string, args ...any) error {
	return contract.Errorf(contract.StatusInvalid, format, args...)
}

func notFound(format string, args ...any) error {
	return contract.Errorf(contract.StatusNotFound, format, args...)
}

// errAgentNotFound is what a request about an Agent the Owner does not have fails
// with.
var errAgentNotFound = notFound("Agent not found")

func contractUnavailable(format string, args ...any) error {
	return contract.Errorf(contract.StatusUnavailable, format, args...)
}

func requireOwner(owner string) error {
	if owner == "" {
		return invalid("an owner is required")
	}
	return nil
}
