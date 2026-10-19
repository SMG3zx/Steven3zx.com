package jobrunner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"
)

type start struct{}

// worker holds its slot until release is closed, then finishes.
type worker struct {
	runner  *Runner
	started chan<- string
	release <-chan struct{}
	id      string
}

func (*worker) PreStart(*actor.Context) error { return nil }
func (*worker) PostStop(*actor.Context) error { return nil }
func (w *worker) Receive(c *actor.ReceiveContext) {
	if _, ok := c.Message().(*start); !ok {
		c.Unhandled()
		return
	}
	defer c.Shutdown()
	defer w.runner.Finish()
	w.started <- w.id
	<-w.release
}

func testSystem(t *testing.T) actor.ActorSystem {
	t.Helper()
	system, err := actor.NewActorSystem("jobrunner-test", actor.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = system.Stop(context.Background()) })
	return system
}

func waitFor(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("started %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("job %q never started", want)
	}
}

func TestStartRefusesAJobWhenEverySlotIsTaken(t *testing.T) {
	system, ctx := testSystem(t), context.Background()
	runner := New(1)
	started, release := make(chan string, 4), make(chan struct{})
	if err := runner.Start(ctx, system, "job-a", &worker{runner, started, release, "a"}, &start{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, started, "a")
	err := runner.Start(ctx, system, "job-b", &worker{runner, started, release, "b"}, &start{})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	close(release)
	if err = runner.Drain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestQueueStartsAJobOnceASlotFrees(t *testing.T) {
	system, ctx := testSystem(t), context.Background()
	runner := New(1)
	started, release := make(chan string, 4), make(chan struct{})
	fail := func(err error) { t.Errorf("job could not start: %v", err) }
	runner.Queue(ctx, system, "job-a", &worker{runner, started, release, "a"}, &start{}, fail)
	waitFor(t, started, "a")
	runner.Queue(ctx, system, "job-b", &worker{runner, started, release, "b"}, &start{}, fail)
	select {
	case id := <-started:
		t.Fatalf("job %q started while the only slot was taken", id)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	waitFor(t, started, "b")
	if err := runner.Drain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestQueueReportsAJobThatCannotStartAndFreesItsSlot(t *testing.T) {
	system, ctx := testSystem(t), context.Background()
	if err := system.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	runner := New(1)
	started, release := make(chan string, 1), make(chan struct{})
	failed := make(chan error, 1)
	runner.Queue(ctx, system, "job-a", &worker{runner, started, release, "a"}, &start{}, func(err error) { failed <- err })
	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("onFail called with a nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("onFail was never called")
	}
	// The failed job must not hold a slot or block a drain.
	if err := runner.Start(ctx, system, "job-b", &worker{runner, started, release, "b"}, &start{}); errors.Is(err, ErrBusy) {
		t.Fatal("the failed job still holds its slot")
	}
	done, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := runner.Drain(done); err != nil {
		t.Fatalf("Drain = %v, want the failed jobs not counted", err)
	}
}

func TestDrainStopsAcceptingAndWaitsForRunningJobs(t *testing.T) {
	system := testSystem(t)
	runner := New(2)
	started, release := make(chan string, 4), make(chan struct{})
	if err := runner.Start(context.Background(), system, "job-a", &worker{runner, started, release, "a"}, &start{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, started, "a")
	short, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := runner.Drain(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain with a job running = %v, want deadline exceeded", err)
	}
	if runner.Accepting() {
		t.Fatal("still accepting after Drain")
	}
	close(release)
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatalf("Drain after the job ended = %v", err)
	}
}
