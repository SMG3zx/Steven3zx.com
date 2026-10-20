package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

const (
	// plainBackupSuffix names the unencrypted original kept after migration.
	// It still holds password hashes; delete it once sign-in works.
	plainBackupSuffix = ".plain-backup"
	// encryptingSuffix names the encrypted copy while it is being built.
	encryptingSuffix = ".encrypting"
)

var sqliteHeader = []byte("SQLite format 3\x00")

// encryptPlainStore converts an unencrypted admin database at path into one
// encrypted with hexKey, once. Turso cannot encrypt a file in place, so it
// builds an encrypted copy beside it by ATTACHing the plain file, then swaps
// the files and keeps the original as path+plainBackupSuffix. A missing or
// already encrypted file is left alone. It reports whether it changed path.
//
// Turso locks the file, so nothing else may have it open (the server runs
// this before it opens the store).
func encryptPlainStore(path, hexKey string) (bool, error) {
	if err := checkKey(hexKey); err != nil {
		return false, err
	}
	tmp, backup := path+encryptingSuffix, path+plainBackupSuffix

	plain, err := hasPlainHeader(path)
	if errors.Is(err, fs.ErrNotExist) {
		// A crash between the two renames leaves the backup and the finished
		// copy but no database; complete the swap.
		if exists(tmp) && exists(backup) {
			return true, renameWithWAL(tmp, path)
		}
		return false, nil
	}
	if err != nil || !plain {
		return false, err
	}
	if exists(backup) {
		return false, fmt.Errorf("%s already exists; move it away before migrating %s", backup, path)
	}

	if err := checkpoint(path); err != nil {
		return false, fmt.Errorf("checkpoint plain database: %w", err)
	}
	removeWithWAL(tmp) // a leftover from an earlier failed attempt
	if err := copyIntoEncrypted(path, tmp, hexKey); err != nil {
		removeWithWAL(tmp)
		return false, err
	}
	if err := renameWithWAL(path, backup); err != nil {
		return false, err
	}
	return true, renameWithWAL(tmp, path)
}

func copyIntoEncrypted(plainPath, encPath, hexKey string) error {
	db, err := sql.Open("turso", encryptedDSN(encPath, hexKey, "encryption,attach"))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		schema,
		`ATTACH '` + strings.ReplaceAll(plainPath, `'`, `''`) + `' AS old`,
		`INSERT INTO users (id, username, password_hash, created_at)
			SELECT id, username, password_hash, created_at FROM old.users`,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
			SELECT token_hash, user_id, created_at, expires_at FROM old.sessions`,
		`DETACH old`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("copy into encrypted database: %w", err)
		}
	}
	return db.Close()
}

// checkpoint folds the plain database's WAL into its main file so ATTACH
// and the backup both see every committed row.
func checkpoint(path string) error {
	db, err := sql.Open("turso", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return err
	}
	return db.Close()
}

func hasPlainHeader(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, len(sqliteHeader))
	if _, err := io.ReadFull(f, head); err != nil {
		// Too short to be a SQLite database; let openStore deal with it.
		return false, nil
	}
	return bytes.Equal(head, sqliteHeader), nil
}

func renameWithWAL(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	if exists(from + "-wal") {
		return os.Rename(from+"-wal", to+"-wal")
	}
	return nil
}

func removeWithWAL(path string) {
	os.Remove(path)
	os.Remove(path + "-wal")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
