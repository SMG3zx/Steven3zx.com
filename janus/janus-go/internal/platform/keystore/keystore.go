// Package keystore keeps the signing keys of the NATS trust chain in a Turso
// database, encrypted at rest. A key is stored once under a name and read back
// on every start, so the operator and the accounts keep their identity across
// restarts. Its seeds are secrets: the database holds only their ciphertext, and
// the secret that opens it comes from configuration, never from the database.
package keystore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"janus/internal/platform/tursodb"
)

// minSecretLength is the shortest secret the store accepts, the same bound as
// the JWT secret.
const minSecretLength = 32

// migrations is the keystore database's schema. A key is a name and its
// encrypted value.
var migrations = []string{`
CREATE TABLE secret_keys (
	name TEXT PRIMARY KEY,
	ciphertext BLOB NOT NULL,
	created_at INTEGER NOT NULL
);
`}

// Store reads and writes encrypted keys.
type Store struct {
	db   *sql.DB
	aead cipher.AEAD
}

// Open opens the keystore at path and derives the encryption key from secret.
// The same secret must be used on every start; a different one cannot open the
// keys already stored.
func Open(ctx context.Context, path, secret string) (*Store, error) {
	if len(secret) < minSecretLength {
		return nil, fmt.Errorf("the key secret must contain at least %d bytes", minSecretLength)
	}
	db, err := tursodb.Open(ctx, path, migrations)
	if err != nil {
		return nil, fmt.Errorf("open the keystore: %w", err)
	}
	sum := sha256.Sum256([]byte("janus/nats-keys/v1\x00" + secret))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, aead: aead}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// GetOrCreate returns the key stored under name. The first time name is asked
// for, create makes the key and it is stored before the call returns, so a
// restart reads back the same key.
func (s *Store) GetOrCreate(ctx context.Context, name string, create func() ([]byte, error)) ([]byte, error) {
	var value []byte
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var ciphertext []byte
		err := tx.QueryRowContext(ctx, `SELECT ciphertext FROM secret_keys WHERE name=?`, name).Scan(&ciphertext)
		if err == nil {
			value, err = s.open(name, ciphertext)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read key %s: %w", name, err)
		}
		created, err := create()
		if err != nil {
			return fmt.Errorf("make key %s: %w", name, err)
		}
		ciphertext, err = s.seal(name, created)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO secret_keys(name,ciphertext,created_at) VALUES(?,?,?)`,
			name, ciphertext, time.Now().UnixNano()); err != nil {
			return fmt.Errorf("store key %s: %w", name, err)
		}
		value = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return value, nil
}

// seal encrypts value under a fresh nonce. The key name is authenticated, so a
// ciphertext moved to another name fails to open.
func (s *Store) seal(name string, value []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, value, []byte(name)), nil
}

func (s *Store) open(name string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < s.aead.NonceSize() {
		return nil, fmt.Errorf("stored key %s is invalid", name)
	}
	nonce, body := ciphertext[:s.aead.NonceSize()], ciphertext[s.aead.NonceSize():]
	value, err := s.aead.Open(nil, nonce, body, []byte(name))
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt stored key %s; the key secret changed", name)
	}
	return value, nil
}
