# Writing audit events through the OpenTelemetry Go logs SDK into Turso

Research for Linear **ENG-86** (parent map ENG-80, project Origin Admin). Terms follow
`portfolio/admin/GLOSSARY.md`: **admin**, **session**, **authenticator code**,
**reconfirmation**, **action**, **audit event**, **request trace**.

Researched 2026-10-19 against the published modules (read from the Go module cache after
`go get`), the OpenTelemetry specification and semantic conventions on opentelemetry.io, and
Turso's `COMPAT.md`. The exporter sketch below was compiled and run against
`turso.tech/database/tursogo v0.8.3` with Go 1.27.1.

## Answer in short

- **The Go logs API and SDK went stable on 2026-10-02 in v1.47.0.** Pin
  `go.opentelemetry.io/otel{,/log,/sdk,/sdk/log,/trace} v1.47.0` and import
  `go.opentelemetry.io/otel/semconv/v1.43.0`. They need Go 1.26 or later; we are on 1.27.
- **Use the `SimpleProcessor` and not the `BatchProcessor`.** The batch processor queues in
  memory, drops the oldest record when the queue is full, and exports on a 1 s timer, so a
  crash loses events. With the simple processor, `Emit` returns only after our exporter has
  committed the row.
- **`Logger.Emit` has no error return.** Export errors go to the global error handler
  (`otel.Handle`). To fail closed (no destructive action without its audit event), wrap the
  simple processor so it writes the export error into a slot carried on the `Emit` context.
  The sketch below does this and was tested.
- **Use one STRICT `audit_events` table.** It has real indexed columns for time, event
  name, outcome, actor and session. All attributes and the resource are stored as JSON
  columns. Update and delete are blocked by triggers, which makes every audit event
  permanent.
- **Use the semantic conventions where they exist:** the LogRecord `EventName` field (the
  `event.name` attribute is deprecated), `user.id`, `user.name`, `session.id`,
  `session.previous_id`, `client.address`, `user_agent.original` and `error.type`. All of
  these are still *Development* stability. OpenTelemetry has no semantic conventions for
  audit or security events, so our own attributes use the `origin_admin.` prefix.
- **The same pattern works for span ingest.** `sdktrace.SpanExporter` has the same shape,
  and both exporters can share the Turso `*sql.DB` and the JSON attribute encoder. Request
  traces should use the batch processor and a separate table, because losing some of them
  is acceptable.

## 1. Stability, API, and the versions to pin

| Module | Version | Stability |
|---|---|---|
| `go.opentelemetry.io/otel` | v1.47.0 | stable |
| `go.opentelemetry.io/otel/log` | v1.47.0 | **stable (new)** |
| `go.opentelemetry.io/otel/sdk` | v1.47.0 | stable |
| `go.opentelemetry.io/otel/sdk/log` | v1.47.0 | **stable (new)** |
| `go.opentelemetry.io/otel/trace` | v1.47.0 | stable |
| `go.opentelemetry.io/otel/semconv/v1.43.0` | package inside `otel` v1.47.0 | newest semconv package shipped |

- The 1.47.0 entry in `CHANGELOG.md`, dated 2026-10-02, reads: "This release contains the
  first stable release of the OpenTelemetry Go Logs API and SDK", and it lists
  `go.opentelemetry.io/otel/log` and `go.opentelemetry.io/otel/sdk/log`
  ([CHANGELOG](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/CHANGELOG.md)).
  `go list -m -versions` shows both modules jumping from `v0.22.0` straight to
  `v1.47.0-rc.1` and then `v1.47.0`.
- Go 1.25 support was dropped in 1.47.0-rc.1, and the go.mod files declare `go 1.26.0`.
  The admin server's `go 1.27.1` is fine.
- `go.opentelemetry.io/otel/log/global` is **deprecated** in favour of `otel.Logger`,
  `otel.GetLoggerProvider` and `otel.SetLoggerProvider`
  ([CHANGELOG 1.47.0-rc.1](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/CHANGELOG.md)).
  We should not need a global at all. Pass the `log.Logger` into the audit package
  explicitly.
