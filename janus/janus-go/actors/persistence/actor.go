package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	_ "turso.tech/database/tursogo"

	actortrace "janus/actors/telemetry"
)

// Actor owns the SQLite connection and processes persistence messages.
type Actor struct {
	path string
	db   *sql.DB
	key  []byte
}

var _ goakt.Actor = (*Actor)(nil)

// New creates a persistence actor for the database at path. When jwtSecret is
// supplied, it is used to derive the key that encrypts stored provider tokens.
func New(path string, jwtSecret ...string) *Actor {
	a := &Actor{path: path}
	if len(jwtSecret) > 0 {
		a.key = deriveTokenKey(jwtSecret[0])
	}
	return a
}

func (a *Actor) PreStart(*goakt.Context) error {
	if a.path != ":memory:" && !strings.HasPrefix(a.path, "file:") {
		if err := os.MkdirAll(filepath.Dir(a.path), 0700); err != nil {
			return fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("turso", a.path)
	if err != nil {
		return fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return fmt.Errorf("configure sqlite database: %w", err)
	}
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
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return fmt.Errorf("initialize sqlite schema: %w", err)
	}
	if err := migrateProjectSnapshotActiveColumn(db); err != nil {
		_ = db.Close()
		return fmt.Errorf("migrate project snapshots: %w", err)
	}
	if err := migrateAndRecoverJobs(db); err != nil {
		_ = db.Close()
		return fmt.Errorf("recover interrupted project imports: %w", err)
	}
	a.db = db
	return nil
}

func (a *Actor) Receive(rctx *goakt.ReceiveContext) {
	ctx, span := actortrace.StartSpan(rctx.Context(), "actor.persistence.receive", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	span.SetAttributes(attribute.String("actor.type", "persistence"))
	switch msg := rctx.Message().(type) {
	case *CreateLocalUser:
		tx, err := a.db.BeginTx(ctx, nil)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO users(id,email) VALUES(?,?)`, msg.ID, strings.TrimSpace(msg.Email))
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO local_credentials(user_id,email,password_hash) VALUES(?,?,?)`, msg.ID, strings.TrimSpace(msg.Email), msg.PasswordHash)
		}
		if err == nil {
			err = tx.Commit()
		} else if tx != nil {
			_ = tx.Rollback()
		}
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&LocalUserCreated{Err: err})
	case *FindLocalUser:
		var result LocalUserFound
		err := a.db.QueryRowContext(ctx, `SELECT user_id,email,password_hash FROM local_credentials WHERE email=? COLLATE NOCASE`, strings.TrimSpace(msg.Email)).Scan(&result.ID, &result.Email, &result.PasswordHash)
		switch {
		case err == nil:
			result.Found = true
		case errors.Is(err, sql.ErrNoRows):
			err = nil
		}
		result.Err = err
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&result)
	case *ResolveGoogleIdentity:
		result, err := a.resolveGoogle(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&result)
	case *ResolveGitHubLogin:
		result, err := a.resolveGitHubLogin(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		result.Err = err
		rctx.Response(&result)
	case *ConnectGitHub:
		err := a.connectGitHub(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&GitHubConnected{Err: err})
	case *FindGitHubToken:
		result := a.findGitHubToken(ctx, msg.UserID)
		if result.Err != nil {
			actortrace.RecordError(span, result.Err)
		}
		rctx.Response(&result)
	case *DisconnectGitHub:
		_, err := a.db.ExecContext(ctx, `DELETE FROM github_connections WHERE user_id=?`, msg.UserID)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&GitHubDisconnected{Err: err})
	case *CreateProject:
		result, err := a.createProject(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ProjectCreated{Project: result, Err: err})
	case *UpdateProject:
		project, err := a.updateProject(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ProjectUpdated{Project: project, Err: err})
	case *DeleteProject:
		deleted, err := a.deleteProject(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ProjectDeleted{Deleted: deleted, Err: err})
	case *ListProjects:
		result, err := a.listProjects(ctx, msg.OwnerID)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ProjectsListed{Projects: result, Err: err})
	case *GetProject:
		result, err := a.getProject(ctx, msg.ID, msg.OwnerID)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ProjectFound{Project: result.Project, Found: result.Found, Err: err})
	case *CreateImportJob:
		id, err := a.createImportJob(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ImportJobCreated{ID: id, Err: err})
	case *UpdateImportJob:
		err := a.updateImportStatus(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ImportJobCreated{ID: msg.ID, Err: err})
	case *FindImportJob:
		result, err := a.findImportJob(ctx, msg.ID, msg.OwnerID)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&result)
	case *BeginSnapshot:
		err := a.beginSnapshot(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&BeginSnapshotResult{Err: err})
	case *AddSnapshotFiles:
		err := a.addSnapshotFiles(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&SnapshotFilesAdded{Err: err})
	case *CompleteSnapshot:
		err := a.completeSnapshot(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&SnapshotCompleted{Err: err})
	case *DeleteStagingSnapshot:
		_, err := a.db.ExecContext(ctx, `DELETE FROM project_snapshots WHERE id=? AND state='staging'`, msg.SnapshotID)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&StagingSnapshotDeleted{Err: err})
	case *SetProjectEntryPoint:
		result, err := a.db.ExecContext(ctx, `UPDATE project_snapshots SET entry_point=? WHERE id=? AND project_id=? AND owner_id=? AND state='ready'`, msg.EntryPoint, msg.SnapshotID, msg.ProjectID, msg.OwnerID)
		if err == nil {
			if n, _ := result.RowsAffected(); n != 1 {
				err = errors.New("project snapshot not found")
			}
		}
		if err == nil {
			_, err = a.db.ExecContext(ctx, `UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=? AND owner_id=?`, msg.ProjectID, msg.OwnerID)
		}
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ProjectEntryPointSet{Err: err})
	case *ListProjectFiles:
		files, err := a.listProjectFiles(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ProjectFilesListed{Files: files, Err: err})
	case *CreateWASMBuild:
		_, err := a.db.ExecContext(ctx, `INSERT INTO project_wasm_builds(id,owner_id,project_id,snapshot_id,target,status)
			SELECT ?,p.owner_id,p.id,s.id,?,'queued' FROM projects p JOIN project_snapshots s ON s.project_id=p.id
			WHERE p.id=? AND p.owner_id=? AND s.id=? AND s.state='ready'`, msg.ID, msg.Target, msg.ProjectID, msg.OwnerID, msg.SnapshotID)
		if err == nil {
			var exists int
			err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_wasm_builds WHERE id=?`, msg.ID).Scan(&exists)
			if err == nil && exists == 0 {
				err = errors.New("project snapshot not found")
			}
		}
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&WASMBuildCreated{Err: err})
	case *UpdateWASMBuild:
		err := a.updateWASMStatus(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&WASMBuildCreated{Err: err})
	case *CompleteWASMBuild:
		result, err := a.db.ExecContext(ctx, `UPDATE project_wasm_builds SET status='complete',progress=100,error='',artifact_name=?,artifact=?,build_log=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status IN ('queued','building')`, msg.ArtifactName, msg.Artifact, string(msg.Log), msg.ID)
		if err == nil {
			if rows, _ := result.RowsAffected(); rows == 0 {
				err = errors.New("WASM build not found")
			}
		}
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&WASMBuildCreated{Err: err})
	case *FindWASMBuild:
		var result WASMBuildFound
		build := &result.Build
		err := a.db.QueryRowContext(ctx, `SELECT id,project_id,snapshot_id,target,status,progress,error,artifact_name,build_log,created_at,updated_at FROM project_wasm_builds WHERE id=? AND owner_id=?`, msg.ID, msg.OwnerID).Scan(&build.ID, &build.ProjectID, &build.SnapshotID, &build.Target, &build.Status, &build.Progress, &build.Error, &build.ArtifactName, &build.Log, &build.CreatedAt, &build.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		} else if err == nil {
			result.Found = true
		}
		result.Err = err
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&result)
	case *FindWASMBuildArtifact:
		var result WASMBuildArtifact
		err := a.db.QueryRowContext(ctx, `SELECT artifact_name,artifact FROM project_wasm_builds WHERE id=? AND owner_id=? AND status='complete'`, msg.ID, msg.OwnerID).Scan(&result.Name, &result.Data)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		} else if err == nil {
			result.Found = true
		}
		result.Err = err
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&result)
	case *ListSnapshots:
		snapshots, err := a.listSnapshots(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&SnapshotsListed{Snapshots: snapshots, Err: err})
	case *ActivateSnapshot:
		activated, err := a.activateSnapshot(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&SnapshotActivated{Activated: activated, Err: err})
	case *ListResumableImports:
		jobs, err := a.listResumableImports(ctx)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ResumableImportsListed{Jobs: jobs, Err: err})
	case *ListResumableBuilds:
		builds, err := a.listResumableBuilds(ctx)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ResumableBuildsListed{Builds: builds, Err: err})
	case *FindStagingSnapshot:
		result, err := a.findStagingSnapshot(ctx, msg.JobID)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		result.Err = err
		rctx.Response(&result)
	case *DiscardJobStaging:
		err := a.discardJobStaging(ctx, msg.JobID)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&JobStagingDiscarded{Err: err})
	case *ListActiveJobs:
		jobs, err := a.listActiveJobs(ctx, msg.OwnerID)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&ActiveJobsListed{Jobs: jobs, Err: err})
	case *DeleteAccount:
		result, err := a.deleteAccount(ctx, msg)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		result.Err = err
		rctx.Response(&result)
	case *FindUser:
		var result UserFound
		err := a.db.QueryRowContext(ctx, `SELECT id,email,display_name FROM users WHERE id=?`, msg.ID).Scan(&result.ID, &result.Email, &result.DisplayName)
		switch {
		case err == nil:
			result.Found = true
		case errors.Is(err, sql.ErrNoRows):
			err = nil
		}
		result.Err = err
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&result)
	case *CloseDatabase:
		err := a.Close()
		rctx.Response(&DatabaseClosed{Err: err})
	default:
		rctx.Unhandled()
	}
}

func (a *Actor) resolveGoogle(ctx context.Context, msg *ResolveGoogleIdentity) (GoogleIdentityResolved, error) {
	var result GoogleIdentityResolved
	result.Email, result.DisplayName = msg.Email, msg.DisplayName
	err := a.db.QueryRowContext(ctx, `SELECT u.id,u.email,u.display_name FROM oauth_identities i JOIN users u ON u.id=i.user_id WHERE i.provider='google' AND i.subject=?`, msg.Subject).Scan(&result.ID, &result.Email, &result.DisplayName)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	userID := newID()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,display_name) VALUES(?,?,?)`, userID, msg.Email, msg.DisplayName); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_identities(provider,subject,user_id) VALUES('google',?,?)`, msg.Subject, userID); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	result.ID = userID
	return result, nil
}

func (a *Actor) PostStop(*goakt.Context) error { return a.Close() }

func (a *Actor) Close() error {
	if a.db == nil {
		return nil
	}
	return a.db.Close()
}

func newID() string {
	return uuid.NewString()
}
