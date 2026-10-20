package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testKey      = "6c1f0b2a9d8e7f60514233a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8"
	otherKey     = "0000000000000000000000000000000000000000000000000000000000000001"
	testPassword = "correct horse battery staple"
)

// writePlainStore creates an unencrypted admin.db the way the server did
// before ENG-97, holding one admin with testPassword and one session.
func writePlainStore(t *testing.T, path, username string) {
	t.Helper()
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	hash, err := hashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, created_at) VALUES ('u1', ?, ?, 1)`,
		username, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('h1', 'u1', 1, ?)`,
		time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
}

func fileHeader(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 16 {
		t.Fatalf("%s is only %d bytes", path, len(b))
	}
	return b[:16]
}

// openWithoutKey reports the error from querying path with no encryption key.
func openWithoutKey(path string) error {
	db, err := sql.Open("turso", path)
	if err != nil {
		return err
	}
	defer db.Close()
	var n int
	return db.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
}

func TestOpenStoreRejectsMalformedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.db")
	for _, key := range []string{"", "abcd", strings.Repeat("z", 64), testKey + "00"} {
		if st, err := openStore(path, key); err == nil {
			st.Close()
			t.Errorf("openStore accepted key %q", key)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a rejected key still created %s", path)
	}
}

func TestOpenStoreEncryptsAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.db")
	st, err := openStore(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.createUser(context.Background(), "steven", "hash-marker-value"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	if h := fileHeader(t, path); !bytes.HasPrefix(h, []byte("Turso")) {
		t.Fatalf("file header %q is not an encrypted Turso header", h)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("hash-marker-value")) {
		t.Fatal("plaintext row data found in the database file")
	}
	if err := openWithoutKey(path); err == nil {
		t.Fatal("opening the encrypted file without a key succeeded")
	}
	if st, err := openStore(path, otherKey); err == nil {
		st.Close()
		t.Fatal("opening the encrypted file with the wrong key succeeded")
	}

	st, err = openStore(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.userByName(context.Background(), "steven"); err != nil {
		t.Fatalf("reopening with the key: %v", err)
	}
}

func TestEncryptPlainStoreMigratesAndAccountSignsIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin.db")
	writePlainStore(t, path, "steven")

	migrated, err := encryptPlainStore(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Fatal("encryptPlainStore reported no migration for a plain file")
	}

	backup := path + plainBackupSuffix
	if h := fileHeader(t, backup); !bytes.HasPrefix(h, []byte("SQLite format 3")) {
		t.Fatalf("backup header %q is not the original plain file", h)
	}
	if h := fileHeader(t, path); !bytes.HasPrefix(h, []byte("Turso")) {
		t.Fatalf("migrated file header %q is not encrypted", h)
	}
	for _, f := range []string{path, path + "-wal"} {
		if raw, err := os.ReadFile(f); err == nil && bytes.Contains(raw, []byte("argon2id")) {
			t.Fatalf("plaintext password hash found in %s", f)
		}
	}
	if _, err := os.Stat(path + encryptingSuffix); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the temporary encrypted file was left behind")
	}
	if err := openWithoutKey(path); err == nil {
		t.Fatal("opening the migrated file without a key succeeded")
	}

	st, err := openStore(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var sessions int
	if err := st.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sessions after migration = %d, %v; want 1", sessions, err)
	}

	srv := &server{store: st, ipLimiter: newLimiter(5, time.Minute), userLimiter: newLimiter(5, time.Minute)}
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login",
		strings.NewReader(`{"username":"steven","password":"`+testPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login after migration: %d %s", rec.Code, rec.Body)
	}
}

func TestEncryptPlainStoreLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "missing.db")
	if migrated, err := encryptPlainStore(missing, testKey); err != nil || migrated {
		t.Fatalf("missing file: migrated=%v err=%v", migrated, err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("encryptPlainStore created a file that did not exist")
	}

	encrypted := filepath.Join(dir, "admin.db")
	st, err := openStore(encrypted, testKey)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	before, _ := os.ReadFile(encrypted)
	if migrated, err := encryptPlainStore(encrypted, testKey); err != nil || migrated {
		t.Fatalf("encrypted file: migrated=%v err=%v", migrated, err)
	}
	after, _ := os.ReadFile(encrypted)
	if !bytes.Equal(before, after) {
		t.Fatal("encryptPlainStore rewrote an already encrypted file")
	}
}

func TestEncryptPlainStoreRefusesToOverwriteBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.db")
	writePlainStore(t, path, "steven")
	if err := os.WriteFile(path+plainBackupSuffix, []byte("older backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := encryptPlainStore(path, testKey); err == nil {
		t.Fatal("encryptPlainStore overwrote an existing backup")
	}
	if h := fileHeader(t, path); !bytes.HasPrefix(h, []byte("SQLite format 3")) {
		t.Fatal("the plain database was changed after a refused migration")
	}
}

func TestEncryptPlainStoreFinishesInterruptedSwap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.db")
	writePlainStore(t, path, "steven")
	if _, err := encryptPlainStore(path, testKey); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash between the two renames: the plain file is already
	// the backup, but the encrypted copy has not taken its place yet.
	if err := os.Rename(path, path+encryptingSuffix); err != nil {
		t.Fatal(err)
	}

	migrated, err := encryptPlainStore(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Fatal("an interrupted swap was not reported as a migration")
	}
	st, err := openStore(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.userByName(context.Background(), "steven"); err != nil {
		t.Fatalf("user after finishing the swap: %v", err)
	}
}
