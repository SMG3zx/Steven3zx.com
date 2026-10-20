package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "turso.tech/database/tursogo"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS jobs (
	id TEXT PRIMARY KEY,
	company TEXT NOT NULL,
	title TEXT NOT NULL,
	location TEXT NOT NULL DEFAULT '',
	url TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	salary TEXT NOT NULL DEFAULT '',
	applied TEXT NOT NULL DEFAULT '',
	notes TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
PRAGMA temp_store = MEMORY;
`

var errNotFound = errors.New("not found")

// store owns the local Turso database of admin users and their sessions.
type store struct {
	db *sql.DB
}

// openStore opens the admin database encrypted at rest with AEGIS-256.
// hexKey is ADMIN_DB_KEY: 64 hex characters that are never written to disk.
func openStore(path, hexKey string) (*store, error) {
	if err := checkKey(hexKey); err != nil {
		return nil, err
	}
	db, err := sql.Open("turso", encryptedDSN(path, hexKey, "encryption"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// sql.Open accepts a wrong key; the first statement is what fails.
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &store{db: db}, nil
}

func checkKey(hexKey string) error {
	if len(hexKey) != 64 {
		return errors.New("ADMIN_DB_KEY must be 64 hex characters (openssl rand -hex 32)")
	}
	if _, err := hex.DecodeString(hexKey); err != nil {
		return errors.New("ADMIN_DB_KEY must be hex")
	}
	return nil
}

// encryptedDSN carries the key, so it must never be logged. Turso gates
// encryption (and ATTACH) behind its experimental features list.
func encryptedDSN(path, hexKey, experimental string) string {
	return fmt.Sprintf("%s?experimental=%s&encryption_cipher=aegis256&encryption_hexkey=%s",
		path, experimental, hexKey)
}

func (s *store) Close() error { return s.db.Close() }

type user struct {
	ID           string
	Username     string
	PasswordHash string
}

func (s *store) createUser(ctx context.Context, username, passwordHash string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		randomID(), username, passwordHash, time.Now().Unix())
	return err
}

func (s *store) setPassword(ctx context.Context, username, passwordHash string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE username = ?`, passwordHash, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	// A password change signs out every existing session for that user.
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE username = ?)`, username)
	return err
}

func (s *store) userByName(ctx context.Context, username string) (user, error) {
	var u user
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return u, errNotFound
	}
	return u, err
}

// createSession returns the raw token for the cookie; only its hash is stored.
func (s *store) createSession(ctx context.Context, userID string, ttl time.Duration) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	expires := now.Add(ttl)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now.Unix()); err != nil {
		return "", time.Time{}, err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), userID, now.Unix(), expires.Unix())
	return token, expires, err
}

func (s *store) sessionUser(ctx context.Context, token string) (user, error) {
	var u user
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		hashToken(token), time.Now().Unix()).Scan(&u.ID, &u.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return u, errNotFound
	}
	return u, err
}

func (s *store) deleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