- **API shape at v1.47.0.** These details are newer than many blog posts. `log.Record`
  holds `attribute.Value` and `attribute.KeyValue` from the core `attribute` package, which
  now has `MapValue` and `SliceValue`. The old `log.Value` and `log.KeyValue` types are gone.
  Setters are `SetEventName`, `SetTimestamp`, `SetObservedTimestamp`, `SetSeverity`,
  `SetSeverityText`, `SetBody`, `SetErr` and `AddAttributes`
  ([log/record.go](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/log/record.go),
  [pkg.go.dev](https://pkg.go.dev/go.opentelemetry.io/otel/log@v1.47.0)).
- SDK defaults
  ([sdk/log/provider.go](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/sdk/log/provider.go)):
  - attribute count limit 128 (`OTEL_LOGRECORD_ATTRIBUTE_COUNT_LIMIT`)
  - no value length limit (`OTEL_LOGRECORD_ATTRIBUTE_VALUE_LENGTH_LIMIT`)
  - value depth limit 64 (new in 1.47.0)

  Records that go over these limits are truncated or dropped silently, and only
  `Record.DroppedAttributes()` reports it. We store that count.
- The SDK sets **ObservedTimestamp** to `time.Now()` when it is zero. It does **not** set
  Timestamp, so the audit facade must set it (`newRecord` in
  [sdk/log/logger.go](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/sdk/log/logger.go)).
  Trace ID, span ID and trace flags are copied from the span in the `Emit` context.

`go.mod` additions:

```
require (
	go.opentelemetry.io/otel v1.47.0
	go.opentelemetry.io/otel/log v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
	go.opentelemetry.io/otel/sdk/log v1.47.0
	go.opentelemetry.io/otel/trace v1.47.0
)
```

## 2. The exporter contract, choice of processor, and durability

### `sdklog.Exporter`

The interface has three methods: `Export(ctx, []Record) error`, `Shutdown(ctx) error` and
`ForceFlush(ctx) error`
([sdk/log/exporter.go](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/sdk/log/exporter.go)).
The documented rules are:

- Honour the context deadline or cancellation.
- **All retry logic belongs in `Export`.** The SDK never retries, and every error is
  treated as unrecoverable and sent to the error handler.
- Do not keep the `records` slice. Call `Record.Clone` before modifying a record.
- After `Shutdown`, `Export` should return `sdklog.ErrExporterShutdown` (added in 1.46.0),
  and further `Shutdown` or `ForceFlush` calls do nothing.
- `Export` is never called concurrently with itself, but it can run at the same time as
  the other methods.

### Simple processor or batch processor

| | `NewSimpleProcessor` | `NewBatchProcessor` |
|---|---|---|
| When it exports | Inside `OnEmit`, synchronously, under a mutex ([simple.go](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/sdk/log/simple.go)) | Queues the record. A background goroutine exports at least every 1 s (`OTEL_BLRP_SCHEDULE_DELAY`), in batches of 512 ([batch.go](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/sdk/log/batch.go)) |
| Overflow | Not applicable | Queue size 2048. "If enqueueing r would exceed the capacity of q, the oldest Record held in q will be dropped" |
| Crash | Nothing in flight once `Emit` has returned | Up to one interval of queued records is lost |
| Docs' advice | "not recommended for production … there may be exceptions in which certain Exporter implementations perform better with this Processor" | Recommended for production |

Audit events are rare: a handful per admin session. Their cost is one local Turso
transaction each, so the "slow" argument against the simple processor does not apply.
**Use the simple processor.**

### Shutdown and ForceFlush

- `LoggerProvider.Shutdown` stops admitting new `Emit`/`ForceFlush` operations, waits for
  any in flight, then calls each processor's `Shutdown`
  ([provider.go](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/sdk/log/provider.go)).
  `SimpleProcessor.Shutdown` runs `exporter.ForceFlush` and then `exporter.Shutdown`.
- Our exporter commits inside `Export`, so `ForceFlush` has nothing to do. Its `Shutdown`
  only marks the exporter closed. The store owns the `*sql.DB` and closes it **after**
  `lp.Shutdown`.
- Order in `main`: stop the HTTP server (drain handlers), then `lp.Shutdown(ctx)`, then
  `store.Close()`.
- After shutdown, `Emit` is a silent no-op. With the error-slot facade below, the caller
  sees an error instead. This was verified in the test run.

### Crash durability

- With the simple processor, a committed row is as durable as Turso's commit. Turso
  supports `PRAGMA synchronous` `OFF` and `FULL`
  ([Turso COMPAT.md](https://github.com/tursodatabase/turso/blob/main/COMPAT.md)). A fresh
  `tursogo v0.8.3` database reported `PRAGMA synchronous = 2` (FULL) in our test run. Set
  `PRAGMA synchronous = FULL` explicitly at open anyway, so a changed default cannot weaken
  durability.
- **Order of writes for destructive actions.** Write the `reconfirmation` audit event
  (outcome success) *before* running the action, then write the `action` audit event with
  its outcome *after*. If the process crashes mid-action, the reconfirmation row still
  records the intent. If the reconfirmation write fails, refuse the action.

### `Emit` cannot report failure

`log.Logger.Emit(ctx, Record)` returns nothing. The SDK passes processor errors to
`otel.Handle`
([sdk/log/logger.go](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/sdk/log/logger.go)),
and the default handler only prints to stderr. The `Processor` contract adds that `OnEmit`
"should not block". The simple processor already breaks that by design, and we accept it
for audit events.

To fail closed, the facade puts an `*error` slot in the context it passes to `Emit`. A thin
`auditProcessor` wraps `SimpleProcessor.OnEmit` and writes the export error into the slot.
The slot starts out as a "not written" error, so a shut-down provider or a missing
processor also counts as a failure. Also call `otel.SetErrorHandler` so that failures show
up in the server's own log lines.

## 3. LogRecord data model and the Turso table

The data model is stable
([Logs Data Model](https://opentelemetry.io/docs/specs/otel/logs/data-model/)).

| LogRecord field | Type | Column |
|---|---|---|
| Timestamp | uint64 ns since epoch | `time_unix_nano` (falls back to ObservedTimestamp, as the spec advises) |
| ObservedTimestamp | uint64 ns | `observed_time_unix_nano` |
| TraceId / SpanId / TraceFlags | bytes / bytes / byte | `trace_id`, `span_id` (hex), `trace_flags` |
| SeverityNumber (1–24; 9–12 INFO, 13–16 WARN, 17+ error) | int | `severity_number` |
| SeverityText | string | `severity_text` |
| Body (AnyValue) | any | `body`, but only when it is a string ([event rules](https://opentelemetry.io/docs/specs/semconv/general/events/) say an event body must be a display string or nothing) |
| Attributes | map | `attributes` (JSON object), plus promoted columns |
| EventName | string. Non-empty means the record is an Event | `event_name` |
| Resource | attributes | `resource` (JSON) |
| InstrumentationScope | name, version | `scope_name`, `scope_version` |
| (SDK) dropped attribute count | int | `dropped_attributes` |

Columns promoted from attributes for indexing: `outcome` (`origin_admin.outcome`),
`actor_id` (`user.id`), `actor_name` (`user.name`), `session_id` (`session.id`) and
`client_address` (`client.address`).

Turso supports `STRICT` tables, `CREATE INDEX`, `CREATE TRIGGER` with `RAISE(ABORT, …)`,
`json_extract`, `->`/`->>` and `jsonb`. `COMPAT.md` does not list expression indexes or
partial indexes
([Turso COMPAT.md](https://github.com/tursodatabase/turso/blob/main/COMPAT.md)). That is
why the filtered fields are real columns rather than indexes on JSON paths. Ad-hoc queries
can still use `json_extract(attributes, '$."origin_admin.action.name"')`, which was
verified.

```sql
CREATE TABLE IF NOT EXISTS audit_events (
	id                      INTEGER PRIMARY KEY,            -- insertion order
	time_unix_nano          INTEGER NOT NULL,
	observed_time_unix_nano INTEGER NOT NULL,
	event_name              TEXT    NOT NULL,
	severity_number         INTEGER NOT NULL,
	severity_text           TEXT    NOT NULL,
	outcome                 TEXT    NOT NULL CHECK (outcome IN ('success', 'failure')),
	actor_id                TEXT,          -- user.id; NULL for a failed sign-in with an unknown name
	actor_name              TEXT,          -- user.name
	session_id              TEXT,          -- session.id
	client_address          TEXT,          -- client.address
	trace_id                TEXT,
	span_id                 TEXT,
	trace_flags             INTEGER NOT NULL DEFAULT 0,
	body                    TEXT,
	attributes              TEXT    NOT NULL,   -- JSON object of every attribute
	resource                TEXT    NOT NULL,   -- JSON object
	scope_name              TEXT    NOT NULL,
	scope_version           TEXT    NOT NULL,
	dropped_attributes      INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX IF NOT EXISTS audit_events_time  ON audit_events (time_unix_nano);
CREATE INDEX IF NOT EXISTS audit_events_name  ON audit_events (event_name, time_unix_nano);
CREATE INDEX IF NOT EXISTS audit_events_actor ON audit_events (actor_id, time_unix_nano);
CREATE TRIGGER IF NOT EXISTS audit_events_no_update BEFORE UPDATE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit events are permanent'); END;
CREATE TRIGGER IF NOT EXISTS audit_events_no_delete BEFORE DELETE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit events are permanent'); END;
```

Notes:

- The trigger is a guard against bugs, not tamper-proofing. Anyone with the database file
  can drop it.
- Storing the resource JSON on every row is redundant. At audit volume this is negligible,
  and it keeps each row self-describing.
- The existing `store.go` already calls `SetMaxOpenConns(1)` on the shared `*sql.DB`, so
  writes are serialised.

## 4. Semantic conventions to follow

- **Event name.** Every event must have an event name, set in the LogRecord **`EventName`
  field** (`Record.SetEventName`). The `event.name` attribute is deprecated: its value
  "MUST now be set as the value of the EventName field"
  ([event registry](https://opentelemetry.io/docs/specs/semconv/registry/attributes/event/)).
  Names must not contain dynamic values. Attributes should be flat, and `error.type` belongs
  on any event that can succeed or fail
  ([Events conventions](https://opentelemetry.io/docs/specs/semconv/general/events/), which
  have *Development* status).
- **Actor.** Use `user.id` and `user.name`. `user.roles` is not useful, because every admin
  has the same powers. All `user.*` attributes are *Development*
  ([user registry](https://opentelemetry.io/docs/specs/semconv/registry/attributes/user/)).
- **Session.** Use `session.id` and `session.previous_id`, both *Development*
  ([session registry](https://opentelemetry.io/docs/specs/semconv/registry/attributes/session/)).
  `session.previous_id` is defined as the prior session ID for the same user, which fits
  "session replaced" exactly. **Never store the session cookie or its `token_hash` as
  `session.id`.** Give each session its own random public ID, a new column on `sessions`.
- **Client.** Use `client.address` and `user_agent.original`. Behind Cloudflare and Caddy,
  set `client.address` from the trusted forwarded header, not from the socket peer.
- **Failure reason.** Use `error.type` with a low-cardinality value.
- **Not covered.** OpenTelemetry has **no** semantic conventions for audit, authentication
  or security events. The only `security_rule.*` attributes are for rule matches, and the
  Go semconv v1.43.0 package contains no audit or authentication keys. Our own attributes
  therefore use the `origin_admin.` prefix, as the naming guidance recommends for
  non-standard attributes.

### Attributes for each audit event kind

These attributes are common to every kind: `user.id`, `user.name`, `session.id`,
`client.address`, `user_agent.original` and `origin_admin.outcome` (`success` or
`failure`). On failure, add `error.type`. Trace and span IDs come automatically from the
request span in the context.

| Audit event | `EventName` | Severity | Extra attributes |
|---|---|---|---|
| Sign-in | `origin_admin.sign_in` | INFO on success, WARN on failure | `error.type` ∈ `unknown_admin`, `bad_password`, `bad_authenticator_code`, `rate_limited`. `session.id` only on success. `user.id` absent when the name is unknown, but `user.name` keeps the attempted name. |
| Sign-out | `origin_admin.sign_out` | INFO | `origin_admin.sign_out.reason` ∈ `admin`, `expired`, `password_changed` |
| Session replaced | `origin_admin.session.replaced` | WARN | `session.id` = new session, `session.previous_id` = the session it ended. `client.address` is the new browser's. Emitted together with the successful sign-in that caused it. |
| Reconfirmation | `origin_admin.reconfirmation` | INFO on success, WARN on failure | `origin_admin.action.name` (the action being confirmed), `error.type` ∈ `bad_authenticator_code`, `rate_limited` |
| Action | `origin_admin.action` | INFO on success, ERROR on failure | `origin_admin.action.name` (low-cardinality verb such as `container.restart`), `origin_admin.action.target` (such as the container name), `origin_admin.action.reconfirmed` (bool), `error.type` on failure |

Use the Go helpers `semconv.UserID`, `semconv.UserName`, `semconv.SessionID`,
`semconv.SessionPreviousID`, `semconv.ClientAddress`, `semconv.UserAgentOriginal` and
`semconv.ErrorTypeKey` from `go.opentelemetry.io/otel/semconv/v1.43.0`, and define the
`origin_admin.*` keys as constants in the audit package.

## 5. Exporter sketch

This code was compiled and run, in a scratch module, against `otel* v1.47.0`,
`semconv/v1.43.0` and `tursogo v0.8.3`. The test run produced these results:

- rows were inserted
- `json_extract` worked
- a record without an outcome was rejected and the error reached the caller
- `UPDATE` and `DELETE` were refused by the triggers
- `Emit` after `Shutdown` surfaced an error to the caller

```go
package audit

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const attrOutcome = attribute.Key("origin_admin.outcome")

// tursoExporter writes audit event LogRecords into audit_events.
type tursoExporter struct {
	db       *sql.DB
	mu       sync.Mutex
	shutdown bool
}

var _ sdklog.Exporter = (*tursoExporter)(nil)

func (e *tursoExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shutdown {
		return sdklog.ErrExporterShutdown
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := range records {
		row, err := toRow(&records[i])
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, insertAuditEvent, row...); err != nil {
			return fmt.Errorf("insert audit event %q: %w", records[i].EventName(), err)
		}
	}
	return tx.Commit()
}

// Rows are committed inside Export, so there is nothing to flush.
func (e *tursoExporter) ForceFlush(context.Context) error { return nil }

func (e *tursoExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.shutdown = true // the store owns and closes the *sql.DB
	return nil
}

const insertAuditEvent = `INSERT INTO audit_events (
	time_unix_nano, observed_time_unix_nano, event_name,
	severity_number, severity_text, outcome, actor_id, actor_name,
	session_id, client_address, trace_id, span_id, trace_flags,
	body, attributes, resource, scope_name, scope_version, dropped_attributes
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

func toRow(r *sdklog.Record) ([]any, error) {
	attrs := map[string]any{}
	var outcome, actorID, actorName, sessionID, clientAddr sql.NullString
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		attrs[string(kv.Key)] = jsonValue(kv.Value)
		s := sql.NullString{String: kv.Value.Emit(), Valid: true}
		switch kv.Key {
		case attrOutcome:
			outcome = s
		case semconv.UserIDKey:
			actorID = s
		case semconv.UserNameKey:
			actorName = s
		case semconv.SessionIDKey:
			sessionID = s
		case semconv.ClientAddressKey:
			clientAddr = s
		}
		return true
	})
	if r.EventName() == "" || !outcome.Valid {
		return nil, errors.New("audit event needs an event name and an outcome")
	}
	attrJSON, err := json.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	resJSON, err := json.Marshal(resourceMap(r.Resource()))
	if err != nil {
		return nil, err
	}
	var traceID, spanID, body sql.NullString
	if r.TraceID().IsValid() {
		traceID = sql.NullString{String: r.TraceID().String(), Valid: true}
	}
	if r.SpanID().IsValid() {
		spanID = sql.NullString{String: r.SpanID().String(), Valid: true}
	}
	if r.Body().Type() == attribute.STRING {
		body = sql.NullString{String: r.Body().AsString(), Valid: true}
	}
	ts := r.Timestamp()
	if ts.IsZero() {
		ts = r.ObservedTimestamp() // data model: fall back to ObservedTimestamp
	}
	scope := r.InstrumentationScope()
	return []any{
		ts.UnixNano(), r.ObservedTimestamp().UnixNano(), r.EventName(),
		int64(r.Severity()), r.SeverityText(), outcome, actorID, actorName,
		sessionID, clientAddr, traceID, spanID, int64(r.TraceFlags()),
		body, string(attrJSON), string(resJSON), scope.Name, scope.Version,
		int64(r.DroppedAttributes()),
	}, nil
}

// jsonValue turns an attribute.Value into plain JSON values. attribute.Value's
// own MarshalJSON wraps every value as {"Type":…,"Value":…}, which we do not want.
func jsonValue(v attribute.Value) any {
	switch v.Type() {
	case attribute.MAP:
		m := map[string]any{}
		for _, kv := range v.AsMap() {
			m[string(kv.Key)] = jsonValue(kv.Value)
		}
		return m
	case attribute.SLICE:
		out := []any{}
		for _, e := range v.AsSlice() {
			out = append(out, jsonValue(e))
		}
		return out
	case attribute.BYTESLICE:
		return base64.StdEncoding.EncodeToString(v.AsByteSlice())
	default:
		return v.AsInterface()
	}
}

func resourceMap(res *resource.Resource) map[string]any {
	m := map[string]any{}
	if res != nil {
		for _, kv := range res.Attributes() {
			m[string(kv.Key)] = jsonValue(kv.Value)
		}
	}
	return m
}

// auditProcessor exports synchronously and hands the export error back to the
// caller through the Emit context, because log.Logger.Emit has no error return.
type auditProcessor struct{ *sdklog.SimpleProcessor }

type errSlotKey struct{}

func (p auditProcessor) OnEmit(ctx context.Context, r *sdklog.Record) error {
	err := p.SimpleProcessor.OnEmit(ctx, r)
	if slot, ok := ctx.Value(errSlotKey{}).(*error); ok {
		*slot = err
	}
	return err
}

// Log is the audit facade the HTTP handlers call.
type Log struct{ logger log.Logger }

func New(db *sql.DB, res *resource.Resource) (Log, *sdklog.LoggerProvider) {
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(auditProcessor{sdklog.NewSimpleProcessor(&tursoExporter{db: db})}),
	)
	return Log{logger: lp.Logger("steven3zx.com/portfolio/admin/server/audit")}, lp
}

// Record emits one audit event and returns an error unless its row was committed.
func (a Log) Record(ctx context.Context, name string, sev log.Severity, attrs ...attribute.KeyValue) error {
	var rec log.Record
	now := time.Now()
	rec.SetEventName(name)
	rec.SetTimestamp(now) // the SDK fills ObservedTimestamp but never Timestamp
	rec.SetObservedTimestamp(now)
	rec.SetSeverity(sev)
	rec.AddAttributes(attrs...)

	exportErr := errors.New("audit event not written: no audit processor ran")
	ctx = context.WithValue(ctx, errSlotKey{}, &exportErr)
	a.logger.Emit(ctx, rec)
	return exportErr
}
```

Usage:

```go
err := auditLog.Record(r.Context(), "origin_admin.reconfirmation", log.SeverityInfo,
	semconv.UserID(admin.ID), semconv.UserName(admin.Username),
	semconv.SessionID(sess.PublicID), semconv.ClientAddress(clientIP(r)),
	attribute.String("origin_admin.outcome", "success"),
	attribute.String("origin_admin.action.name", "container.restart"))
if err != nil {
	http.Error(w, "audit unavailable", http.StatusServiceUnavailable) // fail closed
	return
}
```

## 6. How this sits next to the span ingest path for request traces

- `sdktrace.SpanExporter` has the same shape: `ExportSpans(ctx, []ReadOnlySpan) error` and
  `Shutdown(ctx) error`, with no `ForceFlush`
  ([sdk/trace/span_exporter.go](https://github.com/open-telemetry/opentelemetry-go/blob/v1.47.0/sdk/trace/span_exporter.go)).
  The Turso pattern carries straight over: one transaction per export call, promoted
  indexed columns, and attributes, resource and events as JSON. Both exporters can share
  the `*sql.DB`, `jsonValue` and `resourceMap`.
- **The processors differ.** Request traces are high volume, and losing some is
  acceptable, so they should use `BatchSpanProcessor`. Audit events must use the
  synchronous path. Keep separate tables (`audit_events`, and spans for request traces), so
  that retention can prune request traces while audit events stay permanent.
- **Linking.** Because the SDK copies the span context from the `Emit` context into the
  LogRecord, an audit event emitted inside a traced handler carries the `trace_id` of that
  request's trace. This gives a free join from an audit event to its request trace.
- **Spans from outside the process.** If request traces arrive from Caddy or other
  processes over OTLP rather than being created in process, that path does not use
  `SpanExporter`. It receives OTLP and writes rows directly. Only the row mapping and JSON
  helpers are shared. Which route request traces take is for the span-ingest ticket to
  decide.

## Open points for the plan

1. `sessions` needs a non-secret public session ID to use as `session.id`. Today the only
   key is `token_hash`.
2. Decide which forwarded header (Cloudflare `CF-Connecting-IP` or Caddy's
   `X-Forwarded-For`) is trusted for `client.address`.
3. `user.*`, `session.*` and the event conventions are *Development* stability. Pin
   `semconv/v1.43.0` and treat a later rename as a deliberate migration.
