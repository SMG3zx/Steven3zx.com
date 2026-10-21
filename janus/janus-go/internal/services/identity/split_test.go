package identity

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contractid "janus/internal/contract/identity"
	"janus/internal/platform/natstest"
)

const testSecret = "a-secret-of-at-least-thirty-two-bytes"

// legacySchema is the part of janus.db before the split that matters here: the
// identity tables and one table that refers to users.
const legacySchema = `
CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL DEFAULT '', display_name TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE local_credentials (user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, email TEXT NOT NULL UNIQUE COLLATE NOCASE, password_hash TEXT NOT NULL);
CREATE TABLE oauth_identities (provider TEXT NOT NULL, subject TEXT NOT NULL, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, PRIMARY KEY (provider, subject));
CREATE TABLE github_connections (user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, subject TEXT NOT NULL UNIQUE, token_ciphertext BLOB NOT NULL, connected_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE projects (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL);`

// legacyDatabase writes a janus.db from before the split with alice (local,
// connected to GitHub) and bob (Google), and returns its path.
func legacyDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "janus.db")
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ciphertext, err := New(Config{TokenSecret: testSecret}).encryptToken("gho-alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{legacySchema, nil},
		{`INSERT INTO users(id,email,display_name) VALUES('alice','Alice@example.com',''),('bob','bob@example.com','Bob')`, nil},
		{`INSERT INTO local_credentials(user_id,email,password_hash) VALUES('alice','Alice@example.com','argon-hash')`, nil},
		{`INSERT INTO oauth_identities(provider,subject,user_id) VALUES('google','g-bob','bob'),('github','gh-alice','alice')`, nil},
		{`INSERT INTO github_connections(user_id,subject,token_ciphertext) VALUES('alice','gh-alice',?)`, []any{ciphertext}},
		{`INSERT INTO projects(id,owner_id,name) VALUES('p1','alice','P')`, nil},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestTheSameUsersSignInAfterTheSplit(t *testing.T) {
	ctx := context.Background()
	legacy := legacyDatabase(t)
	path := filepath.Join(t.TempDir(), "identity.db")
	if needed, err := NeedsSplit(ctx, legacy, path); err != nil || !needed {
		t.Fatalf("NeedsSplit before = %v, %v, want true", needed, err)
	}

	copied, err := Split(ctx, legacy, path)
	if err != nil {
		t.Fatal(err)
	}
	want := SplitResult{"users": 2, "local_credentials": 1, "oauth_identities": 2, "github_connections": 1}
	for table, n := range want {
		if copied[table] != n {
			t.Errorf("copied %d rows of %s, want %d", copied[table], table, n)
		}
	}
	if needed, err := NeedsSplit(ctx, legacy, path); err != nil || needed {
		t.Fatalf("NeedsSplit after = %v, %v, want false", needed, err)
	}

	service := New(Config{DatabasePath: path, TokenSecret: testSecret})
	if err := service.Start(ctx, natstest.Start(t).Conn()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop(ctx)

	local, err := service.findLocalUser(ctx, contractid.FindLocalUser{Email: "alice@EXAMPLE.com"})
	if err != nil || !local.Found || local.ID != "alice" || local.PasswordHash != "argon-hash" {
		t.Fatalf("alice's local sign-in = %#v, %v", local, err)
	}
	google, err := service.resolveGoogle(ctx, contractid.ResolveIdentity{Subject: "g-bob", Email: "other@example.com"})
	if err != nil || google.ID != "bob" || google.DisplayName != "Bob" {
		t.Fatalf("bob's Google sign-in = %#v, %v", google, err)
	}
	github, err := service.resolveGitHubLogin(ctx, contractid.ResolveIdentity{Subject: "gh-alice", Email: "alice@example.com"})
	if err != nil || github.ID != "alice" {
		t.Fatalf("alice's GitHub sign-in = %#v, %v", github, err)
	}
	token, err := service.gitHubToken(ctx, contractid.GitHubToken{UserID: "alice"})
	if err != nil || !token.Connected || token.Token != "gho-alice" {
		t.Fatalf("alice's GitHub token = %#v, %v, want the token copied as it was encrypted", token, err)
	}

	// janus.db is only read: its rows, and what refers to them, stay.
	db, err := sql.Open("turso", legacy)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var users, projects int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM users),(SELECT COUNT(*) FROM projects)`).Scan(&users, &projects); err != nil || users != 2 || projects != 1 {
		t.Fatalf("janus.db after the split has %d users and %d projects, %v; want it unchanged", users, projects, err)
	}
}

func TestTheSplitRunsOnce(t *testing.T) {
	ctx := context.Background()
	legacy := legacyDatabase(t)
	path := filepath.Join(t.TempDir(), "identity.db")
	if _, err := Split(ctx, legacy, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Split(ctx, legacy, path); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a second split = %v, want a refusal", err)
	}
}

func TestAFailedSplitLeavesNoIdentityDatabase(t *testing.T) {
	ctx := context.Background()
	// A janus.db without the identity tables cannot be split.
	legacy := filepath.Join(t.TempDir(), "janus.db")
	db, err := sql.Open("turso", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL DEFAULT '', display_name TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	path := filepath.Join(t.TempDir(), "identity.db")
	if _, err := Split(ctx, legacy, path); err == nil {
		t.Fatal("split a database without identity tables")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("identity.db after a failed split: %v, want it gone so the split can run again", err)
	}
	if _, err := Split(ctx, filepath.Join(t.TempDir(), "missing.db"), path); err == nil {
		t.Fatal("split a database that does not exist")
	}
}

func TestNothingNeedsSplittingWithoutUsers(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if needed, err := NeedsSplit(ctx, filepath.Join(dir, "missing.db"), filepath.Join(dir, "identity.db")); err != nil || needed {
		t.Fatalf("no janus.db = %v, %v, want false", needed, err)
	}
	fresh := filepath.Join(dir, "fresh.db")
	db, err := sql.Open("turso", fresh)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if needed, err := NeedsSplit(ctx, fresh, filepath.Join(dir, "identity.db")); err != nil || needed {
		t.Fatalf("a janus.db created after the split = %v, %v, want false", needed, err)
	}
}

func TestTokensAreNotStoredInPlainText(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "identity.db")
	service := New(Config{DatabasePath: path, TokenSecret: testSecret})
	if err := service.Start(ctx, natstest.Start(t).Conn()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop(ctx)
	if _, err := service.createLocalUser(ctx, contractid.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.connectGitHub(ctx, contractid.ConnectGitHub{UserID: "alice", Subject: "gh", Token: "gho-plain-secret"}); err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	if err := service.db.QueryRow(`SELECT token_ciphertext FROM github_connections WHERE user_id='alice'`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), "gho-plain-secret") {
		t.Fatal("the GitHub token is stored in plain text")
	}
}

func TestStartNeedsATokenSecret(t *testing.T) {
	service := New(Config{DatabasePath: filepath.Join(t.TempDir(), "identity.db")})
	if err := service.Start(context.Background(), natstest.Start(t).Conn()); err == nil {
		_ = service.Stop(context.Background())
		t.Fatal("the service started with no key to protect tokens")
	}
}
