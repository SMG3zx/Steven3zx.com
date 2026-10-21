package main

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "turso.tech/database/tursogo"
)

// legacyJanusDB writes a janus.db from before the split holding one local
// User with one Project, and returns its path.
func legacyJanusDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "janus.db")
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL DEFAULT '', display_name TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE local_credentials (user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, email TEXT NOT NULL UNIQUE COLLATE NOCASE, password_hash TEXT NOT NULL)`,
		`CREATE TABLE oauth_identities (provider TEXT NOT NULL, subject TEXT NOT NULL, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, PRIMARY KEY (provider, subject))`,
		`CREATE TABLE github_connections (user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, subject TEXT NOT NULL UNIQUE, token_ciphertext BLOB NOT NULL, connected_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE project_snapshots (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, owner_id TEXT NOT NULL, repository_id INTEGER NOT NULL, repository TEXT NOT NULL, branch TEXT NOT NULL, commit_sha TEXT NOT NULL, default_branch TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '', runtime TEXT NOT NULL DEFAULT '', stars INTEGER NOT NULL DEFAULT 0, forks INTEGER NOT NULL DEFAULT 0, metadata_json TEXT NOT NULL DEFAULT '{}', manifest_json TEXT NOT NULL DEFAULT '', entry_point TEXT NOT NULL DEFAULT '', file_count INTEGER NOT NULL DEFAULT 0, total_bytes INTEGER NOT NULL DEFAULT 0, is_active INTEGER NOT NULL DEFAULT 0, job_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'staging', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE project_files (snapshot_id TEXT NOT NULL, path TEXT NOT NULL, mode TEXT NOT NULL, content BLOB NOT NULL, sha TEXT NOT NULL, PRIMARY KEY(snapshot_id, path))`,
		`CREATE TABLE project_import_jobs (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, project_id TEXT NOT NULL, repository TEXT NOT NULL, branch TEXT NOT NULL, commit_sha TEXT NOT NULL, status TEXT NOT NULL, progress INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO users(id,email) VALUES('alice','alice@example.com')`,
		`INSERT INTO local_credentials(user_id,email,password_hash) VALUES('alice','alice@example.com','h')`,
		`INSERT INTO projects(id,owner_id,name) VALUES('p1','alice','Legacy')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestMigrateNamesTheMigrationsItKnows(t *testing.T) {
	for _, args := range [][]string{nil, {"other"}, {"split", "extra"}} {
		if err := runMigrate(context.Background(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage: janus migrate split") {
			t.Errorf("migrate %v = %v, want the usage", args, err)
		}
	}
}

func TestMigrateSplitCopiesTheConfiguredDatabase(t *testing.T) {
	headscaleEnvironment(t, "", "", "")
	legacy := legacyJanusDB(t)
	dir := t.TempDir()
	t.Setenv("JANUS_DATABASE_PATH", legacy)
	t.Setenv("JANUS_IDENTITY_DATABASE_PATH", filepath.Join(dir, "identity.db"))
	t.Setenv("JANUS_PROJECTS_DATABASE_PATH", filepath.Join(dir, "projects.db"))

	var out bytes.Buffer
	if err := runMigrate(context.Background(), []string{"split"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"identity: copied", "local_credentials", "projects: copied", "projects             1 rows"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := runMigrate(context.Background(), []string{"split"}, &out); err != nil {
		t.Fatalf("running the split again = %v", err)
	}
	if !strings.Contains(out.String(), "identity: ") || strings.Count(out.String(), "exists, skipped") != 2 {
		t.Fatalf("a second run did something:\n%s", out.String())
	}
}

func TestTheIdentityServiceWaitsForTheSplit(t *testing.T) {
	legacy := legacyJanusDB(t)
	identity := filepath.Join(t.TempDir(), "identity.db")
	a := newApplication(nil, nil)
	a.settings = settings{
		NATSAddress: "127.0.0.1:0", NATSDir: t.TempDir(),
		DatabasePath: legacy, IdentityDatabasePath: identity, JWTSecret: strings.Repeat("s", 32),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.startNATS(ctx); err != nil {
		t.Fatal(err)
	}
	defer a.stopNATS(ctx)

	if err := a.startIdentityService(ctx); err == nil || !strings.Contains(err.Error(), "janus migrate split") {
		t.Fatalf("starting before the split = %v, want a refusal naming the command", err)
	}

	headscaleEnvironment(t, "", "", "")
	t.Setenv("JANUS_DATABASE_PATH", legacy)
	t.Setenv("JANUS_IDENTITY_DATABASE_PATH", identity)
	t.Setenv("JANUS_PROJECTS_DATABASE_PATH", filepath.Join(t.TempDir(), "projects.db"))
	if err := runMigrate(ctx, []string{"split"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := a.startIdentityService(ctx); err != nil {
		t.Fatalf("starting after the split = %v", err)
	}
	defer a.identityService.Stop(ctx)
	found, err := a.identities.FindLocalUser(ctx, "alice@example.com")
	if err != nil || !found.Found || found.ID != "alice" {
		t.Fatalf("alice after the split = %#v, %v", found, err)
	}
}

func TestTheProjectsServiceWaitsForTheSplit(t *testing.T) {
	legacy := legacyJanusDB(t)
	projects := filepath.Join(t.TempDir(), "projects.db")
	a := newApplication(nil, nil)
	a.settings = settings{NATSAddress: "127.0.0.1:0", NATSDir: t.TempDir(), DatabasePath: legacy, ProjectsDatabasePath: projects}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.startNATS(ctx); err != nil {
		t.Fatal(err)
	}
	defer a.stopNATS(ctx)

	if err := a.startProjectsService(ctx); err == nil || !strings.Contains(err.Error(), "janus migrate split") {
		t.Fatalf("starting before the split = %v, want a refusal naming the command", err)
	}
	headscaleEnvironment(t, "", "", "")
	t.Setenv("JANUS_DATABASE_PATH", legacy)
	t.Setenv("JANUS_IDENTITY_DATABASE_PATH", filepath.Join(t.TempDir(), "identity.db"))
	t.Setenv("JANUS_PROJECTS_DATABASE_PATH", projects)
	if err := runMigrate(ctx, []string{"split"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := a.startProjectsService(ctx); err != nil {
		t.Fatalf("starting after the split = %v", err)
	}
	defer a.projectsService.Stop(ctx)
	if project, found, err := a.catalog.GetProject(ctx, "p1", "alice"); err != nil || !found || project.Name != "Legacy" {
		t.Fatalf("alice's project after the split = %#v, %v, %v", project, found, err)
	}
}
