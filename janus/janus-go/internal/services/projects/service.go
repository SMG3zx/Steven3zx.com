// Package projects is the projects service. It owns projects.db: Projects,
// their Snapshots and files, and their Imports. An Import is a job on the JOBS
// stream; a worker clones the commit from GitHub, scans it without running it,
// and stores it as a Snapshot. Its contract is internal/contract/projects.
package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/contract"
	contractaudit "janus/internal/contract/audit"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natsx"
	"janus/internal/platform/outbox"
	"janus/internal/platform/tursodb"
)

// operationTimeout bounds the database and GitHub work of one request.
const operationTimeout = 30 * time.Second

// Config configures the service.
type Config struct {
	// DatabasePath is projects.db.
	DatabasePath string
	// GitHubAPI is the GitHub REST API. Empty means https://api.github.com.
	GitHubAPI string
	// GitBase is prepended to a repository's full name to clone it. It ends in
	// a slash. Empty means https://github.com/.
	GitBase string
	// Workers is how many Imports run at once in this process. Zero means 2.
	Workers int
	// ImportTimeout bounds one Import. Zero means 20 minutes.
	ImportTimeout time.Duration
	// AckWait is how long JetStream waits for a sign of life from an Import's
	// worker before handing the job to another. Zero means 2 minutes.
	AckWait time.Duration
}

// Service answers the projects subjects from projects.db and runs Imports.
type Service struct {
	cfg     Config
	db      *sql.DB
	conn    *nats.Conn
	js      jetstream.JetStream
	service micro.Service
	relay   *outbox.Relay
	github  *gitHub
	source  *gitSource

	workers []jetstream.ConsumeContext
	// running holds the Imports this process is running, so an Owner's can be
	// cancelled and Stop can wait for them.
	mu      sync.Mutex
	running map[string]runningImport
	wg      sync.WaitGroup
	closing atomic.Bool
	// stopCtx ends when Stop starts, cancelling the running Imports, which are
	// then left for JetStream to hand out again.
	stopCtx  context.Context
	stopRuns context.CancelFunc
}

type runningImport struct {
	owner  string
	cancel context.CancelFunc
}

// New returns a stopped service.
func New(cfg Config) *Service {
	if cfg.GitHubAPI == "" {
		cfg.GitHubAPI = "https://api.github.com"
	}
	if cfg.GitBase == "" {
		cfg.GitBase = "https://github.com/"
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.ImportTimeout <= 0 {
		cfg.ImportTimeout = 20 * time.Minute
	}
	if cfg.AckWait <= 0 {
		cfg.AckWait = 2 * time.Minute
	}
	return &Service{
		cfg:     cfg,
		github:  &gitHub{http: &http.Client{Timeout: 15 * time.Second}, base: cfg.GitHubAPI},
		source:  &gitSource{base: cfg.GitBase},
		running: map[string]runningImport{},
	}
}

// Start opens projects.db, declares the streams it uses, starts publishing
// its outbox, answers its subjects and starts its Import workers. A failure
// leaves nothing running.
func (s *Service) Start(ctx context.Context, conn *nats.Conn) (err error) {
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
	s.relay = outbox.NewRelay(s.db, s.js)
	s.relay.Start()
	if s.service, err = micro.AddService(conn, micro.Config{Name: contractprojects.ServiceName, Version: "1.0.0", Description: "Projects, Snapshots and Imports"}); err != nil {
		return fmt.Errorf("register the projects service: %w", err)
	}
	if err = s.addEndpoints(); err != nil {
		return err
	}
	return s.startWorkers(ctx, jobs)
}

func (s *Service) addEndpoints() error {
	return errors.Join(
		natsx.AddEndpoint(s.service, "create-project", contractprojects.SubjectCreateProject, timed(s.createProject)),
		natsx.AddEndpoint(s.service, "update-project", contractprojects.SubjectUpdateProject, timed(s.updateProject)),
		natsx.AddEndpoint(s.service, "delete-project", contractprojects.SubjectDeleteProject, timed(s.deleteProject)),
		natsx.AddEndpoint(s.service, "list-projects", contractprojects.SubjectListProjects, timed(s.listProjects)),
		natsx.AddEndpoint(s.service, "get-project", contractprojects.SubjectGetProject, timed(s.getProject)),
		natsx.AddEndpoint(s.service, "list-snapshots", contractprojects.SubjectListSnapshots, timed(s.listSnapshots)),
		natsx.AddEndpoint(s.service, "activate-snapshot", contractprojects.SubjectActivateSnapshot, timed(s.activateSnapshot)),
		natsx.AddEndpoint(s.service, "set-entrypoint", contractprojects.SubjectSetEntryPoint, timed(s.setEntryPoint)),
		natsx.AddEndpoint(s.service, "snapshot-files", contractprojects.SubjectSnapshotFiles, timed(s.snapshotFiles)),
		natsx.AddEndpoint(s.service, "request-import", contractprojects.SubjectRequestImport, timed(s.requestImport)),
		natsx.AddEndpoint(s.service, "retry-import", contractprojects.SubjectRetryImport, timed(s.retryImport)),
		natsx.AddEndpoint(s.service, "find-import", contractprojects.SubjectFindImport, timed(s.findImport)),
		natsx.AddEndpoint(s.service, "active-imports", contractprojects.SubjectActiveImports, timed(s.activeImports)),
		natsx.AddEndpoint(s.service, "delete-owner", contractprojects.SubjectDeleteOwner, timed(s.deleteOwner)),
		natsx.AddEndpoint(s.service, "repositories", contractprojects.SubjectRepositories, timed(s.repositories)),
		natsx.AddEndpoint(s.service, "branches", contractprojects.SubjectBranches, timed(s.branches)),
		natsx.AddEndpoint(s.service, "commits", contractprojects.SubjectCommits, timed(s.commits)),
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

// Stop stops answering and taking Imports, cancels the running ones, which
// JetStream hands out again later, publishes what is left of the outbox, and
// closes projects.db.
func (s *Service) Stop(ctx context.Context) error {
	s.closing.Store(true)
	var errs []error
	if s.service != nil {
		errs = append(errs, s.service.Stop())
	}
	for _, worker := range s.workers {
		worker.Stop()
	}
	if s.stopRuns != nil {
		s.stopRuns()
	}
	waited := make(chan struct{})
	go func() { s.wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("wait for running imports: %w", ctx.Err()))
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
	msg := &nats.Msg{Subject: contractaudit.Subject(contractprojects.ServiceName), Data: data}
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
