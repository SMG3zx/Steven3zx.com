package outbox_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
	"janus/internal/platform/outbox"
	"janus/internal/platform/tursodb"
)

func setup(t *testing.T) (*sql.DB, jetstream.JetStream, jetstream.Stream) {
	t.Helper()
	ctx := context.Background()
	db, err := tursodb.Open(ctx, filepath.Join(t.TempDir(), "service.db"), []string{outbox.Schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	js, err := natstest.Start(t).JetStream()
	if err != nil {
		t.Fatal(err)
	}
	stream, err := natsx.DeclareEvents(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	return db, js, stream
}

func add(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	ctx := context.Background()
	if err := tursodb.InTx(ctx, db, func(tx *sql.Tx) error {
		return outbox.Add(ctx, tx, "janus.events.test.happened", id, []byte(id))
	}); err != nil {
		t.Fatal(err)
	}
}

func messages(t *testing.T, stream jetstream.Stream) uint64 {
	t.Helper()
	info, err := stream.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

func TestFlushPublishesInOrderAndEmptiesTheTable(t *testing.T) {
	db, js, stream := setup(t)
	for i := range 3 {
		add(t, db, fmt.Sprintf("event-%d", i))
	}
	n, err := outbox.NewRelay(db, js).Flush(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("Flush = %d, %v", n, err)
	}
	if got := messages(t, stream); got != 3 {
		t.Fatalf("stream holds %d messages, want 3", got)
	}
	for seq := uint64(1); seq <= 3; seq++ {
		msg, err := stream.GetMsg(context.Background(), seq)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("event-%d", seq-1); string(msg.Data) != want {
			t.Fatalf("message %d is %q, want %q", seq, msg.Data, want)
		}
	}
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("outbox holds %d rows, %v", left, err)
	}
}

func TestAMessagePublishedTwiceIsStoredOnce(t *testing.T) {
	db, js, stream := setup(t)
	add(t, db, "same")
	add(t, db, "same") // as after a crash between publishing and deleting
	if _, err := outbox.NewRelay(db, js).Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := messages(t, stream); got != 1 {
		t.Fatalf("stream holds %d messages, want 1", got)
	}
}

func TestAFailedPublishKeepsTheMessage(t *testing.T) {
	ctx := context.Background()
	db, err := tursodb.Open(ctx, filepath.Join(t.TempDir(), "service.db"), []string{outbox.Schema})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	js, err := natstest.Start(t).JetStream()
	if err != nil {
		t.Fatal(err)
	}
	add(t, db, "waiting") // no stream captures the subject yet
	relay := outbox.NewRelay(db, js)
	if n, err := relay.Flush(ctx); err == nil || n != 0 {
		t.Fatalf("Flush without a stream = %d, %v, want a failure", n, err)
	}
	stream, err := natsx.DeclareEvents(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := relay.Flush(ctx); err != nil || n != 1 || messages(t, stream) != 1 {
		t.Fatalf("Flush once the stream exists = %d, %v", n, err)
	}
}

func TestTheRelayPublishesAfterAKick(t *testing.T) {
	db, js, _ := setup(t)
	published := make(chan string, 1)
	sub, err := js.Conn().Subscribe("janus.events.test.happened", func(msg *nats.Msg) { published <- string(msg.Data) })
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	relay := outbox.NewRelay(db, js)
	relay.Start()
	defer relay.Stop(context.Background())
	add(t, db, "kicked")
	relay.Kick()
	select {
	case got := <-published:
		if got != "kicked" {
			t.Fatalf("published %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the relay did not publish")
	}
}
