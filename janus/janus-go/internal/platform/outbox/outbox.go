// Package outbox publishes a service's events and jobs from its own database,
// so a message is sent if and only if the change it describes was stored.
//
// A service adds a message to its outbox table in the transaction that makes
// the change, and a Relay publishes the table to JetStream afterwards, with the
// message's ID as Nats-Msg-Id. A message is deleted only after JetStream
// acknowledged it, so it is never lost; it can be published twice when the
// process stops between the two, which the stream's duplicate window and the
// consumers' idempotence absorb.
package outbox

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Schema creates the outbox table. A service puts it in its migrations.
const Schema = `CREATE TABLE outbox (
	seq INTEGER PRIMARY KEY,
	subject TEXT NOT NULL,
	msg_id TEXT NOT NULL,
	data BLOB NOT NULL
);`

// Execer is what a transaction offers; Add takes it so it runs inside the
// caller's.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Add queues data for subject. msgID identifies the message for duplicate
// detection: the same change must produce the same ID.
func Add(ctx context.Context, tx Execer, subject, msgID string, data []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO outbox(subject,msg_id,data) VALUES(?,?,?)`, subject, msgID, data)
	return err
}

// The Relay's pacing.
const (
	batch        = 100
	idleInterval = 2 * time.Second
	publishWait  = 5 * time.Second
)

// Relay publishes the outbox of one database, in the order messages were
// added. It wakes when Kick is called and every couple of seconds, so a
// message is published soon after its transaction commits, and those whose
// publish failed are retried.
type Relay struct {
	db   *sql.DB
	js   jetstream.JetStream
	kick chan struct{}

	stop    context.CancelFunc
	done    chan struct{}
	errorMu sync.Mutex
	lastErr error
}

// NewRelay returns a stopped Relay that publishes db's outbox through js.
func NewRelay(db *sql.DB, js jetstream.JetStream) *Relay {
	return &Relay{db: db, js: js, kick: make(chan struct{}, 1)}
}

// Start publishes until Stop.
func (r *Relay) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.stop, r.done = cancel, make(chan struct{})
	go r.run(ctx)
}

// Kick asks the Relay to publish now. Call it after committing a transaction
// that added messages. It never blocks.
func (r *Relay) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Stop publishes what is left, within ctx, and stops. What is not published
// stays in the table for the next start.
func (r *Relay) Stop(ctx context.Context) error {
	if r.stop == nil {
		return nil
	}
	r.stop()
	select {
	case <-r.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	flushCtx, cancel := context.WithTimeout(ctx, publishWait)
	defer cancel()
	_, err := r.Flush(flushCtx)
	return err
}

// Err returns the last publish failure, or nil once a publish succeeded since.
func (r *Relay) Err() error {
	r.errorMu.Lock()
	defer r.errorMu.Unlock()
	return r.lastErr
}

func (r *Relay) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(idleInterval)
	defer ticker.Stop()
	for {
		n, err := r.Flush(ctx)
		r.errorMu.Lock()
		r.lastErr = err
		r.errorMu.Unlock()
		if err == nil && n == batch {
			continue // more is waiting
		}
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
		case <-ticker.C:
		}
	}
}

type message struct {
	seq         int64
	subject, id string
	data        []byte
}

// Flush publishes up to one batch of the outbox in order and returns how many
// it published. It stops at the first failure, so order is kept.
func (r *Relay) Flush(ctx context.Context) (int, error) {
	pending, err := r.pending(ctx)
	if err != nil {
		return 0, err
	}
	for i, m := range pending {
		publishCtx, cancel := context.WithTimeout(ctx, publishWait)
		msg := &nats.Msg{Subject: m.subject, Data: m.data, Header: nats.Header{}}
		_, err = r.js.PublishMsg(publishCtx, msg, jetstream.WithMsgID(m.id))
		cancel()
		if err != nil {
			return i, err
		}
		if _, err = r.db.ExecContext(ctx, `DELETE FROM outbox WHERE seq=?`, m.seq); err != nil {
			return i, err
		}
	}
	return len(pending), nil
}

func (r *Relay) pending(ctx context.Context) ([]message, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT seq,subject,msg_id,data FROM outbox ORDER BY seq LIMIT ?`, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []message
	for rows.Next() {
		var m message
		if err := rows.Scan(&m.seq, &m.subject, &m.id, &m.data); err != nil {
			return nil, err
		}
		pending = append(pending, m)
	}
	return pending, rows.Err()
}
