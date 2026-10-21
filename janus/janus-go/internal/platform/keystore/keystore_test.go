package keystore

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const secret = "0123456789abcdef0123456789abcdef"

func open(t *testing.T, path, secret string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path, secret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAKeyIsMadOnceAndReadBackAfterARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.db")
	first := open(t, path, secret)
	made := 0
	create := func() ([]byte, error) {
		made++
		return []byte("operator-seed-value"), nil
	}
	got, err := first.GetOrCreate(context.Background(), "operator", create)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "operator-seed-value" || made != 1 {
		t.Fatalf("first call = %q, made %d times", got, made)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	again := open(t, path, secret)
	got, err = again.GetOrCreate(context.Background(), "operator", create)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "operator-seed-value" || made != 1 {
		t.Fatalf("after a restart = %q, made %d times; the key was made again", got, made)
	}
}

func TestTheStoredValueIsEncrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.db")
	s := open(t, path, secret)
	plain := []byte("SAAPLAINTEXTSEED-do-not-store-me")
	if _, err := s.GetOrCreate(context.Background(), "agents", func() ([]byte, error) { return plain, nil }); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := s.db.QueryRow(`SELECT ciphertext FROM secret_keys WHERE name='agents'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, plain) || strings.Contains(string(raw), "SAAPLAINTEXTSEED") {
		t.Fatal("the database holds the key in plain text")
	}
}

func TestAnotherSecretCannotReadTheKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.db")
	s := open(t, path, secret)
	if _, err := s.GetOrCreate(context.Background(), "operator", func() ([]byte, error) { return []byte("seed"), nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	other := open(t, path, "ffffffffffffffffffffffffffffffff")
	_, err := other.GetOrCreate(context.Background(), "operator", func() ([]byte, error) { return []byte("new"), nil })
	if err == nil || !strings.Contains(err.Error(), "cannot decrypt") {
		t.Fatalf("read with another secret = %v, want a decryption refusal", err)
	}
}

func TestACiphertextMovedToAnotherNameDoesNotOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.db")
	s := open(t, path, secret)
	if _, err := s.GetOrCreate(context.Background(), "operator", func() ([]byte, error) { return []byte("seed"), nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE secret_keys SET name='janus' WHERE name='operator'`); err != nil {
		t.Fatal(err)
	}
	_, err := s.GetOrCreate(context.Background(), "janus", func() ([]byte, error) { return []byte("new"), nil })
	if err == nil {
		t.Fatal("a key read under another name decrypted")
	}
}

func TestASecretTooShortIsRefused(t *testing.T) {
	_, err := Open(context.Background(), filepath.Join(t.TempDir(), "keys.db"), "short")
	if err == nil {
		t.Fatal("a short secret opened the keystore")
	}
}
