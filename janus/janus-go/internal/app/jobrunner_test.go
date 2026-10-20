package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// holdingJob reports that it started, then holds its slot until release is closed.
func holdingJob(id string, started chan<- string, release <-chan struct{}) func() {
	return func() {
		started <- id
		<-release
	}
}

func receive(t *testing.T, ch <-chan string, want string) {
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
	runner := NewJobRunner(1)
	started, release := make(chan string, 4), make(chan struct{})
	if err := runner.Start(holdingJob("a", started, release), nil); err != nil {
		t.Fatal(err)
	}
	receive(t, started, "a")
	if err := runner.Start(holdingJob("b", started, release), nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	close(release)
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestQueueStartsAJobOnceASlotFrees(t *testing.T) {
	runner := NewJobRunner(1)
	started, release := make(chan string, 4), make(chan struct{})
	runner.Queue(holdingJob("a", started, release), nil)
	receive(t, started, "a")
	runner.Queue(holdingJob("b", started, release), nil)
	select {
	case id := <-started:
		t.Fatalf("job %q started while the only slot was taken", id)
	case <-time.After(100 * time.Millisecond):
	}
	if runner.Pending() != 2 {
		t.Fatalf("pending = %d, want the running and the queued job", runner.Pending())
	}
	close(release)
	receive(t, started, "b")
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestQueuedJobsAreDroppedOnceTheRunnerStopsAccepting(t *testing.T) {
	runner := NewJobRunner(1)
	started, release := make(chan string, 4), make(chan struct{})
	runner.Queue(holdingJob("a", started, release), nil)
	receive(t, started, "a")
	runner.Queue(holdingJob("b", started, release), nil)
	runner.StopAccepting()
	close(release)
	if err := runner.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-started:
		t.Fatalf("queued job %q ran after the runner stopped accepting", id)
	default:
	}
}

func TestAPanicFreesTheSlotAndIsReported(t *testing.T) {
	runner := NewJobRunner(1)
	crashed := make(chan error, 1)
	if err := runner.Start(func() { panic("boom") }, func(err error) { crashed <- err }); err != nil {
		t.Fatal(err)
	}
	if err := runner.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-crashed; err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("crashed = %v", err)
	}
	if err := runner.Start(func() {}, nil); err != nil {
		t.Fatalf("the panicked job still holds its slot: %v", err)
	}
	if err := runner.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDrainStopsAcceptingAndWaitsForRunningJobs(t *testing.T) {
	runner := NewJobRunner(2)
	started, release := make(chan string, 4), make(chan struct{})
	if err := runner.Start(holdingJob("a", started, release), nil); err != nil {
		t.Fatal(err)
	}
	receive(t, started, "a")
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
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
	if runner.Pending() != 0 {
		t.Fatalf("pending = %d after Drain", runner.Pending())
	}
}

func TestCancelOwnerCancelsOnlyThatOwnersTrackedJobs(t *testing.T) {
	r := NewJobRunner(2)
	mine, releaseMine := r.Track(context.Background(), "alice")
	other, releaseOther := r.Track(context.Background(), "bob")
	defer releaseMine()
	defer releaseOther()

	if got := r.CancelOwner("alice"); got != 1 {
		t.Fatalf("CancelOwner cancelled %d jobs, want 1", got)
	}
	if mine.Err() == nil {
		t.Error("alice's job was not cancelled")
	}
	if other.Err() != nil {
		t.Error("bob's job was cancelled")
	}
	releaseMine()
	if got := r.CancelOwner("alice"); got != 0 {
		t.Errorf("a released job was cancelled again: %d", got)
	}
}

func TestTheLimitHoldsUnderManyCallers(t *testing.T) {
	const limit = 3
	runner := NewJobRunner(limit)
	var running, peak, accepted int
	var mu sync.Mutex
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			err := runner.Start(func() {
				mu.Lock()
				running++
				peak = max(peak, running)
				mu.Unlock()
				<-release
				mu.Lock()
				running--
				mu.Unlock()
			}, nil)
			if err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	close(release)
	if err := runner.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if accepted != limit || peak > limit {
		t.Fatalf("accepted %d jobs with a peak of %d running, want %d", accepted, peak, limit)
	}
}
