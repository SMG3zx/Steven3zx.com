package jobrunner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

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
}

// New returns a Runner that runs at most limit jobs at once. It accepts jobs at
// once; call Drain to stop.
func New(limit int) *Runner {
	r := &Runner{slots: make(chan struct{}, limit)}
	r.accepting.Store(true)
	return r
}

// Accepting reports whether Drain has not been called.
func (r *Runner) Accepting() bool { return r.accepting.Load() }

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

// Finish is called by a worker when its job ends.
func (r *Runner) Finish() {
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
	pid, err := system.Spawn(ctx, name, worker)
	if err != nil {
		r.jobs.Done()
		return err
	}
	if err = actor.Tell(context.WithoutCancel(ctx), pid, msg); err != nil {
		r.jobs.Done()
		return err
	}
	return nil
}
