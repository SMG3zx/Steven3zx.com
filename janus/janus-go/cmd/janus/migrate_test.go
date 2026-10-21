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
// User, and returns its path.
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
		`INSERT INTO users(id,email) VALUES('alice','alice@example.com')`,
		`INSERT INTO local_credentials(user_id,email,password_hash) VALUES('alice','alice@example.com','h')`,
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
	identity := filepath.Join(t.TempDir(), "identity.db")
	t.Setenv("JANUS_DATABASE_PATH", legacy)
	t.Setenv("JANUS_IDENTITY_DATABASE_PATH", identity)

	var out bytes.Buffer
	if err := runMigrate(context.Background(), []string{"split"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "local_credentials") || !strings.Contains(out.String(), "1 rows") {
		t.Fatalf("output:\n%s", out.String())
	}
	if err := runMigrate(context.Background(), []string{"split"}, &bytes.Buffer{}); err == nil {
		t.Fatal("the split ran twice")
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
