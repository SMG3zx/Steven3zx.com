package wasmruntime

import (
	"testing"
	"time"
)

func spin(d time.Duration) error {
	for end := time.Now().Add(d); time.Now().Before(end); {
	}
	return nil
}

func TestMeasureCallCountsCPUTimeSpentWorking(t *testing.T) {
	if _, ok := threadCPUTime(); !ok {
		t.Skip("thread CPU time is not available on this platform")
	}
	wall, cpu, err := measureCall(func() error { return spin(300 * time.Millisecond) })
	if err != nil {
		t.Fatal(err)
	}
	if cpu < 150*time.Millisecond {
		t.Fatalf("cpu = %v after %v of work, want at least 150ms", cpu, wall)
	}
	// One thread cannot use more CPU than the wall time it ran for. Allow for
	// the coarse scheduler tick Windows reports in.
	if cpu > wall+50*time.Millisecond {
		t.Fatalf("cpu = %v is more than wall = %v", cpu, wall)
	}
}

func TestMeasureCallDoesNotCountTimeSpentWaiting(t *testing.T) {
	if _, ok := threadCPUTime(); !ok {
		t.Skip("thread CPU time is not available on this platform")
	}
	wall, cpu, err := measureCall(func() error { time.Sleep(300 * time.Millisecond); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if wall < 300*time.Millisecond {
		t.Fatalf("wall = %v, want at least the 300ms slept", wall)
	}
	if cpu > 100*time.Millisecond {
		t.Fatalf("cpu = %v after sleeping, want it far below the %v of wall time", cpu, wall)
	}
}

func TestMeasureCallReturnsTheCallsError(t *testing.T) {
	want := errTest("boom")
	_, _, err := measureCall(func() error { return want })
	if err != want {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
