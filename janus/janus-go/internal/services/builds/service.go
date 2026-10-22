// Package builds is the builds service. It owns builds.db: WASM Builds of a
// Project's Snapshot, their Checks, and the Artifacts they produce. A Build is a
// job on the JOBS stream; a worker pages the Snapshot's files from the projects
// service, compiles them and runs the Checks. Each Artifact is kept in builds.db
// and copied to the artifacts object store, keyed by Build ID. Its contract is
// internal/contract/builds.
package builds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/contract"
	contractaudit "janus/internal/contract/audit"
	contractbuilds "janus/internal/contract/builds"
	"janus/internal/platform/natsx"
	"janus/internal/platform/outbox"
	"janus/internal/platform/tursodb"
)

// operationTimeout bounds the database and projects work of one request.
const operationTimeout = 30 * time.Second

// maxDeliveries is how many times a Build is handed to a worker. One whose
// workers keep dying fails on its last delivery instead of waiting forever.
const maxDeliveries = 4

// Config configures the service.
type Config struct {
	// DatabasePath is builds.db.
	DatabasePath string
	// Compile compiles a workspace. It is required; the container compiler of
	// services/builds/docker is what Janus runs.
	Compile CompileFunc
	// Check runs the checks of a compiled workspace. Nil skips the checks, so a
	// completed Build has no Check.
	Check CheckFunc
	// Workers is how many Builds run at once in this process. Zero means 2.
	Workers int
	// BuildTimeout bounds one Build. Zero means 10 minutes.
	BuildTimeout time.Duration
	// AckWait is how long JetStream waits for a sign of life from a Build's
	// worker before handing the job to another. Zero means 2 minutes.
	AckWait time.Duration
}

// Service answers the builds subjects from builds.db and runs Builds.
type Service struct {
	cfg       Config
	db        *sql.DB
	conn      *nats.Conn
	js        jetstream.JetStream
	service   micro.Service
	relay     *outbox.Relay
	artifacts jetstream.ObjectStore

	workers []jetstream.ConsumeContext
	events  jetstream.ConsumeContext
	// accountEvents consumes identity's account deletions.
	accountEvents jetstream.ConsumeContext
	// running holds the Builds this process is running, so an Owner's can be
	// cancelled and Stop can wait for them.
	mu      sync.Mutex
	running map[string]runningBuild
	wg      sync.WaitGroup
	closing atomic.Bool
	// stopCtx ends when Stop starts, cancelling the running Builds, which are
	// then left for JetStream to hand out again.
	stopCtx  context.Context
	stopRuns context.CancelFunc
}

type runningBuild struct {
	owner  string
	cancel context.CancelFunc
}

// New returns a stopped service.
func New(cfg Config) *Service {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.BuildTimeout <= 0 {
		cfg.BuildTimeout = 10 * time.Minute
	}
	if cfg.AckWait <= 0 {
		cfg.AckWait = 2 * time.Minute
	}
	return &Service{cfg: cfg, running: map[string]runningBuild{}}
}

// Start opens builds.db, declares the streams and the artifacts store it uses,
// starts publishing its outbox, answers its subjects and starts its workers. A
// failure leaves nothing running.
func (s *Service) Start(ctx context.Context, conn *nats.Conn) (err error) {
	if s.cfg.Compile == nil {
		return errors.New("the builds service needs a compiler")
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
	jobs, err := natsx.DeclareJobs(ctx, s.js)
	if err != nil {
		return err
	}
	if s.artifacts, err = natsx.DeclareObjectStore(ctx, s.js, natsx.ArtifactsBucket); err != nil {
		return err
	}
	s.relay = outbox.NewRelay(s.db, s.js)
	s.relay.Start()
	if s.service, err = micro.AddService(conn, micro.Config{Name: contractbuilds.ServiceName, Version: "1.0.0", Description: "WASM Builds, Checks and Artifacts"}); err != nil {
		return fmt.Errorf("register the builds service: %w", err)
	}
	if err = s.addEndpoints(); err != nil {
		return err
	}
	if err = s.startProjectEvents(ctx); err != nil {
		return err
	}
	if err = s.startAccountEvents(); err != nil {
		return err
	}
	return s.startWorkers(ctx, jobs)
}

func (s *Service) addEndpoints() error {
	return errors.Join(
		natsx.AddEndpoint(s.service, "request-build", contractbuilds.SubjectRequestBuild, timed(s.requestBuild)),
		natsx.AddEndpoint(s.service, "retry-build", contractbuilds.SubjectRetryBuild, timed(s.retryBuild)),
		natsx.AddEndpoint(s.service, "find-build", contractbuilds.SubjectFindBuild, timed(s.findBuild)),
		natsx.AddEndpoint(s.service, "find-check", contractbuilds.SubjectFindCheck, timed(s.findCheck)),
		natsx.AddEndpoint(s.service, "find-artifact", contractbuilds.SubjectFindArtifact, timed(s.findArtifact)),
		natsx.AddEndpoint(s.service, "restore-artifact", contractbuilds.SubjectRestoreArtifact, timed(s.restoreArtifact)),
		natsx.AddEndpoint(s.service, "delete-owner", contractbuilds.SubjectDeleteOwner, timed(s.deleteOwner)),
		natsx.AddEndpoint(s.service, "active-builds", contractbuilds.SubjectActiveBuilds, timed(s.activeBuilds)),
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

// Stop stops answering and taking Builds, cancels the running ones, which
// JetStream hands out again later, publishes what is left of the outbox, and
// closes builds.db.
func (s *Service) Stop(ctx context.Context) error {
	s.closing.Store(true)
	var errs []error
	if s.service != nil {
		errs = append(errs, s.service.Stop())
	}
	for _, worker := range s.workers {
		worker.Stop()
	}
	if s.events != nil {
		s.events.Drain()
	}
	if s.accountEvents != nil {
		s.accountEvents.Drain()
	}
	if s.stopRuns != nil {
		s.stopRuns()
	}
	waited := make(chan struct{})
	go func() { s.wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("wait for running builds: %w", ctx.Err()))
	}
	if s.relay != nil {
		errs = append(errs, s.relay.Stop(ctx))
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
	msg := &nats.Msg{Subject: contractaudit.Subject(contractbuilds.ServiceName), Data: data}
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

func conflict(format string, args ...any) error {
	return contract.Errorf(contract.StatusConflict, format, args...)
}
