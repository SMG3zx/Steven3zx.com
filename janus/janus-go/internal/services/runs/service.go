// Package runs is the runs service. It runs one-off WASI WebAssembly Builds of an
// Account with Wasmtime, in the process, and keeps the newest Runs in memory. It
// owns no database: a Build's Artifact is read from the artifacts object store
// after the builds service confirms the Owner, and the resource limits come from
// the template of the Build's Snapshot, which the projects service knows. Its
// contract is internal/contract/runs.
package runs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/contract"
	contractruns "janus/internal/contract/runs"
	"janus/internal/platform/natsx"
	"janus/internal/platform/wasmrun"
)

// operationTimeout bounds the work of one request, including the calls to the
// builds and projects services it makes.
const operationTimeout = 30 * time.Second

// The limits of the Runs.
const (
	// maxConcurrentRuns is how many WASM Runs may be active at once.
	maxConcurrentRuns = 2
	// maxRunTable is how many Runs are kept; the oldest is evicted for a new one.
	maxRunTable = 100
	// maxRunArgs and maxRunArgBytes bound the WASI arguments of a Run.
	maxRunArgs     = 64
	maxRunArgBytes = 4096
)

// Config configures the service. The service has no configuration: it owns no
// database, and its Runs are kept in memory.
type Config struct{}

// Service answers the runs subjects and executes Runs.
type Service struct {
	conn      *nats.Conn
	service   micro.Service
	artifacts jetstream.ObjectStore

	active   atomic.Int32
	draining atomic.Bool
	// jobs waits for the Runs that are executing, so Stop can wait for them.
	jobs sync.WaitGroup

	// mu guards the table of Runs and the owner of each.
	mu     sync.Mutex
	runs   map[string]record
	issued uint64
}

// record is a Run and the Owner it belongs to.
type record struct {
	owner string
	// seq orders the Runs by when they started, so the oldest is evicted first.
	seq uint64
	run contractruns.Run
}

// New returns a stopped service.
func New(Config) *Service {
	return &Service{runs: map[string]record{}}
}

// Start declares the artifacts store it reads and answers the runs subjects. A
// failure leaves nothing running.
func (s *Service) Start(ctx context.Context, conn *nats.Conn) (err error) {
	s.conn = conn
	js, err := jetstream.New(conn)
	if err != nil {
		return fmt.Errorf("open JetStream: %w", err)
	}
	if s.artifacts, err = natsx.DeclareObjectStore(ctx, js, natsx.ArtifactsBucket); err != nil {
		return err
	}
	if s.service, err = micro.AddService(conn, micro.Config{Name: contractruns.ServiceName, Version: "1.0.0", Description: "One-off WASM runs"}); err != nil {
		return fmt.Errorf("register the runs service: %w", err)
	}
	if err = s.addEndpoints(); err != nil {
		_ = s.service.Stop()
		return err
	}
	return nil
}

func (s *Service) addEndpoints() error {
	return errors.Join(
		natsx.AddEndpoint(s.service, "request-run", contractruns.SubjectRequestRun, timed(s.requestRun)),
		natsx.AddEndpoint(s.service, "get-run", contractruns.SubjectGetRun, timed(s.getRun)),
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

// Stop stops answering, refuses new Runs and waits for the active ones, or until
// ctx ends.
func (s *Service) Stop(ctx context.Context) error {
	s.draining.Store(true)
	var errs []error
	if s.service != nil {
		errs = append(errs, s.service.Stop())
	}
	if err := s.wait(ctx); err != nil {
		errs = append(errs, fmt.Errorf("wait for running WASM runs: %w", err))
	}
	return errors.Join(errs...)
}

// wait waits for the executing Runs, or until ctx ends.
func (s *Service) wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() { s.jobs.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// requestRun starts a Run of one of the Owner's complete WASI Builds and answers
// with its ID. The Run itself happens in the background.
func (s *Service) requestRun(ctx context.Context, request contractruns.RequestRun) (contractruns.RunAccepted, error) {
	if s.draining.Load() {
		return contractruns.RunAccepted{}, contract.Errorf(contract.StatusUnavailable, "WASM runtime is shutting down")
	}
	if !wasmrun.Available() {
		return contractruns.RunAccepted{}, contract.Errorf(contract.StatusUnavailable, "Wasmtime runtime unavailable: enable CGO and a supported native toolchain")
	}
	if len(request.Args) > maxRunArgs {
		return contractruns.RunAccepted{}, contract.Errorf(contract.StatusInvalid, "at most 64 WASI arguments are allowed")
	}
	for _, arg := range request.Args {
		if len(arg) > maxRunArgBytes {
			return contractruns.RunAccepted{}, contract.Errorf(contract.StatusInvalid, "WASI argument exceeds 4096 bytes")
		}
	}
	if !s.acquire() {
		return contractruns.RunAccepted{}, contract.Errorf(contract.StatusConflict, "two WASM runs are already active")
	}
	started := false
	defer func() {
		if !started {
			s.active.Add(-1)
		}
	}()
	build, err := s.runnableBuild(ctx, request.OwnerID, request.BuildID)
	if err != nil {
		return contractruns.RunAccepted{}, err
	}
	name, data, err := s.artifact(ctx, request.OwnerID, build.ID)
	if err != nil {
		return contractruns.RunAccepted{}, err
	}
	limits := s.limits(ctx, request.OwnerID, build.ProjectID, build.SnapshotID)

	id := s.newRun(request.OwnerID, request.BuildID)
	started = true
	s.jobs.Add(1)
	go s.execute(id, name, data, append([]string(nil), request.Args...), limits)
	return contractruns.RunAccepted{RunID: id}, nil
}

// getRun returns one of the Owner's Runs. It is absent when the Owner has no
// such Run, not an error.
func (s *Service) getRun(_ context.Context, key contractruns.RunKey) (contractruns.RunFound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.runs[key.RunID]
	if !ok || rec.owner != key.OwnerID {
		return contractruns.RunFound{}, nil
	}
	return contractruns.RunFound{Found: true, Run: rec.run}, nil
}

// acquire takes one of the active-Run slots, and reports false when none is free.
func (s *Service) acquire() bool {
	for {
		n := s.active.Load()
		if n >= maxConcurrentRuns {
			return false
		}
		if s.active.CompareAndSwap(n, n+1) {
			return true
		}
	}
}
