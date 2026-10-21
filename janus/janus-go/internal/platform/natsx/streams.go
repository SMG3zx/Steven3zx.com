package natsx

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The streams every service shares, from section 5 of docs/architecture.md.
// Each service declares the ones it uses when it starts; declaring one that
// exists with the same settings changes nothing.
const (
	// EventsStream keeps every event for a week, for the services that react
	// to them.
	EventsStream   = "EVENTS"
	EventsSubjects = "janus.events.>"
	// JobsStream holds work that must outlive a restart until a worker
	// acknowledges it. Each kind of job has its own subject and consumer.
	JobsStream   = "JOBS"
	JobsSubjects = "janus.jobs.>"
)

// eventsRetention is how long EVENTS keeps an event.
const eventsRetention = 7 * 24 * time.Hour

// duplicateWindow is how long a stream remembers a Nats-Msg-Id, so the same
// event or job published twice within it is stored once.
const duplicateWindow = 10 * time.Minute

// DeclareEvents creates or updates EVENTS.
func DeclareEvents(ctx context.Context, js jetstream.JetStream) (jetstream.Stream, error) {
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       EventsStream,
		Subjects:   []string{EventsSubjects},
		Retention:  jetstream.LimitsPolicy,
		MaxAge:     eventsRetention,
		Storage:    jetstream.FileStorage,
		Discard:    jetstream.DiscardOld,
		Duplicates: duplicateWindow,
	})
	if err != nil {
		return nil, fmt.Errorf("declare the %s stream: %w", EventsStream, err)
	}
	return stream, nil
}

// DeclareJobs creates or updates JOBS. A job leaves it when its worker
// acknowledges it.
func DeclareJobs(ctx context.Context, js jetstream.JetStream) (jetstream.Stream, error) {
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       JobsStream,
		Subjects:   []string{JobsSubjects},
		Retention:  jetstream.WorkQueuePolicy,
		Storage:    jetstream.FileStorage,
		Duplicates: duplicateWindow,
	})
	if err != nil {
		return nil, fmt.Errorf("declare the %s stream: %w", JobsStream, err)
	}
	return stream, nil
}
