// Package sqlstore is the app.Store over a Turso (SQLite) database. It owns the
// database and its single connection, so every operation takes turns, and an
// operation that spans several statements runs in a transaction.
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	_ "turso.tech/database/tursogo"

	"janus/internal/app"
)

// DefaultTimeout is how long one Store operation may take unless WithTimeout
// says otherwise.
const DefaultTimeout = 30 * time.Second

var tracer = otel.Tracer("github.com/SMG3zx/Janus/sqlstore")

// Store is an app.Store backed by one database file.
//
// Every error it returns is a *app.ReplyError, except a cancelled or expired
// context, which is returned as it is. Callers tell the two apart with
// app.IsReply.
type Store struct {
	db      *sql.DB
	key     []byte
	timeout time.Duration
}

var _ app.Store = (*Store)(nil)

// Option configures Open.
type Option func(*Store)

// WithTokenSecret derives the key that encrypts stored provider tokens from
// secret. Without it, ConnectGitHub fails because tokens cannot be protected.
func WithTokenSecret(secret string) Option {
	return func(s *Store) { s.key = deriveTokenKey(secret) }
}

// WithTimeout bounds each operation. The default is DefaultTimeout.
func WithTimeout(d time.Duration) Option { return func(s *Store) { s.timeout = d } }

// Open opens the database at path, creating it and its directory when they do
// not exist, brings its schema up to date and recovers jobs that the previous
// shutdown interrupted.
func Open(path string, options ...Option) (*Store, error) {
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("turso", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite database: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize sqlite schema: %w", err)
	}
	if err := migrateProjectSnapshotActiveColumn(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate project snapshots: %w", err)
	}
	if err := migrateAndRecoverJobs(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("recover interrupted project imports: %w", err)
	}
	s := &Store{db: db, timeout: DefaultTimeout}
	for _, option := range options {
		option(s)
	}
	return s, nil
}

// Close closes the database. The Store is unusable afterwards.
func (s *Store) Close() error { return s.db.Close() }

// querier is what *sql.DB and *sql.Tx share, so a helper works in either.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// start begins the span and the time limit of one operation. The returned
// function ends them and passes err through in the form the Store returns it.
func (s *Store) start(ctx context.Context, operation string) (context.Context, func(error) error) {
	ctx, span := tracer.Start(ctx, "sqlstore."+operation, trace.WithSpanKind(trace.SpanKindInternal))
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	return ctx, func(err error) error {
		cancel()
		defer span.End()
		if err == nil {
			return nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return &app.ReplyError{Err: err}
	}
}

// inTx runs fn in a transaction, which commits when fn returns nil.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func newID() string { return uuid.NewString() }

const schema = `
CREATE TABLE IF NOT EXISTS users (
	 id TEXT PRIMARY KEY,
	 email TEXT NOT NULL DEFAULT '',
	 display_name TEXT NOT NULL DEFAULT '',
	 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS local_credentials (
	 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	 email TEXT NOT NULL UNIQUE COLLATE NOCASE,
	 password_hash TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS oauth_identities (
	 provider TEXT NOT NULL,
	 subject TEXT NOT NULL,
	 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	 PRIMARY KEY (provider, subject)
);
CREATE TABLE IF NOT EXISTS github_connections (
	 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	 subject TEXT NOT NULL UNIQUE,
	 token_ciphertext BLOB NOT NULL,
	 connected_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS projects (
	 id TEXT PRIMARY KEY,
	 owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	 name TEXT NOT NULL,
	 description TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
 updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX IF NOT EXISTS projects_owner_updated ON projects(owner_id, updated_at DESC);
CREATE TABLE IF NOT EXISTS project_snapshots (
	 id TEXT PRIMARY KEY,
	 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	 owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	 repository_id INTEGER NOT NULL,
	 repository TEXT NOT NULL,
	 branch TEXT NOT NULL,
	 commit_sha TEXT NOT NULL,
	 default_branch TEXT NOT NULL DEFAULT '',
	 language TEXT NOT NULL DEFAULT '',
	 runtime TEXT NOT NULL DEFAULT '',
	 stars INTEGER NOT NULL DEFAULT 0,
	 forks INTEGER NOT NULL DEFAULT 0,
	 metadata_json TEXT NOT NULL DEFAULT '{}',
	 entry_point TEXT NOT NULL DEFAULT '',
	 file_count INTEGER NOT NULL DEFAULT 0,
	 total_bytes INTEGER NOT NULL DEFAULT 0,
	is_active INTEGER NOT NULL DEFAULT 0,
	 state TEXT NOT NULL DEFAULT 'staging',
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX IF NOT EXISTS project_snapshots_latest ON project_snapshots(project_id, state, created_at DESC);
CREATE TABLE IF NOT EXISTS project_files (
	 snapshot_id TEXT NOT NULL REFERENCES project_snapshots(id) ON DELETE CASCADE,
	 path TEXT NOT NULL,
	 mode TEXT NOT NULL,
	 content BLOB NOT NULL,
	 sha TEXT NOT NULL,
	 PRIMARY KEY(snapshot_id, path)
);
CREATE TABLE IF NOT EXISTS project_import_jobs (
	 id TEXT PRIMARY KEY,
	 owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	 repository TEXT NOT NULL,
	 branch TEXT NOT NULL,
	 commit_sha TEXT NOT NULL,
	 status TEXT NOT NULL,
	 progress INTEGER NOT NULL DEFAULT 0,
	 error TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
 updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX IF NOT EXISTS import_jobs_owner ON project_import_jobs(owner_id, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_import_per_project ON project_import_jobs(project_id) WHERE status IN ('queued','cloning','scanning','storing');
CREATE TABLE IF NOT EXISTS project_wasm_builds (
	 id TEXT PRIMARY KEY,
	 owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	 snapshot_id TEXT NOT NULL REFERENCES project_snapshots(id) ON DELETE CASCADE,
	 target TEXT NOT NULL,
	 status TEXT NOT NULL,
	 progress INTEGER NOT NULL DEFAULT 0,
	 error TEXT NOT NULL DEFAULT '',
	 artifact_name TEXT NOT NULL DEFAULT '',
	 artifact BLOB,
	 build_log TEXT NOT NULL DEFAULT '',
	 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
	 updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX IF NOT EXISTS wasm_builds_owner ON project_wasm_builds(owner_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_wasm_build_per_project ON project_wasm_builds(project_id) WHERE status IN ('queued','building');`
