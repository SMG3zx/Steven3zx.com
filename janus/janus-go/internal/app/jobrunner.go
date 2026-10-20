package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrBusy means every job slot is taken.
var ErrBusy = errors.New("all job slots are in use")

// JobRunner runs each job on its own goroutine, never more than the limit at
// once, and counts jobs so a shutdown can wait for them.
//
// A job that panics does not take the process down: the runner recovers it,
// frees its slot and tells the job's crashed function, which records the job as
// failed.
type JobRunner struct {
	slots     chan struct{}
	jobs      sync.WaitGroup
	accepting atomic.Bool
	pending   atomic.Int64

	mu      sync.Mutex
	running map[uint64]trackedJob
	nextID  uint64
}

// trackedJob is a running job's cancel function and the Account it belongs to.
type trackedJob struct {
	owner  string
	cancel context.CancelFunc
}

// NewJobRunner returns a JobRunner that runs at most limit jobs at once. It
// accepts jobs at once; call Drain to stop.
func NewJobRunner(limit int) *JobRunner {
	r := &JobRunner{slots: make(chan struct{}, limit), running: map[uint64]trackedJob{}}
	r.accepting.Store(true)
	return r
}

// Accepting reports whether Drain or StopAccepting has not been called.
func (r *JobRunner) Accepting() bool { return r.accepting.Load() }

// StopAccepting makes the runner refuse new jobs. Running jobs carry on.
func (r *JobRunner) StopAccepting() { r.accepting.Store(false) }

// Pending is the number of jobs that have been started or queued and have not
// yet finished.
func (r *JobRunner) Pending() int { return int(r.pending.Load()) }

// Start runs job on its own goroutine if a slot is free, and returns ErrBusy
// otherwise. It does not block. When Start returns an error the job has not
// started and holds no slot. If job panics, crashed is called with the panic as
// an error, after the slot is freed; crashed may be nil.
func (r *JobRunner) Start(job func(), crashed func(error)) error {
	select {
	case r.slots <- struct{}{}:
	default:
		return ErrBusy
	}
	r.jobs.Add(1)
	r.pending.Add(1)
	go r.run(job, crashed)
	return nil
}

// Queue runs job once a slot is free, so a burst of jobs never exceeds the
// limit. It does not block the caller. A job that is still waiting when the
// runner stops accepting is dropped without running, and so stays queued in the
// Store for the next start to resume.
func (r *JobRunner) Queue(job func(), crashed func(error)) {
	r.jobs.Add(1)
	r.pending.Add(1)
	go func() {
		r.slots <- struct{}{}
		if !r.accepting.Load() {
			r.finish()
			return
		}
		r.run(job, crashed)
	}()
}

func (r *JobRunner) run(job func(), crashed func(error)) {
	defer r.finish()
	defer func() {
		if recovered := recover(); recovered != nil && crashed != nil {
			crashed(fmt.Errorf("job panicked: %v", recovered))
		}
	}()
	job()
}

// finish frees the slot of a job that ended, and counts the job as done.
func (r *JobRunner) finish() {
	r.pending.Add(-1)
	<-r.slots
	r.jobs.Done()
}

// Track returns a context for a running job that CancelOwner can cancel, and a
// release function the job calls when it ends. The context ends when parent
// does, too.
func (r *JobRunner) Track(parent context.Context, owner string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.running[id] = trackedJob{owner: owner, cancel: cancel}
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
func (r *JobRunner) CancelOwner(owner string) int {
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

// Drain stops accepting jobs and waits for the running ones, or until ctx ends.
func (r *JobRunner) Drain(ctx context.Context) error {
	r.accepting.Store(false)
	return r.Wait(ctx)
}

// Wait waits for every started and queued job to finish, or until ctx ends. It
// does not stop the runner accepting jobs.
func (r *JobRunner) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() { r.jobs.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
