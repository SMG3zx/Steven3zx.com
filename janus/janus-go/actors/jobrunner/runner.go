package jobrunner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

// ErrBusy means every job slot is taken.
var ErrBusy = errors.New("all job slots are in use")

// Runner starts each job on its own worker actor, never more than the limit at
// once, and counts jobs so a shutdown can wait for them.
//
// A worker must call Finish exactly once when its job ends, whatever the outcome.
// That frees the slot and counts the job as done.
type Runner struct {
	slots     chan struct{}
	jobs      sync.WaitGroup
	accepting atomic.Bool
	pending   atomic.Int64

	mu      sync.Mutex
	running map[uint64]tracked
	nextID  uint64
}

// tracked is a running job's cancel function and the Account it belongs to.
type tracked struct {
	owner  string
	cancel context.CancelFunc
}

// New returns a Runner that runs at most limit jobs at once. It accepts jobs at
// once; call Drain to stop.
func New(limit int) *Runner {
	r := &Runner{slots: make(chan struct{}, limit), running: map[uint64]tracked{}}
	r.accepting.Store(true)
	return r
}

// Accepting reports whether Drain or StopAccepting has not been called.
func (r *Runner) Accepting() bool { return r.accepting.Load() }

// StopAccepting makes the runner refuse new jobs. Running jobs carry on.
func (r *Runner) StopAccepting() { r.accepting.Store(false) }

// Pending is the number of jobs that have started and not yet finished.
func (r *Runner) Pending() int { return int(r.pending.Load()) }

// Await calls pending every interval until it reports zero, or until ctx ends.
// An actor answers "how many jobs are left" at once, so waiting for a drain is
// a poll from the caller instead of a handler that blocks its dispatcher worker.
func Await(ctx context.Context, interval time.Duration, pending func(context.Context) (int, error)) error {
	for {
		left, err := pending(ctx)
		if err != nil {
			return err
		}
		if left == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Start runs msg on worker if a slot is free, and returns ErrBusy otherwise.
// It does not block. The worker is spawned under name, which must be unique.
// When Start returns an error the job has not started and holds no slot.
func (r *Runner) Start(ctx context.Context, system actor.ActorSystem, name string, worker actor.Actor, msg any) error {
	select {
	case r.slots <- struct{}{}:
	default:
		return ErrBusy
	}
	if err := r.spawn(ctx, system, name, worker, msg); err != nil {
		<-r.slots
		return err
	}
	return nil
}

// Queue runs msg on worker once a slot is free, so a burst of jobs never
// exceeds the limit. It does not block the caller. onFail is called if the job
// could not be started.
func (r *Runner) Queue(ctx context.Context, system actor.ActorSystem, name string, worker actor.Actor, msg any, onFail func(error)) {
	go func() {
		r.slots <- struct{}{}
		if err := r.spawn(context.WithoutCancel(ctx), system, name, worker, msg); err != nil {
			<-r.slots
			onFail(err)
		}
	}()
}

// Track returns a context for a running job that CancelOwner can cancel, and a
// release function the job calls when it ends. The context ends when parent
// does, too.
func (r *Runner) Track(parent context.Context, owner string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.running[id] = tracked{owner: owner, cancel: cancel}
	r.mu.Unlock()
	return ctx, func() {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
		cancel()
	}
}

// CancelOwner cancels the context of every tracked job that belongs to owner and
// returns how many it cancelled.
func (r *Runner) CancelOwner(owner string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, job := range r.running {
		if job.owner == owner {
			job.cancel()
			count++
		}
	}
	return count
}

// Finish is called by a worker when its job ends.
func (r *Runner) Finish() {
	r.pending.Add(-1)
	<-r.slots
	r.jobs.Done()
}

// Drain stops accepting jobs and waits for the running ones, or until ctx ends.
func (r *Runner) Drain(ctx context.Context) error {
	r.accepting.Store(false)
	done := make(chan struct{})
	go func() { r.jobs.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runner) spawn(ctx context.Context, system actor.ActorSystem, name string, worker actor.Actor, msg any) error {
	r.jobs.Add(1)
	r.pending.Add(1)
	pid, err := system.Spawn(ctx, name, worker)
	if err != nil {
		r.pending.Add(-1)
		r.jobs.Done()
		return err
	}
	if err = actor.Tell(context.WithoutCancel(ctx), pid, msg); err != nil {
		r.pending.Add(-1)
		r.jobs.Done()
		return err
	}
	return nil
}
