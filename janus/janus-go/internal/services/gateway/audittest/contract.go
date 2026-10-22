package audittest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	contractaudit "janus/internal/contract/audit"
)

// Run runs the Audit contract against adapters made by newAudit. The factory
// returns the Audit and a function that reads back the events it has written,
// oldest first. An adapter that writes asynchronously may return fewer events
// than were recorded for a short while; the suite waits up to two seconds for
// the rest. Each case gets a fresh Audit.
func Run[A recorder](t *testing.T, newAudit func(t *testing.T) (audit A, events func() []contractaudit.Event)) {
	t.Helper()
	for _, c := range []struct {
		name string
		test func(t *testing.T, audit recorder, events func(want int) []contractaudit.Event)
	}{
		{"RecordsLevelNameAndFields", testRecordsAnEvent},
		{"RecordsEventsInOrder", testRecordsInOrder},
		{"RecordsAnEventWithNoFields", testNoFields},
		{"FallsBackToInfoAndAGenericName", testFallbacks},
		{"RecordsFromManyGoroutines", testConcurrent},
	} {
		t.Run(c.name, func(t *testing.T) {
			audit, read := newAudit(t)
			c.test(t, audit, func(want int) []contractaudit.Event { return waitFor(t, read, want) })
		})
	}
}

// waitFor returns the events once there are want of them, or whatever there is
// when two seconds have passed.
func waitFor(t *testing.T, read func() []contractaudit.Event, want int) []contractaudit.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		events := read()
		if len(events) >= want || time.Now().After(deadline) {
			return events
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func testRecordsAnEvent(t *testing.T, audit recorder, events func(int) []contractaudit.Event) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		audit.Record(context.Background(), contractaudit.Event{Level: level, Name: "event." + level, Fields: map[string]string{"user": "alice", "repository": "alice/repo"}})
	}
	got := events(4)
	if len(got) != 4 {
		t.Fatalf("recorded %d events, want 4", len(got))
	}
	for i, level := range []string{"debug", "info", "warn", "error"} {
		if got[i].Level != level || got[i].Name != "event."+level || got[i].Fields["user"] != "alice" || got[i].Fields["repository"] != "alice/repo" || len(got[i].Fields) != 2 {
			t.Errorf("event %d = %+v", i, got[i])
		}
	}
}

func testRecordsInOrder(t *testing.T, audit recorder, events func(int) []contractaudit.Event) {
	for i := range 20 {
		audit.Record(context.Background(), contractaudit.Event{Level: "info", Name: fmt.Sprintf("event.%02d", i)})
	}
	got := events(20)
	if len(got) != 20 {
		t.Fatalf("recorded %d events, want 20", len(got))
	}
	for i, event := range got {
		if want := fmt.Sprintf("event.%02d", i); event.Name != want {
			t.Fatalf("event %d is %q, want %q", i, event.Name, want)
		}
	}
}

func testNoFields(t *testing.T, audit recorder, events func(int) []contractaudit.Event) {
	audit.Record(context.Background(), contractaudit.Event{Level: "info", Name: "bare"})
	if got := events(1); len(got) != 1 || got[0].Name != "bare" || len(got[0].Fields) != 0 {
		t.Fatalf("events = %+v", got)
	}
}

func testFallbacks(t *testing.T, audit recorder, events func(int) []contractaudit.Event) {
	audit.Record(context.Background(), contractaudit.Event{Name: "no level"})
	audit.Record(context.Background(), contractaudit.Event{Level: "shout", Name: "odd level"})
	audit.Record(context.Background(), contractaudit.Event{})
	got := events(3)
	if len(got) != 3 {
		t.Fatalf("recorded %d events, want 3", len(got))
	}
	if got[0].Level != "info" || got[1].Level != "info" {
		t.Errorf("levels = %q and %q, want info", got[0].Level, got[1].Level)
	}
	if got[2].Level != "info" || got[2].Name == "" {
		t.Errorf("event with no level and no name = %+v, want info and a generic name", got[2])
	}
}

func testConcurrent(t *testing.T, audit recorder, events func(int) []contractaudit.Event) {
	const writers, each = 8, 25
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for i := range each {
				audit.Record(context.Background(), contractaudit.Event{Level: "info", Name: "concurrent", Fields: map[string]string{"writer": fmt.Sprint(w), "n": fmt.Sprint(i)}})
			}
		})
	}
	wg.Wait()
	if got := events(writers * each); len(got) != writers*each {
		t.Fatalf("recorded %d events, want %d", len(got), writers*each)
	}
}
